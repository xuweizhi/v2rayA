package dns

import (
	"fmt"
	"log"
	"sync"

	"github.com/miekg/dns"
)

// RouteResult represents the result of routing a DNS query.
type RouteResult struct {
	// UpstreamID is the ID of the matched upstream server.
	UpstreamID string
	// UpstreamAddr is the address of the matched upstream server.
	UpstreamAddr string
	// Action is the routing action: route / reject / bypass.
	Action string
	// RuleID is the ID of the matched rule.
	RuleID string
	// Policy is the query policy: single / parallel / fallback.
	Policy string
	// ProxyTag is the proxy channel tag associated with the upstream.
	// Empty or "direct" means direct connection; other values indicate
	// a specific proxy outbound tag.
	ProxyTag string
	// ExpectedIPs contains optional response-IP CIDR constraints from the
	// matched rule. When non-empty on a route result, the handler verifies
	// resolved A/AAAA answer IPs against these CIDRs post-response.
	ExpectedIPs []*IPRule
}

type compiledRule struct {
	queryMatchers []RuleMatcher
	ipRules       []*IPRule
}

// Router is the DNS query routing engine.
// It maintains an ordered list of rules and matches incoming queries
// against them to determine the upstream server.
type Router struct {
	rules            []*DnsRule        // Ordered list of routing rules.
	matchers         [][]RuleMatcher   // Pre-built matchers for each rule.
	compiled         []compiledRule    // Split query/response matchers for each rule.
	upstreamMap      map[string]string // Upstream ID → upstream address mapping.
	upstreamProxyTag map[string]string // Upstream ID → proxy tag mapping.
	defaultUpstream  string            // Default upstream ID used when no rule matches.
	geositeResolver  GeositeResolver   // Resolves geosite tags (set externally).
	mu               sync.RWMutex
}

// resolveDefaultUpstream determines the default upstream ID for cfg.
// It honors cfg.DefaultUpstream when explicitly configured, otherwise falling
// back to the last non-bootstrap upstream (or the last upstream if all are bootstrap).
func resolveDefaultUpstream(cfg *DnsModuleConfig) string {
	if cfg == nil {
		return ""
	}
	if cfg.DefaultUpstream != "" {
		return cfg.DefaultUpstream
	}
	defaultUpstream := ""
	for _, u := range cfg.Upstreams {
		if !u.Bootstrap {
			defaultUpstream = u.ID
			if defaultUpstream == "" {
				defaultUpstream = u.Addr
			}
		}
	}
	if defaultUpstream == "" && len(cfg.Upstreams) > 0 {
		u := cfg.Upstreams[len(cfg.Upstreams)-1]
		defaultUpstream = u.ID
		if defaultUpstream == "" {
			defaultUpstream = u.Addr
		}
	}
	return defaultUpstream
}

func buildUpstreamMaps(upstreams []UpstreamConfig) (map[string]string, map[string]string) {
	upstreamMap := make(map[string]string, len(upstreams))
	upstreamProxyTag := make(map[string]string, len(upstreams))
	for _, u := range upstreams {
		key := u.ID
		if key == "" {
			key = u.Addr
		}
		if key != "" {
			upstreamMap[key] = u.Addr
			if u.ProxyTag != "" {
				upstreamProxyTag[key] = u.ProxyTag
			}
		}
	}
	return upstreamMap, upstreamProxyTag
}

func splitCompiledRule(matchers []RuleMatcher) compiledRule {
	var cr compiledRule
	for _, m := range matchers {
		switch v := m.(type) {
		case *IPRule:
			cr.ipRules = append(cr.ipRules, v)
		case *OrMatcher:
			if len(v.Matchers) > 0 {
				allIP := true
				for _, child := range v.Matchers {
					if _, ok := child.(*IPRule); !ok {
						allIP = false
						break
					}
				}
				if allIP {
					for _, child := range v.Matchers {
						cr.ipRules = append(cr.ipRules, child.(*IPRule))
					}
					continue
				}
			}
			cr.queryMatchers = append(cr.queryMatchers, m)
		default:
			cr.queryMatchers = append(cr.queryMatchers, m)
		}
	}
	return cr
}

