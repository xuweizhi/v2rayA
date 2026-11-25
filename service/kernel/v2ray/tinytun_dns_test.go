//go:build tinytun

package v2ray

import (
	"slices"
	"testing"

	"github.com/v2rayA/v2rayA/db/configure"
)

func TestTinyTunDNSRetainsUpstreamAndTransportPerRule(t *testing.T) {
	cfg, err := buildTinyTunDNSConfigFromRules([]configure.DnsRule{
		{Domain: "full:test.example", Action: "bypass", Outbound: "proxy", Upstream: "tls://[2001:db8::8]"},
		{Server: "https://dns.example/dns-query", Domains: "domain:other.example", Outbound: "proxy"},
		{Action: "reject", Domain: "domain:blocked.example"},
		{Upstream: "2001:db8::5", Outbound: "direct"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 3 {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
	for i, want := range [][3]string{{"dot", "direct", "[2001:db8::8]:853"}, {"doh", "proxy", "https://dns.example/dns-query"}, {"udp", "direct", "[2001:db8::5]:53"}} {
		g := cfg.Groups[i]
		if g.Protocol != want[0] || g.Upstream != want[1] || !slices.Equal(g.Servers, []string{want[2]}) {
			t.Fatalf("group %d = %+v", i, g)
		}
	}
	if cfg.Routing.FallbackGroup != cfg.Groups[2].Name || !slices.Equal(cfg.Routing.Rules, []string{"match(domain:test.example)," + cfg.Groups[0].Name, "match(suffix:other.example)," + cfg.Groups[1].Name, "match(suffix:blocked.example),reject"}) {
		t.Fatalf("routing = %+v", cfg.Routing)
	}
}
