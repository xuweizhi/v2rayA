package dns

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/miekg/dns"
)

// --- Helpers ---

func startMockUDPServer(t *testing.T, fn func(w dns.ResponseWriter, r *dns.Msg)) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	srv := &dns.Server{
		PacketConn:        pc,
		Handler:           dns.HandlerFunc(fn),
		NotifyStartedFunc: func() { close(ready) },
	}
	go func() { _ = srv.ActivateAndServe() }()
	<-ready
	t.Cleanup(func() {
		_ = srv.Shutdown()
		_ = pc.Close()
	})
	return pc.LocalAddr().String()
}

// ============================================================================
// K-H2: Unresolved geosite rules, prefixes, and pre-Start SetGeositeResolver
// ============================================================================

func TestUnresolvedGeositeNeverMatchesAll(t *testing.T) {
	// 1. Nil resolver with geosite: tag must produce NeverMatcher, not match-all.
	rule := &DnsRule{ID: "geo-nil", Domain: []string{"geosite:cn"}, Upstream: "cn"}
	matchers, err := BuildMatchers(rule, nil)
	if err != nil {
		t.Fatalf("BuildMatchers unexpected error: %v", err)
	}
	if len(matchers) == 0 {
		t.Fatal("expected NeverMatcher, got empty matchers (would match everything)")
	}
	if MatchAll(matchers, &DnsQuery{Name: "google.com", QType: TypeA}) {
		t.Fatal("unresolved geosite rule matched arbitrary domain")
	}

	// 2. Resolver returning empty for unknown tag + QueryType must still never match.
	emptyResolver := func(tag string) ([]string, []string) { return nil, nil }
	rule2 := &DnsRule{
		ID:        "geo-missing",
		Domain:    []string{"geosite:nonexistent"},
		QueryType: []QueryType{TypeA},
		Upstream:  "cn",
	}
	matchers2, err := BuildMatchers(rule2, emptyResolver)
	if err != nil {
		t.Fatalf("BuildMatchers unexpected error: %v", err)
	}
	if MatchAll(matchers2, &DnsQuery{Name: "google.com", QType: TypeA}) {
		t.Fatal("missing geosite tag matched despite domain constraint")
	}

	// 3. Explicit catch-all rule (no domain/IP/qtype/clientIP constraints) still matches all.
	catchAll := &DnsRule{ID: "catch-all", Upstream: "default"}
	matchers3, err := BuildMatchers(catchAll, emptyResolver)
	if err != nil {
		t.Fatalf("BuildMatchers catch-all error: %v", err)
	}
	if !MatchAll(matchers3, &DnsQuery{Name: "anything.example", QType: TypeA}) {
		t.Fatal("explicit catch-all rule should match")
	}
}

func TestDomainRulePrefixesAndGeositeExpansion(t *testing.T) {
	resolver := func(tag string) ([]string, []string) {
		if tag == "mixed" {
			return []string{
				"exact.example.com",
				"regexp:^api[0-9]+\\.service\\.test$",
				"keyword:tracker",
			}, []string{"suffix.test"}
		}
		return nil, nil
	}

	router, err := newRouter(
		[]*DnsRule{
			{
				ID:       "prefix-rule",
				Domain:   []string{"full:Exact.Direct.Test.", "domain:Sub.Direct.Test.", "keyword:AdBlock", "regexp:^cdn\\d+\\.assets\\.test$"},
				Upstream: "direct",
			},
			{
				ID:       "geo-rule",
				Domain:   []string{"geosite:mixed"},
				Upstream: "geo",
			},
		},
		[]UpstreamConfig{
			{ID: "direct", Addr: "1.1.1.1:53"},
			{ID: "geo", Addr: "2.2.2.2:53"},
			{ID: "fallback", Addr: "8.8.8.8:53"},
		},
		"fallback",
		resolver,
	)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}

	cases := []struct {
		name string
		want string
	}{
		{"exact.direct.test", "direct"},
		{"EXACT.DIRECT.TEST.", "direct"},
		{"other.exact.direct.test", "fallback"}, // full: must not match subdomains
		{"sub.direct.test", "direct"},
		{"deep.SUB.direct.test.", "direct"},
		{"notsub.direct.test", "fallback"},
		{"my-adblock-host.org", "direct"},
		{"CDN42.ASSETS.TEST.", "direct"},
		{"exact.example.com", "geo"},
		{"API99.SERVICE.TEST.", "geo"},
		{"sub.TRACKER.net", "geo"},
		{"www.suffix.test", "geo"},
		{"unmatched.org", "fallback"},
	}
	for _, tc := range cases {
		res := router.Route(&DnsQuery{Name: tc.name, QType: TypeA})
		if res.UpstreamID != tc.want {
			t.Errorf("Route(%q) = %q, want %q", tc.name, res.UpstreamID, tc.want)
		}
	}
}

