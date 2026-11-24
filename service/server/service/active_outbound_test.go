package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

func TestSwitchActiveOutbound(t *testing.T) {
	resetReviewDB(t)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const exit = "出口组"
	require(configure.AddOutbound(exit))
	if err := SwitchActiveOutbound(exit); err == nil {
		t.Fatal("accepted an empty group")
	}
	first, second := reviewNode(t, "first"), reviewNode(t, "second")
	require(configure.AppendServers([]*configure.ServerRaw{&first, &second}))
	for _, group := range []string{"proxy", exit} {
		require(configure.AddConnect(configure.Which{TYPE: configure.ServerType, ID: 1, Outbound: group}))
	}
	require(configure.AddConnect(configure.Which{TYPE: configure.ServerType, ID: 2, Outbound: exit}))
	chain := configure.DefaultOutboundSetting()
	chain.PreProxy = "proxy"
	require(configure.SetOutboundSetting(exit, chain))
	if ActiveOutbound() != "proxy" {
		t.Fatal("older database lost default group")
	}
	require(SwitchActiveOutbound(exit))
	if err := DeleteOutbound(exit); err == nil || configure.GetConnectedServersByOutbound(exit).Len() != 2 {
		t.Fatal("deleting active group changed its membership")
	}
	settings := configure.NewSetting()
	settings.Transparent = configure.TransparentWhitelist
	settings.RulePortMode = configure.WhitelistMode
	settings.RoutingRules = []configure.RoutingRuleItem{{Outbound: "proxy", Domain: []string{"domain:pinned.example"}}}
	tmpl, err := v2ray.NewTemplateFromConnectedServers(settings)
	require(err)
	defer tmpl.Close()
	rules := tmpl.Routing.Rules
	if rules[len(rules)-1].BalancerTag != exit {
		t.Fatal("default route does not select the exit balancer")
	}
	var pinned, front, whitelist, direct bool
	for _, r := range rules {
		if slices.Contains(r.Domain, "domain:pinned.example") {
			pinned = r.OutboundTag == "proxy"
		}
		for _, in := range r.InboundTag {
			if strings.HasPrefix(in, "v2raya-pre-in-") {
				front = r.OutboundTag == "proxy"
			}
		}
		if slices.Contains(r.Domain, "geosite:google") {
			whitelist = r.BalancerTag == exit
		}
		if slices.Contains(r.Domain, "geosite:cn") {
			direct = r.OutboundTag == "direct"
		}
	}
	if !pinned || !front || !whitelist || !direct {
		t.Fatalf("incorrect routes: pinned=%t front=%t whitelist=%t direct=%t", pinned, front, whitelist, direct)
	}
	// A least-ping group used as a pre-proxy must stop when selection fails.
	require(configure.AddConnect(configure.Which{TYPE: configure.ServerType, ID: 2, Outbound: "proxy"}))
	frontSettings := configure.DefaultOutboundSetting()
	frontSettings.Type = configure.LeastPing
	require(configure.SetOutboundSetting("proxy", frontSettings))
	chained, err := v2ray.NewTemplateFromConnectedServers(settings)
	require(err)
	defer chained.Close()
	fallbacks := map[string]string{}
	for _, balancer := range chained.Routing.Balancers {
		fallbacks[balancer.Tag] = balancer.FallbackTag
	}
	if fallbacks["proxy"] != "block" || fallbacks[exit] != "" {
		t.Fatalf("unexpected fallback for chained groups: %v", fallbacks)
	}
	require(configure.ClearConnects(exit))
	tmpl, err = v2ray.NewTemplateFromConnectedServers(settings)
	require(err)
	defer tmpl.Close()
	rules = tmpl.Routing.Rules
	if rules[len(rules)-1].OutboundTag != "block" {
		t.Fatal("empty selected group sent traffic to another exit")
	}
	require(SwitchActiveOutbound("proxy"))
	require(DeleteOutbound(exit))
}
