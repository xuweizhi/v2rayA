package dns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

// ProxyAddrResolver is a function type that resolves a proxy tag to a
// SOCKS5/HTTP proxy address. This allows external code (e.g., v2rayA's
// process manager) to provide the proxy address lookup logic.
// Returns the proxy address (e.g., "127.0.0.1:1080") or empty string
// if the tag is unknown.
type ProxyAddrResolver func(proxyTag string) string

// SetProxyAddrResolver sets the proxy address resolver function.
// This is used by exchangeViaProxy to determine how to route DNS
// queries through a specific proxy channel.
func (m *UpstreamManager) SetProxyAddrResolver(resolver ProxyAddrResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyAddrResolver = resolver
}

// SetDispatcher sets the xray-core routing dispatcher for internal routing.
// When set, proxy-tagged upstreams use xray-core's internal routing
// instead of external SOCKS5 proxy (like xray-core's traditional DNS module).
func (m *UpstreamManager) SetDispatcher(d RouteDispatcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatcher = d
}

// Exchange sends a DNS query to the specified upstream server via UDP or TCP
// and returns the parsed response. This is a concrete implementation of the
// upstream query mechanism.
//
// The method:
//  1. Constructs a dns.Msg from the DnsQuery.
//  2. Adds EDNS0 Client Subnet (ECS) option if client IP is available.
//  3. Sets the DNSSEC OK (DO) bit for DNSSEC-aware upstreams.
//  4. Selects the transport protocol (udp/tcp) from the upstream config.
//  5. If xray-core dispatcher is available, ALWAYS uses it to prevent DNS loop:
//     - Without dispatcher: direct UDP/TCP query hits iptables --dport 53 redirect
//     → gets sent back to DNS module → infinite loop
//     - With dispatcher: xray outbound has sockopt.mark=0x80, iptables sees the
//     0x80 mark and skips redirect (TP_OUT/DNS_MARK chains RETURN on 0x80/0x80)
//  6. If the upstream has a non-empty, non-"direct" ProxyTag without dispatcher,
//     routes through external SOCKS5 proxy.
//  7. Otherwise, sends directly via UDP/TCP.
//  8. Sends the query and measures round-trip time.
//  9. If UDP response is truncated, automatically falls back to TCP.
//  10. On failure, retries once after a short delay.
//  11. Wraps the response into DnsResponse.
//
// Exchange tries only the explicitly configured fallback resolvers. It never
// consults an unrelated subscription or the default proxy DNS on failure.
func isProxiedUpstream(upstream *UpstreamInstance) bool {
	return upstream != nil && upstream.ProxyTag != "" && upstream.ProxyTag != "direct"
}

func (m *UpstreamManager) Exchange(upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	return m.ExchangeContext(context.Background(), upstream, query)
}

// ExchangeContext sends a DNS query to the specified upstream server (and any
// explicitly configured fallback resolvers) while honoring ctx cancellation
// and deadlines.
func (m *UpstreamManager) ExchangeContext(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if upstream == nil {
		return nil, fmt.Errorf("nil DNS upstream")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidates := []*UpstreamInstance{upstream}
	seen := map[string]bool{upstream.ID: true}
	for _, id := range upstream.Fallback {
		if seen[id] {
			continue
		}
		seen[id] = true
		candidate, ok := m.GetUpstream(id)
		if !ok {
			return nil, fmt.Errorf("DNS fallback %q not found", id)
		}
		candidates = append(candidates, candidate)
	}
	var lastErr error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(candidates) > 1 {
			// Keep node-DNS fallback within the listener's budget,
			// including resolvers that drop packets instead of refusing them.
			budget := 4 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				if rem := time.Until(deadline); rem > 0 && rem < budget {
					budget = rem
				}
			}
			cp := *candidate
			cp.attemptTimeout = budget / time.Duration(len(candidates))
			if cp.attemptTimeout <= 0 {
				cp.attemptTimeout = 100 * time.Millisecond
			}
			candidate = &cp
		}
		response, err := m.exchangeOne(ctx, candidate, query)
		if err == nil && response != nil && response.Rcode != dns.RcodeServerFailure && response.Rcode != dns.RcodeRefused {
			return response, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			err = fmt.Errorf("DNS upstream %q returned no usable response", candidate.ID)
		}
		lastErr = err
	}
	return nil, lastErr
}