func TestModuleSetGeositeResolverBeforeStart(t *testing.T) {
	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Upstreams = []UpstreamConfig{
		{ID: "cn", Addr: "127.0.0.1:53", ProxyTag: "direct"},
		{ID: "proxy", Addr: "127.0.0.2:53", ProxyTag: "proxy"},
	}
	cfg.Rules = []RuleConfig{
		{ID: "cn-geo", Domain: []string{"geosite:cn"}, Upstream: "cn", Action: "route"},
	}

	mod := NewDnsModule(cfg)
	mod.SetGeositeResolver(func(tag string) ([]string, []string) {
		if tag == "cn" {
			return nil, []string{"baidu.com"}
		}
		return nil, nil
	})
	if err := mod.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mod.Stop()

	// Immediately after Start(), geosite rules must already be expanded.
	router := mod.GetRouter()
	if got := router.Route(&DnsQuery{Name: "www.baidu.com", QType: TypeA}).UpstreamID; got != "cn" {
		t.Errorf("baidu.com routed to %q, want cn", got)
	}
	if got := router.Route(&DnsQuery{Name: "google.com", QType: TypeA}).UpstreamID; got != "proxy" {
		t.Errorf("google.com routed to %q, want proxy", got)
	}
}

// ============================================================================
// K-H3: Case-insensitive domain matching, normalized cache keys, wire question
// ============================================================================

type recordingHandler struct {
	mu      sync.Mutex
	queries []*DnsQuery
}

func (h *recordingHandler) HandleQuery(_ context.Context, q *DnsQuery) (*DnsResponse, error) {
	cp := *q
	h.mu.Lock()
	h.queries = append(h.queries, &cp)
	h.mu.Unlock()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(q.Name), uint16(q.QType))
	if q.QClass != 0 {
		m.Question[0].Qclass = q.QClass
	}
	m.Response = true
	m.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: dns.Fqdn(q.Name), Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.IPv4(192, 0, 2, 10),
		},
	}
	return &DnsResponse{RawMsg: m, Rcode: dns.RcodeSuccess, Answer: m.Answer, TTL: 60}, nil
}

func TestCaseInsensitiveNormalizationAndWireQuestionPreserved(t *testing.T) {
	up := &UpstreamInstance{ID: "u1", Addr: "8.8.8.8:53", ProxyTag: "direct"}
	k1 := buildCacheKey(&DnsQuery{Name: "ExAmPlE.CoM.", QType: TypeA}, up)
	k2 := buildCacheKey(&DnsQuery{Name: "example.com", QType: TypeA}, up)
	k3 := buildCacheKey(&DnsQuery{Name: "EXAMPLE.COM.", QType: TypeA}, up)
	if k1 != k2 || k2 != k3 || k1.Name != "example.com" {
		t.Fatalf("cache keys not normalized: %+v / %+v / %+v", k1, k2, k3)
	}

	h := &recordingHandler{}
	l := NewDnsListener(&DnsListenerConfig{ListenAddr: "127.0.0.1:0", Timeout: 2}, h)
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	defer l.Stop()

	addr := l.servers[0].udp.PacketConn.LocalAddr().String()
	req := new(dns.Msg).SetQuestion("MiXeD.ExAmPlE.CoM.", dns.TypeA)
	resp, _, err := (&dns.Client{Net: "udp"}).Exchange(req, addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Question) != 1 || resp.Question[0].Name != "MiXeD.ExAmPlE.CoM." {
		t.Fatalf("wire response question did not preserve original case: %+v", resp.Question)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queries) != 1 || h.queries[0].Name != "mixed.example.com" {
		t.Fatalf("handler did not receive normalized name: %+v", h.queries)
	}
}

// ============================================================================
// K-H5: handleBypass, DoT bootstrap order & default port, IPv6 bootstrap, DoQ
// ============================================================================

func TestHandleBypassTrulyBypassesProxy(t *testing.T) {
	var directHits atomic.Int32
	directAddr := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		directHits.Add(1)
		_ = w.WriteMsg(encryptedTestReply(r))
	})

	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Cache.Enabled = false
	// Even when the bypass rule points at a proxy-tagged upstream, handleBypass must bypass the proxy.
	cfg.Upstreams = []UpstreamConfig{
		{ID: "proxied-up", Addr: directAddr, Protocol: "udp", ProxyTag: "proxy"},
	}
	cfg.Rules = []RuleConfig{
		{ID: "bypass-rule", Domain: []string{"bypass.example"}, Upstream: "proxied-up", Action: "bypass"},
	}

	var proxyCalls atomic.Int32
	mod := NewDnsModule(cfg)
	mod.SetDispatcher(reviewDispatcher(func(ctx context.Context, network, addr, tag string) (net.Conn, error) {
		proxyCalls.Add(1)
		return nil, errors.New("proxy should not be called for bypass")
	}))
	if err := mod.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mod.Stop()

	resp, err := mod.handler.HandleQuery(context.Background(), &DnsQuery{Name: "bypass.example", QType: TypeA})
	if err != nil || resp == nil || len(resp.Answer) != 1 {
		t.Fatalf("bypass query failed: %v %+v", err, resp)
	}
	if proxyCalls.Load() != 0 {
		t.Fatalf("bypass query invoked proxy dispatcher %d times", proxyCalls.Load())
	}
	if directHits.Load() != 1 {
		t.Fatalf("expected 1 direct hit, got %d", directHits.Load())
	}
}

