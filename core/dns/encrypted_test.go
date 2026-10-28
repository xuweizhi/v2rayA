package dns

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func encryptedTestReply(q *dns.Msg) *dns.Msg {
	r := new(dns.Msg)
	r.SetReply(q)
	r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, 1)}}
	return r
}

type encryptedTestDispatcher struct {
	calls        atomic.Int32
	address, tag string
}

func (d *encryptedTestDispatcher) Dispatch(ctx context.Context, network, address, tag string) (net.Conn, error) {
	d.calls.Add(1)
	d.address, d.tag = address, tag
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestEncryptedDNSDoHPreservesHostnameAndSelectedRoute(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "example.com" || r.TLS.ServerName != "example.com" {
			t.Error("bootstrap lost original host/SNI")
		}
		body, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		if err := q.Unpack(body); err != nil {
			t.Error(err)
			return
		}
		wire, _ := encryptedTestReply(q).Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(wire)
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	up := &UpstreamInstance{ID: "test", Addr: "https://example.com/dns-query", DialAddress: strings.TrimPrefix(srv.URL, "https://"), Protocol: "https", ProxyTag: "landing"}
	mgr := NewUpstreamManager(nil)
	dispatcher := &encryptedTestDispatcher{}
	mgr.SetDispatcher(dispatcher)
	q := &DnsQuery{Name: "node.example", QType: QueryType(dns.TypeA)}
	res, err := mgr.exchangeEncryptedWithTLS(up, q, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil || res == nil || len(res.Answer) != 1 {
		t.Fatalf("DoH query: %v %+v", err, res)
	}
	if dispatcher.calls.Load() != 1 || dispatcher.tag != "landing" || dispatcher.address != up.DialAddress {
		t.Fatalf("wrong dispatcher path: %+v", dispatcher)
	}
	if _, err = mgr.exchangeEncrypted(up, q); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
	if requests.Load() != 1 {
		t.Fatal("TLS failure reached HTTP handler")
	}
	mgr.SetDispatcher(nil)
	if _, err = mgr.exchangeEncryptedWithTLS(up, q, &tls.Config{RootCAs: roots}); err == nil {
		t.Fatal("missing group silently used direct")
	}
}

func TestEncryptedDNSDoTFraming(t *testing.T) {
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", certServer.TLS.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		dc := &dns.Conn{Conn: conn}
		q, err := dc.ReadMsg()
		if err == nil {
			err = dc.WriteMsg(encryptedTestReply(q))
		}
		done <- err
	}()
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	up := &UpstreamInstance{Addr: net.JoinHostPort("example.com", port), DialAddress: listener.Addr().String(), Protocol: "tcp-tls", ProxyTag: "direct"}
	res, err := NewUpstreamManager(nil).exchangeEncryptedWithTLS(up, &DnsQuery{Name: "node.example", QType: QueryType(dns.TypeA)}, &tls.Config{RootCAs: roots})
	if err != nil || res == nil || len(res.Answer) != 1 {
		t.Fatalf("DoT query: %v %+v", err, res)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExplicitDNSFallbackAndCacheIsolation(t *testing.T) {
	start := func(rcode int) (string, *atomic.Int32) {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		calls := new(atomic.Int32)
		srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
			calls.Add(1)
			r := encryptedTestReply(q)
			r.Rcode = rcode
			_ = w.WriteMsg(r)
		})}
		go srv.ActivateAndServe()
		t.Cleanup(func() { _ = srv.Shutdown(); _ = pc.Close() })
		return pc.LocalAddr().String(), calls
	}
	primary, pCalls := start(dns.RcodeRefused)
	backup, bCalls := start(dns.RcodeSuccess)
	unrelated, uCalls := start(dns.RcodeSuccess)
	mgr := NewUpstreamManager([]UpstreamConfig{{ID: "primary", Addr: primary, Protocol: "udp", Fallback: []string{"backup"}}, {ID: "backup", Addr: backup, Protocol: "udp"}, {ID: "unrelated", Addr: unrelated, Protocol: "udp"}, {ID: "without-backup", Addr: primary, Protocol: "udp"}})
	up, _ := mgr.GetUpstream("primary")
	q := &DnsQuery{Name: "node.example", QType: QueryType(dns.TypeA)}
	res, err := mgr.Exchange(up, q)
	if err != nil || res == nil || res.Upstream != backup {
		t.Fatalf("fallback failed: %v %+v", err, res)
	}
	if pCalls.Load() != 1 || bCalls.Load() != 1 || uCalls.Load() != 0 {
		t.Fatal("unexpected fallback path")
	}
	without, _ := mgr.GetUpstream("without-backup")
	if _, err = mgr.Exchange(without, q); err == nil {
		t.Fatal("unconfigured fallback used")
	}
	if bCalls.Load() != 1 || uCalls.Load() != 0 {
		t.Fatal("another policy's fallback was used")
	}
	if buildCacheKey(q, up) == buildCacheKey(q, without) {
		t.Fatal("fallback answer cache shared across policies")
	}
}

func TestDNSFallbackAfterUnresponsivePrimary(t *testing.T) {
	blackhole, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blackhole.Close()
	backup, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	srv := &dns.Server{PacketConn: backup, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) { _ = w.WriteMsg(encryptedTestReply(q)) })}
	go srv.ActivateAndServe()
	defer srv.Shutdown()
	mgr := NewUpstreamManager([]UpstreamConfig{{ID: "primary", Addr: blackhole.LocalAddr().String(), Protocol: "udp", Fallback: []string{"backup"}}, {ID: "backup", Addr: backup.LocalAddr().String(), Protocol: "udp"}})
	up, _ := mgr.GetUpstream("primary")
	start := time.Now()
	res, err := mgr.Exchange(up, &DnsQuery{Name: "node.example", QType: QueryType(dns.TypeA)})
	if err != nil || res == nil || len(res.Answer) != 1 {
		t.Fatalf("silent timeout did not fall back: %v %+v", err, res)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("fallback exceeded listener budget")
	}
}
