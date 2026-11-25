package dns

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// DnsHandler processes incoming DNS queries and returns responses.
// It uses the Router to determine which upstream to query, and the
// UpstreamManager to send queries to upstream DNS servers.
type DnsHandler struct {
	timeout time.Duration
	module  *DnsModule
}

// prefetchAsync 异步执行预取，后台刷新缓存条目
func (h *DnsHandler) prefetchAsync(ctx context.Context, upstream *UpstreamInstance,
	upstreamMgr *UpstreamManager, query *DnsQuery, key CacheKey) {

	if h.module == nil {
		return
	}
	cache := h.module.GetCache()
	if cache == nil {
		return
	}

	if upstream == nil || upstreamMgr == nil {
		cache.SetPrefetching(key, false)
		return
	}

	// 记录预取指标
	if h.module.metrics != nil {
		h.module.metrics.RecordPrefetch()
	}

	dnsLogf("[dns] prefetch: %s %s via %s (tag=%s)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, upstream.ProxyTag)

	timeout := h.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if ctx == nil {
		ctx = context.Background()
	}
	prefetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := upstreamMgr.ExchangeContext(prefetchCtx, upstream, query)
	if err != nil {
		log.Printf("[dns] prefetch error for %s %s: %v",
			dns.Type(uint16(query.QType)).String(), query.Name, err)
		cache.RecordPrefetchFailure(key)
		return
	}

	if resp == nil {
		cache.RecordPrefetchFailure(key)
		return
	}

	// If the refreshed response is negative (NXDOMAIN/SERVFAIL/REFUSED/etc.),
	// never call RecordPrefetchSuccess on an un-updated expiring entry (which
	// would reset PrefetchFailCount=0 and cause an immediate prefetch storm).
	if resp.Rcode != dns.RcodeSuccess {
		if !cache.NegativeCacheEnabled() {
			if resp.Rcode == dns.RcodeNameError {
				cache.Remove(key)
			} else {
				cache.RecordPrefetchFailure(key)
			}
			return
		}
		cache.Set(key, resp, resp.TTL)
		cache.RecordPrefetchFailure(key)
		return
	}

	// 预取成功，更新缓存
	cache.Set(key, resp, resp.TTL)
	cache.RecordPrefetchSuccess(key)

	dnsLogf("[dns] prefetch completed: %s %s via %s",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr)
}

// buildCacheKey 构建完整的缓存键（路由后已知上游和代理渠道）
func buildCacheKey(query *DnsQuery, upstream *UpstreamInstance) CacheKey {
	proxyTag := "direct"
	if upstream != nil && upstream.ProxyTag != "" {
		proxyTag = upstream.ProxyTag
	}
	upstreamAddr := ""
	if upstream != nil {
		upstreamAddr = upstream.Addr
		// A response obtained from a subscription's fallback must not be reused
		// by another policy that only happens to share the primary resolver.
		if len(upstream.Fallback) > 0 {
			upstreamAddr += "|fallback:" + upstream.ID + ":" + strings.Join(upstream.Fallback, ",")
		}
	}
	qclass := query.QClass
	if qclass == 0 {
		qclass = dns.ClassINET
	}
	var clientSubnet netip.Prefix
	if !isProxiedUpstream(upstream) {
		clientSubnet = upstreamClientSubnet(query.ClientIP)
	}
	return CacheKey{
		ClientSubnet: clientSubnet,
		UpstreamAddr: upstreamAddr,
		ProxyTag:     proxyTag,
		Name:         normalizeDomain(query.Name),
		QType:        query.QType,
		QClass:       qclass,
		DNSSECOK:     query.DNSSECOK,
	}
}

func lookupUpstream(mgr *UpstreamManager, u UpstreamConfig) *UpstreamInstance {
	if mgr == nil {
		return nil
	}
	if u.ID != "" {
		if inst, ok := mgr.GetUpstream(u.ID); ok && inst != nil {
			return inst
		}
	}
	if u.Addr != "" {
		if inst, ok := mgr.GetUpstream(u.Addr); ok && inst != nil {
			return inst
		}
	}
	return nil
}

func upstreamMetricKey(upstream *UpstreamInstance, fallbackID string) string {
	if upstream != nil {
		if upstream.ID != "" {
			return upstream.ID
		}
		if upstream.Addr != "" {
			return upstream.Addr
		}
	}
	return fallbackID
}

// NewDnsHandler creates a new DnsHandler with the given timeout and module reference.
func NewDnsHandler(timeout time.Duration, module *DnsModule) *DnsHandler {
	return &DnsHandler{
		timeout: timeout,
		module:  module,
	}
}

// HandleQuery processes a DNS query by routing it through the router
// and sending it to the appropriate upstream server.
//
// Processing flow:
//  1. Route the query using the Router.
//  2. Based on the action (route/reject/bypass), take appropriate action.
//  3. For "route": send the query to the matched upstream via UpstreamManager.
//  4. For "reject": return a REFUSED response.
//  5. For "bypass": send the query directly (bypass routing).
//  6. Track statistics.
func (h *DnsHandler) HandleQuery(ctx context.Context, query *DnsQuery) (*DnsResponse, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("dns handler: context cancelled: %w", ctx.Err())
	default:
	}

	// Log the incoming query (structured, concise).
	clientIP := "unknown"
	if query.ClientIP != nil {
		clientIP = query.ClientIP.String()
	}
	dnsLogf("[dns] query: %s %s from %s", QTypeToString(query.QType), query.Name, clientIP)

	// Get module references.
	module := h.module
	if module == nil {
		// Fallback: return stub response if no module configured.
		log.Printf("[dns] no module configured, returning stub response for %s", query.Name)
		return h.buildStubResponse(query), nil
	}

	router := module.GetRouter()
	upstreamMgr := module.GetUpstreamManager()

	// Track total queries (backward compatible stats).
	module.stats.mu.Lock()
	module.stats.TotalQueries++
	module.stats.mu.Unlock()

	// Step 1: Route the query.
	var result *RouteResult
	if router != nil {
		result = router.Route(query)
	} else {
		// No router — use default behavior.
		result = &RouteResult{
			Action: "route",
			Policy: "single",
		}
	}

	// Step 2: Handle based on action.
	switch result.Action {
	case "reject":
		return h.handleReject(query, module, result)

	case "bypass":
		return h.handleBypass(ctx, query, module, result, upstreamMgr)

	default: // "route"
		return h.handleRoute(ctx, query, module, result, router, upstreamMgr)
	}
}