func TestPrepareNormalizesDotPortBeforeBootstrapAndSkipsProxiedBootstrap(t *testing.T) {
	var proxiedQueries atomic.Int32
	bootstrapSrv := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		resp := new(dns.Msg).SetReply(r)
		q := r.Question[0]
		name := normalizeDomain(q.Name)
		switch name {
		case "dot.direct.test":
			if q.Qtype == dns.TypeA {
				resp.Answer = []dns.RR{&dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   net.IPv4(127, 0, 0, 9),
				}}
			}
		case "ipv6only.direct.test":
			if q.Qtype == dns.TypeAAAA {
				resp.Answer = []dns.RR{&dns.AAAA{
					Hdr:  dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
					AAAA: net.ParseIP("2001:db8::53"),
				}}
			}
		case "proxied.doh.test":
			proxiedQueries.Add(1)
			if q.Qtype == dns.TypeA {
				resp.Answer = []dns.RR{&dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
					A:   net.IPv4(198, 51, 100, 99),
				}}
			}
		}
		_ = w.WriteMsg(resp)
	})

	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Bootstrap = []string{"dot.direct.test", "ipv6only.direct.test", "proxied.doh.test"}
	cfg.BootstrapDns = []string{bootstrapSrv}
	cfg.Upstreams = []UpstreamConfig{
		{ID: "dot", Addr: "dot.direct.test", Protocol: "dot", ProxyTag: "direct"},
		{ID: "v6", Addr: "ipv6only.direct.test", Protocol: "udp", ProxyTag: "direct"},
		{ID: "proxied", Addr: "https://proxied.doh.test/dns-query", Protocol: "https", ProxyTag: "proxy"},
	}

	mod := NewDnsModule(cfg)
	if err := mod.prepare(); err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if mod.cancel != nil {
		mod.cancel()
	}

	if cfg.Upstreams[0].Addr != "dot.direct.test:853" || cfg.Upstreams[0].DialAddress != "127.0.0.9:853" {
		t.Errorf("dot upstream not normalized/bootstrapped properly: %+v", cfg.Upstreams[0])
	}
	if cfg.Upstreams[1].Addr != "[2001:db8::53]:53" {
		t.Errorf("ipv6-only bootstrap upstream addr = %q, want [2001:db8::53]:53", cfg.Upstreams[1].Addr)
	}
	if cfg.Upstreams[2].DialAddress != "" || cfg.Upstreams[2].Addr != "https://proxied.doh.test/dns-query" {
		t.Errorf("proxied upstream should not be overwritten with local bootstrap IP: %+v", cfg.Upstreams[2])
	}
	if proxiedQueries.Load() != 0 {
		t.Errorf("proxied upstream hostname was queried %d times via local bootstrap DNS", proxiedQueries.Load())
	}
}

func TestEncryptedDNSDoQAndUnsupportedProtocol(t *testing.T) {
	certSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer certSrv.Close()

	srvTLS := certSrv.TLS.Clone()
	srvTLS.MinVersion = tls.VersionTLS13
	srvTLS.NextProtos = []string{"doq"}

	ql, err := quic.ListenAddr("127.0.0.1:0", srvTLS, nil)
	if err != nil {
		t.Fatalf("quic.ListenAddr: %v", err)
	}
	defer ql.Close()

	errCh := make(chan error, 1)
	go func() {
		qconn, err := ql.Accept(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		// The client closes the connection after reading the response. Closing here
		// could discard data still queued after stream.Close().
		stream, err := qconn.AcceptStream(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		var lenBuf [2]byte
		if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
			errCh <- err
			return
		}
		qLen := binary.BigEndian.Uint16(lenBuf[:])
		qBuf := make([]byte, qLen)
		if _, err := io.ReadFull(stream, qBuf); err != nil {
			errCh <- err
			return
		}
		q := new(dns.Msg)
		if err := q.Unpack(qBuf); err != nil {
			errCh <- err
			return
		}
		if q.Id != 0 {
			errCh <- errors.New("RFC 9250 requires DoQ query ID == 0")
			return
		}
		if !carriesOwnToken(q) {
			errCh <- errors.New("DoQ query missing loop marker")
			return
		}
		reply := encryptedTestReply(q)
		reply.Id = 0
		wire, err := reply.Pack()
		if err != nil {
			errCh <- err
			return
		}
		frame := make([]byte, 2+len(wire))
		binary.BigEndian.PutUint16(frame[:2], uint16(len(wire)))
		copy(frame[2:], wire)
		_, err = stream.Write(frame)
		_ = stream.Close()
		errCh <- err
	}()

	roots := x509.NewCertPool()
	roots.AddCert(certSrv.Certificate())
	_, port, _ := net.SplitHostPort(ql.Addr().String())
	up := &UpstreamInstance{
		ID:          "doq-test",
		Addr:        net.JoinHostPort("example.com", port),
		DialAddress: ql.Addr().String(),
		Protocol:    "quic",
		ProxyTag:    "direct",
	}
	mgr := NewUpstreamManager(nil)
	res, err := mgr.exchangeEncryptedWithTLSContext(
		context.Background(),
		up,
		&DnsQuery{Name: "doq.example", QType: TypeA},
		&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13},
	)
	if err != nil || res == nil || len(res.Answer) != 1 {
		t.Fatalf("DoQ exchange failed: %v %+v", err, res)
	}
	if srvErr := <-errCh; srvErr != nil {
		t.Fatalf("DoQ server check failed: %v", srvErr)
	}

	// Proxied DoQ must return a clear descriptive error instead of dialing invalid network "quic".
	proxiedDoQ := &UpstreamInstance{ID: "doq-proxy", Addr: "dns.adguard.com:853", Protocol: "quic", ProxyTag: "proxy"}
	_, err = mgr.Exchange(proxiedDoQ, &DnsQuery{Name: "doq.example", QType: TypeA})
	if err == nil || !strings.Contains(err.Error(), "DoQ") {
		t.Fatalf("expected descriptive DoQ proxy error, got %v", err)
	}

	// Unknown direct protocol must return a clear error.
	bogus := &UpstreamInstance{ID: "bogus", Addr: "127.0.0.1:53", Protocol: "bogus", ProxyTag: "direct"}
	_, err = mgr.Exchange(bogus, &DnsQuery{Name: "test.example", QType: TypeA})
	if err == nil || !strings.Contains(err.Error(), "unsupported direct protocol") {
		t.Fatalf("expected unsupported protocol error, got %v", err)
	}
}