func compileRules(rules []*DnsRule, resolver GeositeResolver) ([]*DnsRule, [][]RuleMatcher, []compiledRule, error) {
	newRules := make([]*DnsRule, len(rules))
	newMatchers := make([][]RuleMatcher, len(rules))
	newCompiled := make([]compiledRule, len(rules))

	for i, rule := range rules {
		matchers, err := BuildMatchers(rule, resolver)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("rule %d (id=%q): %w", i, rule.ID, err)
		}
		cp := *rule
		newRules[i] = &cp
		newMatchers[i] = matchers
		newCompiled[i] = splitCompiledRule(matchers)
	}
	return newRules, newMatchers, newCompiled, nil
}

func matchAnyIPQuery(ipRules []*IPRule, query *DnsQuery) bool {
	for _, ir := range ipRules {
		if ir.Match(query) {
			return true
		}
	}
	return false
}

func matchAnyIPResponse(ipRules []*IPRule, resp *DnsResponse) bool {
	for _, ir := range ipRules {
		if ir.MatchResponse(resp) {
			return true
		}
	}
	return false
}

func responseHasAddressRecords(resp *DnsResponse) bool {
	if resp == nil {
		return false
	}
	for _, rr := range resp.Answer {
		switch rr.(type) {
		case *dns.A, *dns.AAAA:
			return true
		}
	}
	return false
}

// NewRouter creates a new Router with the given rules and default upstream.
func NewRouter(rules []*DnsRule, upstreams []UpstreamConfig, defaultUpstream string) (*Router, error) {
	return newRouter(rules, upstreams, defaultUpstream, nil)
}

func newRouter(rules []*DnsRule, upstreams []UpstreamConfig, defaultUpstream string, resolver GeositeResolver) (*Router, error) {
	upstreamMap, upstreamProxyTag := buildUpstreamMaps(upstreams)
	r := &Router{
		rules:            make([]*DnsRule, 0),
		matchers:         make([][]RuleMatcher, 0),
		compiled:         make([]compiledRule, 0),
		upstreamMap:      upstreamMap,
		upstreamProxyTag: upstreamProxyTag,
		defaultUpstream:  defaultUpstream,
		geositeResolver:  resolver,
	}

	// Load and compile rules.
	if err := r.UpdateRules(rules); err != nil {
		return nil, fmt.Errorf("new router: %w", err)
	}

	return r, nil
}

// DefaultUpstream returns the configured default upstream ID.
func (r *Router) DefaultUpstream() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultUpstream
}

// Route matches a DNS query against the rule list and returns the routing result.
// Rules are evaluated in order; the first matching rule determines the result.
// If no rule matches, the default upstream is used.
func (r *Router) Route(query *DnsQuery) *RouteResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Iterate rules in order.
	for i, rule := range r.rules {
		if i >= len(r.compiled) {
			continue
		}
		cr := r.compiled[i]
		action := rule.Action
		if action == "" {
			action = "route"
		}

		matched := false
		var expectedIPs []*IPRule

		if len(cr.ipRules) == 0 {
			matched = MatchAll(cr.queryMatchers, query)
		} else if action == "reject" {
			// Response-IP reject rules only match pre-query if the query itself
			// encodes an IP (PTR / literal); otherwise they are applied post-response.
			matched = MatchAll(cr.queryMatchers, query) && matchAnyIPQuery(cr.ipRules, query)
		} else if len(cr.queryMatchers) > 0 {
			// Query-time criteria matched; enforce response IP expectation post-response.
			if MatchAll(cr.queryMatchers, query) {
				matched = true
				expectedIPs = cr.ipRules
			}
		} else {
			// IP-only route/bypass rule: match PTR/IP-literal queries.
			matched = matchAnyIPQuery(cr.ipRules, query)
		}

		if matched {
			// Rule matched — determine upstream address.
			upstreamAddr := r.upstreamMap[rule.Upstream]

			dnsLogf("[dns router] rule %q matched query %s %s → upstream=%s action=%s",
				rule.ID, query.Name, rule.Upstream, upstreamAddr, action)

			policy := rule.Policy
			if policy == "" {
				policy = "single"
			}

			// Get proxy tag for the matched upstream.
			proxyTag := r.upstreamProxyTag[rule.Upstream]

			return &RouteResult{
				UpstreamID:   rule.Upstream,
				UpstreamAddr: upstreamAddr,
				Action:       action,
				RuleID:       rule.ID,
				Policy:       policy,
				ProxyTag:     proxyTag,
				ExpectedIPs:  expectedIPs,
			}
		}
	}

	// No rule matched — use default upstream.
	dnsLogf("[dns router] no rule matched for %s %d → using default upstream %s",
		query.Name, query.QType, r.defaultUpstream)

	defaultAddr := r.upstreamMap[r.defaultUpstream]
	defaultProxyTag := r.upstreamProxyTag[r.defaultUpstream]
	return &RouteResult{
		UpstreamID:   r.defaultUpstream,
		UpstreamAddr: defaultAddr,
		Action:       "route",
		RuleID:       "",
		Policy:       "single",
		ProxyTag:     defaultProxyTag,
	}
}