func (h *DnsHandler) selectFallbackUpstream(module *DnsModule, router *Router, upstreamMgr *UpstreamManager, excludeID string) *UpstreamInstance {
	if upstreamMgr == nil {
		return nil
	}

	// 1. Prefer the router/config default upstream.
	defaultID := ""
	if router != nil {
		defaultID = router.DefaultUpstream()
	}
	if defaultID == "" && module != nil {
		defaultID = resolveDefaultUpstream(module.Config())
	}
	if defaultID != "" && defaultID != excludeID {
		if u, ok := upstreamMgr.GetUpstream(defaultID); ok && u != nil {
			return u
		}
	}

	var cfgUpstreams []UpstreamConfig
	if module != nil && module.Config() != nil {
		cfgUpstreams = module.Config().Upstreams
	}

	// 2. Scan configured non-bootstrap upstreams (preferring last, matching default convention),
	// skipping any entries whose GetUpstream lookup returns nil.
	for i := len(cfgUpstreams) - 1; i >= 0; i-- {
		if cfgUpstreams[i].Bootstrap {
			continue
		}
		if inst := lookupUpstream(upstreamMgr, cfgUpstreams[i]); inst != nil && inst.ID != excludeID {
			return inst
		}
	}

	// 3. Scan any configured upstream, skipping nil lookup results.
	for _, u := range cfgUpstreams {
		if inst := lookupUpstream(upstreamMgr, u); inst != nil && inst.ID != excludeID {
			return inst
		}
	}

	// 4. Final fallback: any upstream registered directly in UpstreamManager.
	for _, inst := range upstreamMgr.ListUpstreams() {
		if inst != nil && !inst.Bootstrap && inst.ID != excludeID {
			return inst
		}
	}
	for _, inst := range upstreamMgr.ListUpstreams() {
		if inst != nil && inst.ID != excludeID {
			return inst
		}
	}
	return nil
}