// ============================================================================
// UX-H6: ECS privacy leak on proxied DNS + CGNAT / non-public IP filtering
// ============================================================================

func TestECSFilteredForCGNATAndSuppressedOnProxiedUpstreams(t *testing.T) {
	// CGNAT, FakeIP/benchmark, multicast, reserved, link-local, etc. must never produce ECS.
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"::ffff:100.64.1.2", false},
		{"198.18.0.1", false},
		{"198.19.255.254", false},
		{"0.1.2.3", false},
		{"192.0.0.1", false},
		{"240.0.0.1", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"100::1", false},
		{"fec0::1", false},
		{"64:ff9b::1.2.3.4", false},
		{"1.1.1.1", true},
		{"8.8.8.8", true},
		{"2606:4700:4700::1111", true},
	} {
		got := upstreamClientSubnet(net.ParseIP(tc.ip)).IsValid()
		if got != tc.want {
			t.Errorf("upstreamClientSubnet(%q).IsValid() = %v, want %v", tc.ip, got, tc.want)
		}
	}

	// Direct query attaches ECS; proxied query MUST NOT attach ECS.
	q := &DnsQuery{Name: "privacy.example", QType: TypeA, ClientIP: net.ParseIP("1.1.1.1")}
	hasECS := func(m *dns.Msg) bool {
		opt := m.IsEdns0()
		if opt == nil {
			return false
		}
		for _, o := range opt.Option {
			if _, ok := o.(*dns.EDNS0_SUBNET); ok {
				return true
			}
		}
		return false
	}
	if !hasECS(buildUpstreamQuestion(q, false)) {
		t.Fatal("direct query with public ClientIP should include ECS")
	}
	if hasECS(buildUpstreamQuestion(q, true)) {
		t.Fatal("proxied query must NOT include client ECS")
	}

	// End-to-end dispatcher check: verify wire message sent over proxy has no ECS.
	var sawECS atomic.Bool
	mgr := NewUpstreamManager(nil)
	mgr.SetDispatcher(reviewDispatcher(func(ctx context.Context, network, addr, tag string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			dc := &dns.Conn{Conn: server}
			req, err := dc.ReadMsg()
			if err != nil {
				return
			}
			if hasECS(req) {
				sawECS.Store(true)
			}
			_ = dc.WriteMsg(encryptedTestReply(req))
		}()
		return client, nil
	}))
	_, err := mgr.Exchange(&UpstreamInstance{ID: "p", Addr: "8.8.8.8:53", ProxyTag: "proxy"}, q)
	if err != nil {
		t.Fatalf("proxied exchange failed: %v", err)
	}
	if sawECS.Load() {
		t.Fatal("ECS leaked on wire to proxied upstream")
	}

	// Cache key must omit ClientSubnet for proxied upstream.
	proxiedKey := buildCacheKey(q, &UpstreamInstance{Addr: "8.8.8.8:53", ProxyTag: "proxy"})
	if proxiedKey.ClientSubnet.IsValid() {
		t.Errorf("proxied cache key should not partition by ClientSubnet: %+v", proxiedKey)
	}
}

// ============================================================================
// K-M1: Metrics concurrency races, unified key, ctx cancellation, SOCKS5 timeout
// ============================================================================