func (m *UpstreamManager) exchangeOne(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	if upstream == nil {
		return nil, nil
	}

	if isEncryptedDNS(upstream.Protocol) {
		return m.exchangeEncryptedContext(ctx, upstream, query)
	}
	// Route through proxy if a proxy channel is specified — use dispatcher or SOCKS5.
	if isProxiedUpstream(upstream) {
		if m.getDispatcher() != nil {
			return m.exchangeViaDispatcher(ctx, upstream, query)
		}
		return m.exchangeViaProxy(ctx, upstream, query)
	}

	// Direct upstream: use direct UDP/TCP with SO_MARK=0x80 on the socket.
	// This prevents the iptables DNS redirect loop:
	//   - DNS_MARK chain checks 0x80/0x80 → RETURN (skip)
	//   - No 0x40 mark set, so ip rule fwmark 0x40/0xc0 doesn't match
	//   - Packet goes out normally, no redirect to :52353
	return m.exchangeDirect(ctx, upstream, query)
}

// UDP, TCP and direct encrypted DNS share the same EDNS options.
// Only add ECS for a public client on direct queries; proxied queries suppress
// client ECS to prevent leaking the client's real IP through the proxy.
func upstreamQuestion(query *DnsQuery) *dns.Msg {
	return buildUpstreamQuestion(query, false)
}

func buildUpstreamQuestion(query *DnsQuery, proxied bool) *dns.Msg {
	qclass := query.QClass
	if qclass == 0 {
		qclass = dns.ClassINET
	}
	msg := new(dns.Msg).SetQuestion(dns.Fqdn(query.Name), uint16(query.QType))
	msg.Question[0].Qclass = qclass
	msg.SetEdns0(4096, query.DNSSECOK)
	markOutgoing(msg)
	if !proxied {
		NewResponseBuilder().addECS(msg, upstreamClientSubnet(query.ClientIP))
	}
	return msg
}

// exchangeDirect sends a DNS query directly via UDP/TCP with SO_MARK=0x80 set
// on the socket. The 0x80 mark tells iptables to skip redirect (loop protection).
func (m *UpstreamManager) exchangeDirect(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	if upstream == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	protocol := strings.ToLower(strings.TrimSpace(upstream.Protocol))
	if protocol == "" {
		protocol = "udp"
	}
	switch protocol {
	case "udp", "udp4", "udp6", "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("dns upstream: unsupported direct protocol %q", upstream.Protocol)
	}

	// Build the DNS query message.
	msg := buildUpstreamQuestion(query, false)

	dnsLogf("[dns upstream] exchange direct: %s %s → %s (%s)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, protocol)

	// Create DNS client with SO_MARK=0x80 on all sockets.
	// The Control function sets the socket mark to 0x80, which iptables
	// DNS_MARK/TP_OUT chains check and RETURN (skip), preventing the loop.
	client := newMarkedDnsClient(protocol)
	reqCtx := ctx
	attempts := 2
	if upstream.attemptTimeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, upstream.attemptTimeout)
		defer cancel()
		attempts = 1
	}

	// Attempt the exchange with retry logic.
	var resp *dns.Msg
	var rtt time.Duration
	var err error

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			log.Printf("[dns upstream] retry %d: %s %s → %s", attempt+1,
				dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-reqCtx.Done():
				timer.Stop()
				return nil, fmt.Errorf("dns upstream: exchange failed: %w", reqCtx.Err())
			case <-timer.C:
			}
		}

		resp, rtt, err = client.ExchangeContext(reqCtx, msg, upstream.Addr)

		if err != nil {
			log.Printf("[dns upstream] attempt %d error: %s %s → %s: %v", attempt+1,
				dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, err)
			if reqCtx.Err() != nil {
				break
			}
			continue
		}

		// Handle UDP truncation → TCP fallback.
		if resp != nil && resp.Truncated && (protocol == "udp" || protocol == "udp4" || protocol == "udp6") {
			log.Printf("[dns upstream] truncated response, falling back to TCP: %s %s → %s",
				dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr)

			tcpClient := newMarkedDnsClient("tcp")
			resp, rtt, err = tcpClient.ExchangeContext(reqCtx, msg, upstream.Addr)
			if err != nil {
				log.Printf("[dns upstream] TCP fallback error (attempt %d): %s %s → %s: %v", attempt+1,
					dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, err)
				if reqCtx.Err() != nil {
					break
				}
				continue
			}
		}

		break
	}

	if err != nil {
		return nil, fmt.Errorf("dns upstream: exchange failed: %w", err)
	}

	if resp == nil {
		return nil, nil
	}

	if err := ValidateResponse(resp); err != nil {
		return nil, fmt.Errorf("dns upstream: invalid response: %w", err)
	}
	if err := validateQuestionMatchWithClass(resp, query.Name, query.QType, query.QClass); err != nil {
		return nil, fmt.Errorf("dns upstream: question mismatch: %w", err)
	}

	var ttl uint32
	if len(resp.Answer) > 0 {
		ttl = resp.Answer[0].Header().Ttl
		for _, rr := range resp.Answer[1:] {
			if rr.Header().Ttl < ttl {
				ttl = rr.Header().Ttl
			}
		}
	}

	dnsResp := &DnsResponse{
		Query:      *query,
		RawMsg:     resp,
		Rcode:      resp.Rcode,
		Answer:     resp.Answer,
		Authority:  resp.Ns,
		Additional: resp.Extra,
		TTL:        ttl,
		Upstream:   upstream.Addr,
		UpstreamID: upstream.ID,
		ProxyTag:   upstream.ProxyTag,
		RTT:        rtt,
		Cached:     false,
	}

	dnsLogf("[dns upstream] direct response: %s %s → %s (rcode=%d, rtt=%v, answers=%d)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr,
		resp.Rcode, rtt, len(resp.Answer))

	return dnsResp, nil
}

