package dns

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type reviewDispatcher func(context.Context, string, string, string) (net.Conn, error)

func (f reviewDispatcher) Dispatch(ctx context.Context, network, addr, tag string) (net.Conn, error) {
	return f(ctx, network, addr, tag)
}

func fragmentedDNSReply(wrongID bool) net.Conn {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		request, err := (&dns.Conn{Conn: server}).ReadMsg()
		if err != nil {
			return
		}
		response := new(dns.Msg).SetReply(request)
		if wrongID {
			response.Id++
		}
		body, _ := response.Pack()
		wire := make([]byte, 2, 2+len(body))
		binary.BigEndian.PutUint16(wire, uint16(len(body)))
		wire = append(wire, body...)
		for _, b := range wire {
			if _, err := server.Write([]byte{b}); err != nil {
				return
			}
		}
	}()
	return client
}

func TestDispatcherFragmentedResponseAndID(t *testing.T) {
	for _, wrongID := range []bool{false, true} {
		mgr := NewUpstreamManager(nil)
		mgr.SetDispatcher(reviewDispatcher(func(context.Context, string, string, string) (net.Conn, error) {
			return fragmentedDNSReply(wrongID), nil
		}))
		_, err := mgr.Exchange(&UpstreamInstance{Addr: "resolver.invalid:53", ProxyTag: "test"}, &DnsQuery{Name: "example.com", QType: TypeA})
		if (err != nil) != wrongID {
			t.Fatalf("wrongID=%v: %v", wrongID, err)
		}
	}
}

func TestUnavailableDNSProxyFailsClosed(t *testing.T) {
	mgr := NewUpstreamManager(nil)
	mgr.SetProxyAddrResolver(func(string) string { return "" })
	if got := mgr.resolveProxyAddr("proxy"); got != "" {
		t.Fatalf("resolver was overridden: %q", got)
	}
	_, err := mgr.Exchange(&UpstreamInstance{Addr: "resolver.invalid:53", ProxyTag: "missing"}, &DnsQuery{Name: "example.com", QType: TypeA})
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected unavailable proxy error, got %v", err)
	}
}

func TestHealthChecksUseRouteAndPublishSnapshots(t *testing.T) {
	mgr := NewUpstreamManager([]UpstreamConfig{{ID: "group", Addr: "resolver.invalid:53", ProxyTag: "group"}})
	mgr.SetDispatcher(reviewDispatcher(func(_ context.Context, _, _ string, tag string) (net.Conn, error) {
		if tag != "group" {
			t.Errorf("wrong route %s", tag)
		}
		return fragmentedDNSReply(false), nil
	}))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if !mgr.HealthCheck()[BuildUpstreamKey("resolver.invalid:53", "group")] {
				t.Error("route was not probed")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				for _, u := range mgr.ListUpstreams() {
					_ = u.Healthy
				}
				u, _ := mgr.GetUpstream("group")
				_ = u.Healthy
			}
		}()
	}
	wg.Wait()
}

func TestQuestionMatchChecksClassAndCount(t *testing.T) {
	msg := new(dns.Msg).SetQuestion("EXAMPLE.com.", dns.TypeA)
	if err := ValidateQuestionMatch(msg, "example.com", TypeA); err != nil {
		t.Fatal(err)
	}
	msg.Question[0].Qclass = dns.ClassCHAOS
	if err := ValidateQuestionMatch(msg, "example.com", TypeA); err == nil {
		t.Fatal("wrong class accepted")
	}
	msg.Question[0].Qclass = dns.ClassINET
	msg.Question = append(msg.Question, msg.Question[0])
	if err := ValidateQuestionMatch(msg, "example.com", TypeA); err == nil {
		t.Fatal("extra question accepted")
	}
}