func TestMetricsSnapshotAndDiagnosticsRaceFree(t *testing.T) {
	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Upstreams = []UpstreamConfig{{ID: "u1", Addr: "127.0.0.1:53"}}
	mod := NewDnsModule(cfg)
	if err := mod.Start(); err != nil {
		t.Fatal(err)
	}
	defer mod.Stop()

	metrics := mod.GetMetrics()
	diag := mod.GetDiagnostics()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := &DnsQuery{Name: "race.test", QType: TypeA}
			resp := &DnsResponse{Upstream: "127.0.0.1:53", UpstreamID: "u1"}
			for {
				select {
				case <-stop:
					return
				default:
					metrics.RecordQuery(q, "u1")
					metrics.RecordResponse(resp, 5*time.Millisecond)
					metrics.RecordError("u1", errors.New("err"))
				}
			}
		}()
	}

	// Resetter
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				metrics.Reset()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Readers inspecting Snapshot, Stats, and GetDebugInfo
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := metrics.Snapshot()
					for _, um := range snap.QueriesByUpstream {
						_ = um.Queries + um.Successes + um.Errors
						_ = um.AvgRTT
						_ = um.LastQuery
					}
					_ = mod.Stats()
					_ = diag.GetDebugInfo()
				}
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestUnifiedUpstreamMetricsKey(t *testing.T) {
	srvAddr := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		_ = w.WriteMsg(encryptedTestReply(r))
	})

	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Cache.Enabled = false
	cfg.Upstreams = []UpstreamConfig{{ID: "primary-dns", Addr: srvAddr, Protocol: "udp", ProxyTag: "direct"}}
	mod := NewDnsModule(cfg)
	if err := mod.Start(); err != nil {
		t.Fatal(err)
	}
	defer mod.Stop()

	_, err := mod.handler.HandleQuery(context.Background(), &DnsQuery{Name: "metrics.test", QType: TypeA})
	if err != nil {
		t.Fatalf("HandleQuery: %v", err)
	}

	snap := mod.GetMetrics().Snapshot()
	if len(snap.QueriesByUpstream) != 1 {
		t.Fatalf("expected 1 unified upstream metrics entry, got %d: %+v", len(snap.QueriesByUpstream), snap.QueriesByUpstream)
	}
	um, ok := snap.QueriesByUpstream["primary-dns"]
	if !ok {
		t.Fatalf("expected metrics keyed by upstream ID 'primary-dns', got %+v", snap.QueriesByUpstream)
	}
	if um.Queries != 1 || um.Successes != 1 {
		t.Errorf("expected Queries=1 Successes=1, got %+v", um)
	}
}

func TestExchangeContextCancellationAndSocks5AttemptTimeout(t *testing.T) {
	// 1. Stalled dispatcher connection aborted promptly by context timeout.
	mgr := NewUpstreamManager(nil)
	mgr.SetDispatcher(reviewDispatcher(func(ctx context.Context, network, addr, tag string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := mgr.ExchangeContext(ctx, &UpstreamInstance{ID: "stalled", Addr: "8.8.8.8:53", ProxyTag: "proxy"}, &DnsQuery{Name: "hang.test", QType: TypeA})
	if err == nil {
		t.Fatal("expected error from cancelled/timed-out dispatcher exchange")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dispatcher exchange took %v, did not respect ctx timeout", elapsed)
	}

	// 2. SOCKS5 exchange honors attemptTimeout instead of hardcoded 5s.
	stallLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stallLn.Close()
	go func() {
		for {
			c, err := stallLn.Accept()
			if err != nil {
				return
			}
			// Accept connection and stall forever without sending SOCKS5 reply.
			t.Cleanup(func() { _ = c.Close() })
		}
	}()

	socksMgr := NewUpstreamManager(nil)
	socksMgr.SetProxyAddrResolver(func(string) string { return stallLn.Addr().String() })
	up := &UpstreamInstance{
		ID:             "socks-stall",
		Addr:           "8.8.8.8:53",
		ProxyTag:       "proxy",
		attemptTimeout: 200 * time.Millisecond,
	}
	start = time.Now()
	_, err = socksMgr.ExchangeContext(context.Background(), up, &DnsQuery{Name: "socks.test", QType: TypeA})
	if err == nil {
		t.Fatal("expected timeout error from stalled SOCKS5 proxy")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("SOCKS5 exchange took %v, ignored attemptTimeout=200ms", elapsed)
	}
}

// ============================================================================
// K-M2: IPRule vs ClientIPRule, prefetch TOCTOU & negative storm, QR=1, QClass, DO
// ============================================================================

func TestIPRuleDistinctFromClientIPRuleAndPostResponseFiltering(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	ipRule := &IPRule{CIDR: cidr}

	// IPRule must NOT match ClientIP (that is ClientIPRule's job).
	if ipRule.Match(&DnsQuery{Name: "example.com", QType: TypeA, ClientIP: net.ParseIP("10.0.0.5")}) {
		t.Fatal("IPRule silently matched ClientIP")
	}
	// IPRule matches PTR query for 10.0.0.5.
	if !ipRule.Match(&DnsQuery{Name: "5.0.0.10.in-addr.arpa.", QType: TypePTR}) {
		t.Fatal("IPRule should match PTR query in its CIDR")
	}
	// IPRule.MatchResponse matches A record in answer.
	respIn := &DnsResponse{Answer: []dns.RR{&dns.A{A: net.IPv4(10, 1, 2, 3)}}}
	respOut := &DnsResponse{Answer: []dns.RR{&dns.A{A: net.IPv4(93, 184, 216, 34)}}}
	if !ipRule.MatchResponse(respIn) || ipRule.MatchResponse(respOut) {
		t.Fatal("IPRule.MatchResponse failed")
	}

	// End-to-end post-response reject on private IP (DNS rebinding protection).
	rebindSrv := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg).SetReply(r)
		ip := net.IPv4(10, 0, 0, 9)
		if normalizeDomain(r.Question[0].Name) == "safe.example" {
			ip = net.IPv4(93, 184, 216, 34)
		}
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   ip,
		}}
		_ = w.WriteMsg(m)
	})

	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Cache.Enabled = false
	cfg.Upstreams = []UpstreamConfig{{ID: "u1", Addr: rebindSrv, Protocol: "udp"}}
	cfg.Rules = []RuleConfig{
		{ID: "no-rebind", IP: []string{"10.0.0.0/8"}, Action: "reject"},
	}
	mod := NewDnsModule(cfg)
	if err := mod.Start(); err != nil {
		t.Fatal(err)
	}
	defer mod.Stop()

	res1, err := mod.handler.HandleQuery(context.Background(), &DnsQuery{Name: "evil.example", QType: TypeA})
	if err != nil || res1.Rcode != dns.RcodeRefused {
		t.Fatalf("expected REFUSED for response IP 10.0.0.9, got err=%v rcode=%v", err, res1)
	}
	res2, err := mod.handler.HandleQuery(context.Background(), &DnsQuery{Name: "safe.example", QType: TypeA})
	if err != nil || res2.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected NOERROR for safe IP 93.184.216.34, got err=%v rcode=%v", err, res2)
	}
}