// MatchResponse evaluates post-response IP-based reject rules against a resolved
// DNS response. Returns a reject RouteResult if a rule matches, or nil otherwise.
func (r *Router) MatchResponse(query *DnsQuery, resp *DnsResponse) *RouteResult {
	if resp == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i, rule := range r.rules {
		if i >= len(r.compiled) {
			continue
		}
		cr := r.compiled[i]
		if len(cr.ipRules) == 0 || rule.Action != "reject" {
			continue
		}
		if MatchAll(cr.queryMatchers, query) && matchAnyIPResponse(cr.ipRules, resp) {
			return &RouteResult{
				UpstreamID: rule.Upstream,
				Action:     "reject",
				RuleID:     rule.ID,
				Policy:     rule.Policy,
			}
		}
	}
	return nil
}

// UpdateRules replaces the current rule set with new rules.
// It rebuilds all matchers for the new rules, expanding geosite tags using
// the configured resolver.
func (r *Router) UpdateRules(rules []*DnsRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newRules, newMatchers, newCompiled, err := compileRules(rules, r.geositeResolver)
	if err != nil {
		return err
	}

	r.rules = newRules
	r.matchers = newMatchers
	r.compiled = newCompiled

	log.Printf("[dns router] updated rules: %d rules loaded", len(rules))
	return nil
}

// ListRules returns a copy of all registered rules.
func (r *Router) ListRules() []*DnsRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rules := make([]*DnsRule, len(r.rules))
	for i, rule := range r.rules {
		cp := *rule
		rules[i] = &cp
	}
	return rules
}

// SetGeositeResolver sets the geosite tag resolver function.
func (r *Router) SetGeositeResolver(resolver GeositeResolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.geositeResolver = resolver
}

// Reload atomically reloads the router configuration from a DnsModuleConfig.
// All new rules, matchers, upstream maps, and the default upstream are built
// and validated into temporary locals before swapping router state, so a
// validation error leaves the existing router state completely intact.
func (r *Router) Reload(cfg *DnsModuleConfig) error {
	if cfg == nil {
		return fmt.Errorf("router reload: config is nil")
	}

	// Convert RuleConfigs to DnsRules.
	rules := make([]*DnsRule, len(cfg.Rules))
	for i, rc := range cfg.Rules {
		rules[i] = &DnsRule{
			ID:           rc.ID,
			Domain:       rc.Domain,
			DomainSuffix: rc.DomainSuffix,
			DomainRegex:  rc.DomainRegex,
			IP:           rc.IP,
			QueryType:    rc.QueryType,
			ClientIP:     rc.ClientIP,
			Upstream:     rc.Upstream,
			Action:       rc.Action,
			Policy:       rc.Policy,
		}
	}

	newUpstreamMap, newUpstreamProxyTag := buildUpstreamMaps(cfg.Upstreams)
	newDefaultUpstream := resolveDefaultUpstream(cfg)

	r.mu.Lock()
	defer r.mu.Unlock()

	newRules, newMatchers, newCompiled, err := compileRules(rules, r.geositeResolver)
	if err != nil {
		return err
	}

	r.rules = newRules
	r.matchers = newMatchers
	r.compiled = newCompiled
	r.upstreamMap = newUpstreamMap
	r.upstreamProxyTag = newUpstreamProxyTag
	r.defaultUpstream = newDefaultUpstream

	log.Printf("[dns router] reloaded: %d rules, %d upstreams, default=%s",
		len(newRules), len(newUpstreamMap), newDefaultUpstream)
	return nil
}
