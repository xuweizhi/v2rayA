package dns

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type countingDNSHandler struct{ calls atomic.Int32 }

func (h *countingDNSHandler) HandleQuery(_ context.Context, q *DnsQuery) (*DnsResponse, error) {
	h.calls.Add(1)
	response := new(dns.Msg).SetReply(upstreamQuestion(q))
	// Some servers echo private EDNS options back in their responses.
	markOutgoing(response)
	return &DnsResponse{RawMsg: response}, nil
}

func TestListenerRefusesOwnQueriesAndKeepsMarkerPrivate(t *testing.T) {
	handler := new(countingDNSHandler)
	l := NewDnsListener(&DnsListenerConfig{ListenAddr: "127.0.0.1:0", Timeout: 2}, handler)
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Stop(); err != nil {
			t.Error(err)
		}
	})
	addr := l.servers[0].udp.PacketConn.LocalAddr().String()

	for _, network := range []string{"udp", "tcp"} {
		client := dns.Client{Net: network}
		q := new(dns.Msg).SetQuestion("example.test.", dns.TypeA)
		for _, edns := range []bool{false, true} {
			if edns {
				q.SetEdns0(1232, false)
			}
			answer, _, err := client.Exchange(q, addr)
			if err != nil {
				t.Fatal(err)
			}
			if answer.Rcode != dns.RcodeSuccess || carriesOwnToken(answer) || (answer.IsEdns0() != nil) != edns {
				t.Errorf("%s edns=%v: unexpected reply %v", network, edns, answer)
			}
		}
		markOutgoing(q)
		before := handler.calls.Load()
		answer, _, err := client.Exchange(q, addr)
		if err != nil {
			t.Fatal(err)
		}
		if answer.Rcode != dns.RcodeServerFailure || handler.calls.Load() != before {
			t.Errorf("%s: marked question reached the handler or got rcode %d", network, answer.Rcode)
		}
	}
}

func TestListenerBindFailureReleasesUDPAndEarlierPairs(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := occupied.Addr().String()
	t.Cleanup(func() { occupied.Close() })
	l := NewDnsListener(&DnsListenerConfig{ListenAddr: "127.0.0.1:0", ExtraListenAddrs: []string{addr}, Timeout: 2}, new(countingDNSHandler))
	if err := l.Start(); err == nil {
		t.Fatal("second address should fail TCP bind")
	}
	if l.Healthy() || len(l.servers) != 0 {
		t.Fatal("listener state survived failed startup")
	}
	retry, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("UDP port remained occupied: %v", err)
	}
	retry.Close()
	l.config.ExtraListenAddrs = nil
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamQuestionECS(t *testing.T) {
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"", false}, {"127.0.0.1", false}, {"172.20.1.5", false}, {"192.168.1.5", false},
		{"169.254.1.2", false}, {"::ffff:10.0.0.1", false}, {"fd01::3", false}, {"fe80::1", false},
		{"1.1.1.1", true}, {"2606:4700:4700::1111", true},
	} {
		msg := upstreamQuestion(&DnsQuery{Name: "example.test", QType: TypeA, ClientIP: net.ParseIP(tc.ip)})
		have := false
		for _, opt := range msg.IsEdns0().Option {
			if _, ok := opt.(*dns.EDNS0_SUBNET); ok {
				have = true
			}
		}
		if have != tc.want || !carriesOwnToken(msg) {
			t.Errorf("client %q: ECS %v, loop mark %v", tc.ip, have, carriesOwnToken(msg))
		}
	}
}

func TestModuleStopLetsActiveHandlerReadState(t *testing.T) {
	cfg := DefaultDnsModuleConfig()
	cfg.Listener.ListenAddr = "127.0.0.1:0"
	cfg.Upstreams = []UpstreamConfig{{ID: "test", Addr: "127.0.0.1:53", ProxyTag: "test"}}
	module := NewDnsModule(cfg)
	inQuery := make(chan struct{})
	module.SetDispatcher(reviewDispatcher(func(context.Context, string, string, string) (net.Conn, error) {
		close(inQuery)
		<-module.listener.ctx.Done()
		module.GetRouter()
		return nil, context.Canceled
	}))
	if err := module.Start(); err != nil {
		t.Fatal(err)
	}
	queryDone := make(chan struct{})
	addr := module.listener.servers[0].udp.PacketConn.LocalAddr().String()
	go func() {
		defer close(queryDone)
		q := new(dns.Msg).SetQuestion("example.test.", dns.TypeA)
		_, _, _ = (&dns.Client{Timeout: 2 * time.Second}).Exchange(q, addr)
	}()
	select {
	case <-inQuery:
	case <-queryDone:
		_ = module.Stop()
		t.Fatal("query did not reach the test dispatcher")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- module.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop blocked the active handler from reading module state")
	}
	<-queryDone
	if err := module.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := module.Stop(); err != nil {
		t.Fatal(err)
	}
}