func TestTryStartPrefetchAtomicAndNoNegativeStorm(t *testing.T) {
	cfg := newTestCacheConfig()
	cfg.NegativeCache = false
	cfg.Prefetch = true
	cfg.PrefetchThreshold = 30
	cache := NewDnsCache(cfg)

	key := newTestCacheKey("storm.example", TypeA)
	cache.Set(key, newTestDNSResponse("storm.example", TypeA, dns.RcodeSuccess, 60), 60)
	// Force entry into prefetch window.
	cache.mu.Lock()
	cache.entries[key].Value.(*CacheEntry).ExpiresAt = time.Now().Add(5 * time.Second)
	cache.mu.Unlock()

	// Atomic TOCTOU check: 50 concurrent callers, exactly 1 must win.
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cache.TryStartPrefetch(key) {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("TryStartPrefetch allowed %d concurrent prefetches, want 1", winners.Load())
	}

	// Negative prefetch storm check: SERVFAIL with NegativeCache=false must record failure & back off.
	servfailSrv := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg).SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(m)
	})
	modCfg := DefaultDnsModuleConfig()
	modCfg.Listener.ListenAddr = "127.0.0.1:0"
	modCfg.Cache = *cfg
	modCfg.Upstreams = []UpstreamConfig{{ID: "sf", Addr: servfailSrv, Protocol: "udp"}}
	mod := NewDnsModule(modCfg)
	if err := mod.Start(); err != nil {
		t.Fatal(err)
	}
	defer mod.Stop()

	up, _ := mod.GetUpstreamManager().GetUpstream("sf")
	q := &DnsQuery{Name: "storm.example", QType: TypeA}
	sfKey := buildCacheKey(q, up)
	mod.GetCache().Set(sfKey, newTestDNSResponse("storm.example", TypeA, dns.RcodeSuccess, 60), 60)
	mod.GetCache().mu.Lock()
	mod.GetCache().entries[sfKey].Value.(*CacheEntry).ExpiresAt = time.Now().Add(5 * time.Second)
	mod.GetCache().mu.Unlock()

	if !mod.GetCache().TryStartPrefetch(sfKey) {
		t.Fatal("expected initial prefetch to start")
	}
	mod.handler.prefetchAsync(context.Background(), up, mod.GetUpstreamManager(), q, sfKey)
	if mod.GetCache().TryStartPrefetch(sfKey) {
		t.Fatal("prefetch immediately re-triggered after SERVFAIL (storm!)")
	}

	// NXDOMAIN with NegativeCache=false must remove the stale entry instead of looping.
	nxSrv := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg).SetReply(r)
		m.Rcode = dns.RcodeNameError
		_ = w.WriteMsg(m)
	})
	nxUp := &UpstreamInstance{ID: "nx", Addr: nxSrv, Protocol: "udp", ProxyTag: "direct"}
	nxKey := buildCacheKey(q, nxUp)
	mod.GetCache().Set(nxKey, newTestDNSResponse("storm.example", TypeA, dns.RcodeSuccess, 60), 60)
	mod.GetCache().mu.Lock()
	mod.GetCache().entries[nxKey].Value.(*CacheEntry).ExpiresAt = time.Now().Add(5 * time.Second)
	mod.GetCache().mu.Unlock()
	if !mod.GetCache().TryStartPrefetch(nxKey) {
		t.Fatal("expected NXDOMAIN prefetch to start")
	}
	mod.handler.prefetchAsync(context.Background(), nxUp, mod.GetUpstreamManager(), q, nxKey)
	if _, exists := mod.GetCache().Get(nxKey); exists {
		t.Fatal("stale positive entry should have been invalidated on NXDOMAIN when NegativeCache=false")
	}
}