// handleRoute sends the query to the matched upstream server.
// 查询流程（方案2：先路由再查缓存）：
//
//  1. Route the query to determine upstream + proxy channel.
//  2. Build full CacheKey (with upstream + proxy tag).
//  3. Check cache. If hit: return cached response, optionally trigger prefetch.
//  4. If miss: send query to upstream, apply post-response IP checks, store result in cache, return response.
func (h *DnsHandler) handleRoute(ctx context.Context, query *DnsQuery, module *DnsModule,
	result *RouteResult, router *Router, upstreamMgr *UpstreamManager) (*DnsResponse, error) {

	upstreamID := result.UpstreamID
	var upstream *UpstreamInstance

	if upstreamMgr != nil && upstreamID != "" {
		var ok bool
		upstream, ok = upstreamMgr.GetUpstream(upstreamID)
		if !ok || upstream == nil {
			log.Printf("[dns] upstream %q not found, using default", upstreamID)
			upstream = nil
		}
	}

	// If no upstream found, prefer defaultUpstream and skip nil GetUpstream results.
	if upstream == nil && upstreamMgr != nil {
		upstream = h.selectFallbackUpstream(module, router, upstreamMgr, "")
	}

	if upstream == nil || upstreamMgr == nil {
		log.Printf("[dns] no upstream available for %s %s, returning stub", QTypeToString(query.QType), query.Name)
		return h.buildStubResponse(query), nil
	}

	metricKey := upstreamMetricKey(upstream, upstreamID)

	// Build full CacheKey after routing (upstream + proxy tag known).
	cacheKey := buildCacheKey(query, upstream)

	// Record route metrics.
	if module.metrics != nil {
		module.metrics.RecordQuery(query, metricKey)
		module.metrics.RecordAction("route")
		if result.RuleID != "" {
			module.metrics.RecordRuleHit(result.RuleID)
		}
	}

	// Log route decision (structured, concise).
	proxyInfo := upstream.ProxyTag
	if proxyInfo == "" {
		proxyInfo = "direct"
	}
	ruleInfo := ""
	if result.RuleID != "" {
		ruleInfo = fmt.Sprintf(" rule(%s)", result.RuleID)
	}
	dnsLogf("[dns] route: %s %s → %s(%s)%s action(route)",
		QTypeToString(query.QType), query.Name, metricKey, proxyInfo, ruleInfo)

	// Check cache (if enabled).
	cache := module.GetCache()
	if cache != nil {
		if cached, ok := cache.Get(cacheKey); ok {
			dnsLogf("[dns] cache-hit: %s %s",
				QTypeToString(query.QType), query.Name)

			// Track cache hit (backward compatible stats).
			module.stats.mu.Lock()
			module.stats.CacheHits++
			module.stats.mu.Unlock()

			// Track cache hit (metrics).
			if module.metrics != nil {
				module.metrics.RecordCacheHit()
			}

			// Atomically check & claim prefetch slot (eliminating TOCTOU race).
			// Use module lifecycle context so client request completion doesn't cancel prefetch.
			if cache.TryStartPrefetch(cacheKey) {
				prefetchQuery := *query
				module.wg.Go(func() {
					h.prefetchAsync(module.lifecycleCtx(), upstream, upstreamMgr, &prefetchQuery, cacheKey)
				})
			}

			return cached, nil
		}

		// Log cache miss.
		dnsLogf("[dns] cache-miss: %s %s", QTypeToString(query.QType), query.Name)

		// Track cache miss (backward compatible stats).
		module.stats.mu.Lock()
		module.stats.CacheMisses++
		module.stats.mu.Unlock()

		// Track cache miss (metrics).
		if module.metrics != nil {
			module.metrics.RecordCacheMiss()
		}
	}

	// Send query to upstream (cache miss or cache disabled).
	start := time.Now()
	resp, err := upstreamMgr.ExchangeContext(ctx, upstream, query)
	rtt := time.Since(start)

	if err != nil {
		log.Printf("[dns] error: %s %s: %v", upstream.Addr, query.Name, err)

		// Track error in stats.
		module.stats.mu.Lock()
		module.stats.RoutedQueries[metricKey]++
		module.stats.mu.Unlock()

		// Track error in metrics.
		if module.metrics != nil {
			module.metrics.RecordError(metricKey, err)
		}

		return nil, fmt.Errorf("dns handler: upstream error: %w", err)
	}

	if resp == nil {
		log.Printf("[dns] empty response for %s %s", QTypeToString(query.QType), query.Name)
		return h.buildStubResponse(query), nil
	}

	// Post-response IP verification (ExpectedIPs): if the rule specified expected
	// response IP CIDRs and the answer contains A/AAAA records outside those CIDRs,
	// fall back to the default upstream.
	if len(result.ExpectedIPs) > 0 && responseHasAddressRecords(resp) && !matchAnyIPResponse(result.ExpectedIPs, resp) {
		if fallback := h.selectFallbackUpstream(module, router, upstreamMgr, upstream.ID); fallback != nil {
			dnsLogf("[dns] response IP mismatch for %s via %s (rule=%s), falling back to %s",
				query.Name, metricKey, result.RuleID, fallback.ID)
			fbStart := time.Now()
			if fbResp, fbErr := upstreamMgr.ExchangeContext(ctx, fallback, query); fbErr == nil && fbResp != nil {
				resp = fbResp
				rtt = time.Since(fbStart)
				proxyInfo = fallback.ProxyTag
				if proxyInfo == "" {
					proxyInfo = "direct"
				}
			}
		}
	}

	// Post-response IP reject rules (IPRule with action=reject).
	if router != nil {
		if postReject := router.MatchResponse(query, resp); postReject != nil && postReject.Action == "reject" {
			return h.handleReject(query, module, postReject)
		}
	}

	// Log successful upstream response.
	dnsLogf("[dns] upstream: %s via %s (rtt=%dms)",
		upstream.Addr, proxyInfo, rtt.Milliseconds())

	// Store response in cache (if enabled and not a bootstrap query).
	if cache != nil && !query.IsBootstrap {
		cache.Set(cacheKey, resp, resp.TTL)
	}

	// Track statistics.
	module.stats.mu.Lock()
	module.stats.RoutedQueries[metricKey]++
	module.stats.AvgRTT = updateAvgRTT(module.stats.AvgRTT, rtt, module.stats.TotalQueries)
	module.stats.mu.Unlock()

	// Record response metrics under the same unified upstream key as RecordQuery.
	if module.metrics != nil {
		module.metrics.RecordResponseForUpstream(metricKey, resp, rtt)
	}

	return resp, nil
}