// exchangeViaProxy sends a DNS query through a proxy channel.
// It uses the configured ProxyAddrResolver to find the SOCKS5/HTTP proxy
// address for the given proxy tag, then sends the DNS query over TCP
// through that proxy.
func (m *UpstreamManager) exchangeViaProxy(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	if upstream == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	dnsLogf("[dns upstream] exchange via proxy: %s %s → %s (proxyTag=%s)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, upstream.ProxyTag)

	// Get the proxy address for the configured tag.
	proxyAddr := m.resolveProxyAddr(upstream.ProxyTag)
	if proxyAddr == "" {
		return nil, fmt.Errorf("DNS proxy %q is unavailable", upstream.ProxyTag)
	}

	// Build DNS query message (suppress client ECS on proxied queries).
	msg := buildUpstreamQuestion(query, true)

	timeout := 5 * time.Second
	attempts := 2
	if upstream.attemptTimeout > 0 {
		timeout = upstream.attemptTimeout
		attempts = 1
	}

	var resp *dns.Msg
	var rtt time.Duration
	var err error

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("dns upstream: proxy exchange failed: %w", ctx.Err())
			case <-timer.C:
			}
		}

		start := time.Now()
		resp, err = m.exchangeViaSocks5(ctx, proxyAddr, upstream.Addr, msg, timeout)
		rtt = time.Since(start)
		if err == nil {
			break
		}
		log.Printf("[dns upstream] proxy exchange error (attempt %d): %s %s → %s via %s: %v",
			attempt+1, dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, proxyAddr, err)
		if ctx.Err() != nil {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("dns upstream: proxy exchange failed: %w", err)
	}

	if resp == nil {
		log.Printf("[dns upstream] empty proxy response: %s %s → %s",
			dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr)
		return nil, nil
	}

	// Validate response.
	if err := ValidateResponse(resp); err != nil {
		return nil, fmt.Errorf("dns upstream: invalid proxy response: %w", err)
	}
	if err := validateQuestionMatchWithClass(resp, query.Name, query.QType, query.QClass); err != nil {
		return nil, fmt.Errorf("dns upstream: proxy question mismatch: %w", err)
	}

	// Calculate TTL.
	var ttl uint32
	if len(resp.Answer) > 0 {
		ttl = resp.Answer[0].Header().Ttl
		for _, rr := range resp.Answer[1:] {
			if rr.Header().Ttl < ttl {
				ttl = rr.Header().Ttl
			}
		}
	}

	dnsResp := &DnsResponse{
		Query:      *query,
		RawMsg:     resp,
		Rcode:      resp.Rcode,
		Answer:     resp.Answer,
		Authority:  resp.Ns,
		Additional: resp.Extra,
		TTL:        ttl,
		Upstream:   upstream.Addr,
		UpstreamID: upstream.ID,
		ProxyTag:   upstream.ProxyTag,
		RTT:        rtt,
		Cached:     false,
	}

	dnsLogf("[dns upstream] proxy response: %s %s → %s via %s (rcode=%d, rtt=%v, answers=%d)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, proxyAddr,
		resp.Rcode, rtt, len(resp.Answer))

	return dnsResp, nil
}