func TestListenerRejectsResponsePacketsAndPreservesQClassAndDOBit(t *testing.T) {
	h := &recordingHandler{}
	l := NewDnsListener(&DnsListenerConfig{ListenAddr: "127.0.0.1:0", Timeout: 2}, h)
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	defer l.Stop()
	addr := l.servers[0].udp.PacketConn.LocalAddr().String()

	// 1. Incoming packet with QR=1 (response) must be silently ignored.
	respPkt := new(dns.Msg).SetQuestion("spoof.example.", dns.TypeA)
	respPkt.Response = true
	wire, _ := respPkt.Pack()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write(wire)
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)
	h.mu.Lock()
	count := len(h.queries)
	h.mu.Unlock()
	if count != 0 {
		t.Fatalf("response packet (QR=1) invoked handler %d times", count)
	}

	// 2. Non-IN QClass (ClassCHAOS) and DO=0 vs DO=1 propagation.
	client := &dns.Client{Net: "udp"}
	chaosQ := new(dns.Msg)
	chaosQ.SetQuestion("version.bind.", dns.TypeTXT)
	chaosQ.Question[0].Qclass = dns.ClassCHAOS
	if _, _, err := client.Exchange(chaosQ, addr); err != nil {
		t.Fatalf("CHAOS query failed: %v", err)
	}

	doQ := new(dns.Msg).SetQuestion("dnssec.example.", dns.TypeA)
	doQ.SetEdns0(1232, true)
	if _, _, err := client.Exchange(doQ, addr); err != nil {
		t.Fatalf("DO=1 query failed: %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(h.queries))
	}
	if h.queries[0].QClass != dns.ClassCHAOS || h.queries[0].DNSSECOK {
		t.Errorf("first query QClass=%d DNSSECOK=%v, want CHAOS/false", h.queries[0].QClass, h.queries[0].DNSSECOK)
	}
	if h.queries[1].QClass != dns.ClassINET || !h.queries[1].DNSSECOK {
		t.Errorf("second query QClass=%d DNSSECOK=%v, want IN/true", h.queries[1].QClass, h.queries[1].DNSSECOK)
	}

	// Verify upstreamQuestion only sets DO=1 when DNSSECOK is true, and preserves QClass.
	mNoDO := upstreamQuestion(&DnsQuery{Name: "version.bind", QType: TypeTXT, QClass: dns.ClassCHAOS, DNSSECOK: false})
	if mNoDO.Question[0].Qclass != dns.ClassCHAOS {
		t.Errorf("upstreamQuestion lost QClass: got %d", mNoDO.Question[0].Qclass)
	}
	if mNoDO.IsEdns0().Do() {
		t.Error("upstreamQuestion set DO=1 when client did not request DNSSEC")
	}
	mWithDO := upstreamQuestion(&DnsQuery{Name: "dnssec.example", QType: TypeA, DNSSECOK: true})
	if !mWithDO.IsEdns0().Do() {
		t.Error("upstreamQuestion did not set DO=1 when client requested DNSSEC")
	}

	// CacheKey must partition by QClass and DNSSECOK.
	kIN := buildCacheKey(&DnsQuery{Name: "version.bind", QType: TypeTXT, QClass: dns.ClassINET}, nil)
	kCH := buildCacheKey(&DnsQuery{Name: "version.bind", QType: TypeTXT, QClass: dns.ClassCHAOS}, nil)
	kDO := buildCacheKey(&DnsQuery{Name: "version.bind", QType: TypeTXT, QClass: dns.ClassINET, DNSSECOK: true}, nil)
	if kIN == kCH || kIN == kDO {
		t.Fatalf("CacheKey failed to distinguish QClass or DNSSECOK: %+v / %+v / %+v", kIN, kCH, kDO)
	}
}

// ============================================================================
// L-8: DefaultUpstream, atomic Router.Reload, BuildUpstreamKey, nil fallback
// ============================================================================