// handleReject returns a REFUSED response for rejected queries.
func (h *DnsHandler) handleReject(query *DnsQuery, module *DnsModule, result *RouteResult) (*DnsResponse, error) {
	dnsLogf("[dns] reject: %s %s (rule=%s)", QTypeToString(query.QType), query.Name, result.RuleID)

	// Track statistics.
	module.stats.mu.Lock()
	module.stats.RejectedQueries++
	module.stats.mu.Unlock()

	// Record metrics.
	if module.metrics != nil {
		module.metrics.RecordQuery(query, "")
		module.metrics.RecordAction("reject")
		if result.RuleID != "" {
			module.metrics.RecordRuleHit(result.RuleID)
		}
	}

	return h.buildRefused(query), nil
}

// handleBypass handles bypass queries — sends directly without proxy routing.
func (h *DnsHandler) handleBypass(ctx context.Context, query *DnsQuery, module *DnsModule, result *RouteResult,
	upstreamMgr *UpstreamManager) (*DnsResponse, error) {

	dnsLogf("[dns] bypass: %s %s (rule=%s)", QTypeToString(query.QType), query.Name, result.RuleID)

	// Track statistics.
	module.stats.mu.Lock()
	module.stats.BypassedQueries++
	module.stats.mu.Unlock()

	// For bypass, select a direct upstream or clone the chosen upstream as direct
	// so action="bypass" genuinely bypasses proxy routing.
	var upstream *UpstreamInstance
	if upstreamMgr != nil {
		if result.UpstreamID != "" {
			if u, ok := upstreamMgr.GetUpstream(result.UpstreamID); ok && u != nil {
				upstream = u
			}
		}

		var cfgUpstreams []UpstreamConfig
		if module != nil && module.Config() != nil {
			cfgUpstreams = module.Config().Upstreams
		}

		// Prefer a direct non-bootstrap upstream if none was explicitly specified.
		if upstream == nil {
			for _, u := range cfgUpstreams {
				if u.Bootstrap || (u.ProxyTag != "" && u.ProxyTag != "direct") {
					continue
				}
				if inst := lookupUpstream(upstreamMgr, u); inst != nil {
					upstream = inst
					break
				}
			}
		}

		// Next fall back to any available non-bootstrap upstream (will be cloned as direct).
		if upstream == nil {
			for _, u := range cfgUpstreams {
				if u.Bootstrap {
					continue
				}
				if inst := lookupUpstream(upstreamMgr, u); inst != nil {
					upstream = inst
					break
				}
			}
		}

		// Final fallback: any available upstream in config or manager.
		if upstream == nil {
			for _, u := range cfgUpstreams {
				if inst := lookupUpstream(upstreamMgr, u); inst != nil {
					upstream = inst
					break
				}
			}
		}
		if upstream == nil {
			for _, inst := range upstreamMgr.ListUpstreams() {
				if inst != nil && !isProxiedUpstream(inst) {
					upstream = inst
					break
				}
			}
		}
		if upstream == nil {
			for _, inst := range upstreamMgr.ListUpstreams() {
				if inst != nil {
					upstream = inst
					break
				}
			}
		}
	}

	if upstream == nil || upstreamMgr == nil {
		return h.buildStubResponse(query), nil
	}

	// Ensure bypass truly uses a direct path even if the selected upstream has a proxy tag.
	if isProxiedUpstream(upstream) {
		directCopy := *upstream
		directCopy.ProxyTag = "direct"
		upstream = &directCopy
	}

	metricKey := upstreamMetricKey(upstream, result.UpstreamID)

	// Record bypass metrics.
	if module.metrics != nil {
		module.metrics.RecordQuery(query, metricKey)
		module.metrics.RecordAction("bypass")
		if result.RuleID != "" {
			module.metrics.RecordRuleHit(result.RuleID)
		}
	}

	start := time.Now()
	resp, err := upstreamMgr.ExchangeContext(ctx, upstream, query)
	rtt := time.Since(start)

	if err != nil {
		log.Printf("[dns] error: %s %s: %v", upstream.Addr, query.Name, err)
		if module.metrics != nil {
			module.metrics.RecordError(metricKey, err)
		}
		return nil, fmt.Errorf("dns handler: bypass upstream error: %w", err)
	}

	if resp == nil {
		return h.buildStubResponse(query), nil
	}

	// Log successful bypass response.
	dnsLogf("[dns] upstream: %s direct (rtt=%dms)",
		upstream.Addr, rtt.Milliseconds())

	module.stats.mu.Lock()
	module.stats.RoutedQueries[metricKey]++
	module.stats.AvgRTT = updateAvgRTT(module.stats.AvgRTT, rtt, module.stats.TotalQueries)
	module.stats.mu.Unlock()

	// Record response metrics under the same unified upstream key.
	if module.metrics != nil {
		module.metrics.RecordResponseForUpstream(metricKey, resp, rtt)
	}

	return resp, nil
}