// exchangeViaSocks5 sends a DNS query through a SOCKS5 proxy using TCP.
// It honors ctx cancellation/deadline and the configured attempt timeout.
func (m *UpstreamManager) exchangeViaSocks5(ctx context.Context, proxyAddr, targetAddr string, msg *dns.Msg, timeout time.Duration) (*dns.Msg, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, markedDialer())
	if err != nil {
		return nil, err
	}
	conn, err := dialer.(proxy.ContextDialer).DialContext(reqCtx, "tcp", targetAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return exchangeDNSConnContext(reqCtx, conn, msg)
}

// dns.Conn reads the full TCP length prefix and payload, including fragmented
// SOCKS5/TCP packets. IDs still need checking when using Conn directly.
func exchangeDNSConn(conn net.Conn, msg *dns.Msg, deadline time.Time) (*dns.Msg, error) {
	ctx := context.Background()
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	return exchangeDNSConnContext(ctx, conn, msg)
}

func exchangeDNSConnContext(ctx context.Context, conn net.Conn, msg *dns.Msg) (*dns.Msg, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	// Close the connection if ctx expires or is cancelled so connections whose
	// SetDeadline is a no-op (such as xray cnc.Connection) cannot stall forever.
	stopWatch := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopWatch()

	client := &dns.Conn{Conn: conn}
	if err := client.WriteMsg(msg); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	response, err := client.ReadMsg()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if response.Id != msg.Id {
		return nil, dns.ErrId
	}
	return response, nil
}

func (m *UpstreamManager) getDispatcher() RouteDispatcher {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dispatcher
}

// resolveProxyAddr resolves a proxy tag to a SOCKS5 proxy address.
// It first checks the configured ProxyAddrResolver, then falls back
// to known defaults for well-known tags.
func (m *UpstreamManager) resolveProxyAddr(proxyTag string) string {
	m.mu.RLock()
	resolver := m.proxyAddrResolver
	m.mu.RUnlock()

	if resolver != nil {
		return resolver(proxyTag)
	}

	// Fallback: check well-known default proxy addresses.
	// These can be overridden by the resolver.
	switch proxyTag {
	case "proxy":
		return "127.0.0.1:1080" // Common SOCKS5 proxy port
	}

	return ""
}

// exchangeWithProtocol sends a DNS query using the specified protocol and
// returns the response, round-trip time, and any error.
func (m *UpstreamManager) exchangeWithProtocol(upstream *UpstreamInstance, msg *dns.Msg, protocol string) (*dns.Msg, time.Duration, error) {
	// Create or reuse the DNS client for the specified protocol.
	client := m.getClientForProtocol(upstream, protocol)
	if client == nil {
		return nil, 0, fmt.Errorf("failed to create DNS client for %s", protocol)
	}

	// Use a context with timeout for the exchange.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, rtt, err := client.ExchangeContext(ctx, msg, upstream.Addr)
	if err != nil {
		return resp, rtt, err
	}

	return resp, rtt, nil
}

// getClientForProtocol returns a DNS client configured for the given protocol.
// It reuses the upstream's client if the protocol matches; otherwise, it creates
// a new client for the requested protocol.
func (m *UpstreamManager) getClientForProtocol(upstream *UpstreamInstance, protocol string) *dns.Client {
	// If the upstream already has a client with the matching protocol, reuse it.
	if upstream.Client != nil && upstream.Client.Net == protocol {
		return upstream.Client
	}

	// Create a new client for the requested protocol.
	// IMPORTANT: the client MUST carry SO_MARK=0x80 (newMarkedDnsClient),
	// otherwise its queries are hijacked by the transparent-proxy redirect
	// rules back into this DNS module, forming an infinite loop that grows
	// memory until the host is exhausted.
	return newMarkedDnsClient(protocol)
}