func TestDefaultUpstreamAndAtomicRouterReload(t *testing.T) {
	// JSON unmarshal supports default_upstream.
	var parsed DnsModuleConfig
	if err := json.Unmarshal([]byte(`{"default_upstream":"u-first"}`), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.DefaultUpstream != "u-first" {
		t.Fatalf("DefaultUpstream = %q, want u-first", parsed.DefaultUpstream)
	}

	cfg := DefaultDnsModuleConfig()
	cfg.Upstreams = []UpstreamConfig{
		{ID: "u-first", Addr: "1.1.1.1:53"},
		{ID: "u-second", Addr: "8.8.8.8:53"},
	}
	cfg.DefaultUpstream = "u-first"
	cfg.Rules = []RuleConfig{
		{ID: "r1", Domain: []string{"one.example"}, Upstream: "u-second"},
	}

	router, err := NewRouter(nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := router.Route(&DnsQuery{Name: "unmatched.example", QType: TypeA}).UpstreamID; got != "u-first" {
		t.Fatalf("default upstream = %q, want explicit u-first", got)
	}

	// Reload with invalid rule must fail atomically without corrupting upstreamMap or defaultUpstream.
	badCfg := DefaultDnsModuleConfig()
	badCfg.Upstreams = []UpstreamConfig{{ID: "u-new", Addr: "9.9.9.9:53"}}
	badCfg.DefaultUpstream = "u-new"
	badCfg.Rules = []RuleConfig{{ID: "bad", DomainRegex: []string{"[unclosed"}, Upstream: "u-new"}}
	if err := router.Reload(badCfg); err == nil {
		t.Fatal("expected Reload with invalid regex to fail")
	}

	if got := router.DefaultUpstream(); got != "u-first" {
		t.Errorf("failed Reload mutated defaultUpstream to %q", got)
	}
	res := router.Route(&DnsQuery{Name: "one.example", QType: TypeA})
	if res.UpstreamID != "u-second" || res.UpstreamAddr != "8.8.8.8:53" {
		t.Errorf("failed Reload mutated existing route/upstreamMap: %+v", res)
	}
}

func TestUpstreamKeySeparatesProtocolsAndFallbacks(t *testing.T) {
	mgr := NewUpstreamManager([]UpstreamConfig{
		{ID: "udp-plain", Addr: "8.8.8.8:53", Protocol: "udp", ProxyTag: "direct"},
		{ID: "tcp-plain", Addr: "8.8.8.8:53", Protocol: "tcp", ProxyTag: "direct"},
		{ID: "udp-fb1", Addr: "8.8.8.8:53", Protocol: "udp", ProxyTag: "direct", Fallback: []string{"b1"}},
		{ID: "udp-fb2", Addr: "8.8.8.8:53", Protocol: "udp", ProxyTag: "direct", Fallback: []string{"b2"}},
	})
	if got := len(mgr.ListUpstreams()); got != 4 {
		t.Fatalf("expected 4 distinct upstreams without key collision, got %d", got)
	}

	tcpInst := mgr.GetOrCreateUpstream(UpstreamConfig{ID: "tcp-plain", Addr: "8.8.8.8:53", Protocol: "tcp", ProxyTag: "direct"})
	udpInst := mgr.GetOrCreateUpstream(UpstreamConfig{ID: "udp-plain", Addr: "8.8.8.8:53", Protocol: "udp", ProxyTag: "direct"})
	if tcpInst == udpInst || tcpInst.Protocol != "tcp" || udpInst.Protocol != "udp" {
		t.Fatalf("UDP and TCP upstreams collided: %+v vs %+v", udpInst, tcpInst)
	}
}

func TestHandlerFallbackPrefersDefaultAndSkipsNil(t *testing.T) {
	srvAddr := startMockUDPServer(t, func(w dns.ResponseWriter, r *dns.Msg) {
		_ = w.WriteMsg(encryptedTestReply(r))
	})

	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Cache.Enabled = false
	cfg.Upstreams = []UpstreamConfig{
		{ID: "missing-from-mgr", Addr: "127.0.0.1:1", Protocol: "udp"},
		{ID: "real-default", Addr: srvAddr, Protocol: "udp"},
	}
	cfg.DefaultUpstream = "real-default"
	cfg.Rules = []RuleConfig{
		{ID: "ghost-rule", Domain: []string{"ghost.example"}, Upstream: "ghost-id", Action: "route"},
	}

	mod := NewDnsModule(cfg)
	if err := mod.Start(); err != nil {
		t.Fatal(err)
	}
	defer mod.Stop()

	// Remove "missing-from-mgr" from manager to verify fallback skips nil GetUpstream results
	// and prefers defaultUpstream ("real-default").
	mgr := mod.GetUpstreamManager()
	mgr.mu.Lock()
	delete(mgr.upstreamsByID, "missing-from-mgr")
	delete(mgr.upstreams, BuildUpstreamKey("127.0.0.1:1", "direct"))
	mgr.mu.Unlock()

	resp, err := mod.handler.HandleQuery(context.Background(), &DnsQuery{Name: "ghost.example", QType: TypeA})
	if err != nil || resp == nil || resp.Upstream != srvAddr {
		t.Fatalf("fallback did not skip nil or prefer defaultUpstream: err=%v resp=%+v", err, resp)
	}
}