// HandleQueryWithTimeout wraps HandleQuery with a timeout derived from the config.
func (h *DnsHandler) HandleQueryWithTimeout(query *DnsQuery, timeout time.Duration) (*DnsResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return h.HandleQuery(ctx, query)
}

// buildStubResponse creates a minimal stub response echoing the query.
func (h *DnsHandler) buildStubResponse(query *DnsQuery) *DnsResponse {
	resp := &DnsResponse{
		Query:    *query,
		Rcode:    dns.RcodeSuccess,
		Upstream: "stub",
		ProxyTag: "direct",
		RTT:      0,
		Cached:   false,
	}

	qclass := query.QClass
	if qclass == 0 {
		qclass = dns.ClassINET
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(query.Name), uint16(query.QType))
	m.Question[0].Qclass = qclass
	m.Response = true
	m.RecursionAvailable = true
	m.Rcode = dns.RcodeSuccess

	resp.RawMsg = m
	return resp
}

// buildRefused creates a REFUSED DNS response.
func (h *DnsHandler) buildRefused(query *DnsQuery) *DnsResponse {
	qclass := query.QClass
	if qclass == 0 {
		qclass = dns.ClassINET
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(query.Name), uint16(query.QType))
	m.Question[0].Qclass = qclass
	m.Response = true
	m.Rcode = dns.RcodeRefused

	return &DnsResponse{
		Query:    *query,
		RawMsg:   m,
		Rcode:    dns.RcodeRefused,
		Upstream: "",
		ProxyTag: "",
		RTT:      0,
		Cached:   false,
	}
}

// updateAvgRTT updates the average RTT using exponential moving average.
func updateAvgRTT(current time.Duration, sample time.Duration, count int64) time.Duration {
	if count <= 1 {
		return sample
	}
	// Use weighted average: new = old * (n-1)/n + sample/n
	avg := int64(current)*(count-1)/count + int64(sample)/count
	return time.Duration(avg)
}

// QueryTypeFromString parses a DNS query type string (e.g., "A", "AAAA") to QueryType.
func QueryTypeFromString(s string) (QueryType, error) {
	s = strings.TrimSpace(s)
	switch strings.ToUpper(s) {
	case "A":
		return TypeA, nil
	case "AAAA":
		return TypeAAAA, nil
	case "CNAME":
		return TypeCNAME, nil
	case "TXT":
		return TypeTXT, nil
	case "MX":
		return TypeMX, nil
	case "SRV":
		return TypeSRV, nil
	case "NS":
		return TypeNS, nil
	case "PTR":
		return TypePTR, nil
	case "SOA":
		return TypeSOA, nil
	default:
		// Try to parse as numeric value.
		return 0, fmt.Errorf("unknown query type: %s", s)
	}
}