// ExchangeRaw sends a raw *dns.Msg to the upstream and returns the raw response.
// This is used for cases where the caller needs low-level access to the DNS
// message without DnsQuery/DnsResponse wrapping.
func (m *UpstreamManager) ExchangeRaw(upstream *UpstreamInstance, msg *dns.Msg) (*dns.Msg, time.Duration, error) {
	if upstream == nil {
		return nil, 0, fmt.Errorf("dns upstream: nil upstream")
	}
	if msg == nil {
		return nil, 0, fmt.Errorf("dns upstream: nil message")
	}

	protocol := strings.ToLower(strings.TrimSpace(upstream.Protocol))
	if protocol == "" {
		protocol = "udp"
	}
	switch protocol {
	case "udp", "udp4", "udp6", "tcp", "tcp4", "tcp6", "tcp-tls":
	default:
		return nil, 0, fmt.Errorf("dns upstream: exchange raw unsupported protocol %q", upstream.Protocol)
	}

	msg = msg.Copy()
	markOutgoing(msg)
	client := m.getClientForProtocol(upstream, protocol)

	resp, rtt, err := client.Exchange(msg, upstream.Addr)
	if err != nil {
		return nil, rtt, fmt.Errorf("dns upstream: exchange raw: %w", err)
	}

	// Handle UDP truncation → TCP fallback.
	if resp != nil && resp.Truncated && protocol == "udp" {
		log.Printf("[dns upstream] truncated raw response, falling back to TCP: %s",
			upstream.Addr)

		tcpClient := m.getClientForProtocol(upstream, "tcp")
		resp, rtt, err = tcpClient.Exchange(msg, upstream.Addr)
		if err != nil {
			return nil, rtt, fmt.Errorf("dns upstream: exchange raw tcp fallback: %w", err)
		}
	}

	return resp, rtt, nil
}

// exchangeViaDispatcher sends a DNS query through xray-core's internal routing
// dispatcher. It uses the RouteDispatcher to create a connection through
// xray-core's routing engine (like xray-core's traditional DNS module does),
// then performs DNS-over-TCP on that connection.
//
// This replaces the external SOCKS5 proxy approach with v2raya-core's built-in
// routing logic, supporting any outbound type (VMESS, VLESS, Trojan, etc.).
func (m *UpstreamManager) exchangeViaDispatcher(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	dispatcher := m.getDispatcher()
	if upstream == nil || dispatcher == nil {
		return nil, fmt.Errorf("DNS dispatcher is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	dnsLogf("[dns upstream] exchange via xray routing: %s %s → %s (proxyTag=%s)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr, upstream.ProxyTag)

	// Build DNS query message (suppress client ECS on proxied queries).
	msg := buildUpstreamQuestion(query, isProxiedUpstream(upstream))

	timeout := 5 * time.Second
	if upstream.attemptTimeout > 0 {
		timeout = upstream.attemptTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := dispatcher.Dispatch(reqCtx, "tcp", upstream.Addr, upstream.ProxyTag)
	if err != nil {
		return nil, fmt.Errorf("DNS dispatch: %w", err)
	}
	defer conn.Close()
	dnsResp, err := exchangeDNSConnContext(reqCtx, conn, msg)
	if err != nil {
		return nil, fmt.Errorf("DNS dispatcher exchange: %w", err)
	}
	rtt := time.Since(start)

	// Validate response.
	if err := ValidateResponse(dnsResp); err != nil {
		return nil, fmt.Errorf("dns upstream: invalid dispatcher response: %w", err)
	}
	if err := validateQuestionMatchWithClass(dnsResp, query.Name, query.QType, query.QClass); err != nil {
		return nil, fmt.Errorf("dns upstream: dispatcher question mismatch: %w", err)
	}

	// Calculate TTL.
	var ttl uint32
	if len(dnsResp.Answer) > 0 {
		ttl = dnsResp.Answer[0].Header().Ttl
		for _, rr := range dnsResp.Answer[1:] {
			if rr.Header().Ttl < ttl {
				ttl = rr.Header().Ttl
			}
		}
	}

	resp := &DnsResponse{
		Query:      *query,
		RawMsg:     dnsResp,
		Rcode:      dnsResp.Rcode,
		Answer:     dnsResp.Answer,
		Authority:  dnsResp.Ns,
		Additional: dnsResp.Extra,
		TTL:        ttl,
		Upstream:   upstream.Addr,
		UpstreamID: upstream.ID,
		ProxyTag:   upstream.ProxyTag,
		RTT:        rtt,
		Cached:     false,
	}

	dnsLogf("[dns upstream] dispatcher response: %s %s → %s (rcode=%d, rtt=%v, answers=%d)",
		dns.Type(uint16(query.QType)).String(), query.Name, upstream.Addr,
		dnsResp.Rcode, rtt, len(dnsResp.Answer))

	return resp, nil
}
