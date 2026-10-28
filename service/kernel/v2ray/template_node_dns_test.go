package v2ray

import (
	"testing"

	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

func dnsNode(t *testing.T, host string) serverObj.ServerObj {
	t.Helper()
	node, err := serverObj.NewFromLink("vless", "vless://00000000-0000-0000-0000-000000000001@"+host+":443#test")
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestSubscriptionDNSOnlyMatchesNodeHostnames(t *testing.T) {
	infos := []serverInfo{{Info: dnsNode(t, "a.example"), NodeDNS: []string{"1.1.1.1", "8.8.8.8"}}, {Info: dnsNode(t, "b.example")}, {Info: dnsNode(t, "127.0.0.1"), NodeDNS: []string{"9.9.9.9"}}}
	upstreams, rules, _, err := nodeDNSRules(infos)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || len(upstreams) != 2 {
		t.Fatalf("unexpected rules/upstreams: %v %v", rules, upstreams)
	}
	if rules[0]["domain"].([]string)[0] != "a.example" || rules[0]["domain_suffix"] != nil {
		t.Fatal("private DNS escaped exact hostname scope")
	}
	if upstreams[0]["proxy_tag"] != "direct" || len(upstreams[0]["fallback"].([]string)) != 1 {
		t.Fatal("missing direct bootstrap/fallback")
	}
	mixed := append(append([]serverInfo{}, infos...), serverInfo{Info: dnsNode(t, "a.example")})
	if _, _, _, err := nodeDNSRules(mixed); err == nil {
		t.Fatal("private DNS silently overrides another subscription's default policy")
	}
	infos = append(infos, serverInfo{Info: dnsNode(t, "a.example"), NodeDNS: []string{"9.9.9.9"}})
	if _, _, _, err := nodeDNSRules(infos); err == nil {
		t.Fatal("conflicting DNS policies silently accepted")
	}
}
