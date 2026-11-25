package v2ray

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/v2ray/where"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "v2raya-template-test-")
	if err != nil {
		panic(err)
	}
	params := conf.GetEnvironmentConfig()
	params.Config = dir
	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestGenerateDnsModuleConfigExtraListenAndDefaultAddr(t *testing.T) {
	origCould := couldLocalDnsListen
	t.Cleanup(func() { couldLocalDnsListen = origCould })

	type dnsCfg struct {
		Listener struct {
			ListenAddr       string   `json:"listen_addr"`
			ExtraListenAddrs []string `json:"extra_listen_addrs"`
		} `json:"listener"`
	}

	// 1. Transparent off + PortSharing false -> 127.0.0.1:52353, NO extra_listen_addrs
	couldLocalDnsListen = func() (bool, error) { return true, nil }
	tmpl := &Template{
		Setting: &configure.Setting{
			Transparent: configure.TransparentClose, DnsListenAddr: "0.0.0.0:52353",
			TransparentType: configure.TransparentRedirect,
			PortSharing:     false,
		},
	}
	if err := tmpl.generateDnsModuleConfigWithRules(nil, configure.DefaultDnsRules); err != nil {
		t.Fatal(err)
	}
	var got dnsCfg
	if err := json.Unmarshal(tmpl.DnsModuleConfig, &got); err != nil {
		t.Fatal(err)
	}
	if got.Listener.ListenAddr != "127.0.0.1:52353" {
		t.Fatalf("expected 127.0.0.1:52353 when PortSharing=false, got %q", got.Listener.ListenAddr)
	}
	if len(got.Listener.ExtraListenAddrs) != 0 {
		t.Fatalf("expected no extra_listen_addrs when transparent off, got %v", got.Listener.ExtraListenAddrs)
	}

	// 2. TransparentSystemProxy + PortSharing true -> 0.0.0.0:52353, NO extra_listen_addrs
	tmpl.Setting = &configure.Setting{
		Transparent:     configure.TransparentWhitelist,
		TransparentType: configure.TransparentSystemProxy,
		PortSharing:     true,
	}
	if err := tmpl.generateDnsModuleConfigWithRules(nil, configure.DefaultDnsRules); err != nil {
		t.Fatal(err)
	}
	got = dnsCfg{}
	if err := json.Unmarshal(tmpl.DnsModuleConfig, &got); err != nil {
		t.Fatal(err)
	}
	if got.Listener.ListenAddr != "0.0.0.0:52353" {
		t.Fatalf("expected 0.0.0.0:52353 when PortSharing=true, got %q", got.Listener.ListenAddr)
	}
	if len(got.Listener.ExtraListenAddrs) != 0 {
		t.Fatalf("expected no extra_listen_addrs in system_proxy mode, got %v", got.Listener.ExtraListenAddrs)
	}

	// 3. TransparentRedirect + couldLocalDnsListen=false -> NO extra_listen_addrs
	couldLocalDnsListen = func() (bool, error) { return false, DnsPortOccupied }
	tmpl.Setting = &configure.Setting{
		Transparent:     configure.TransparentWhitelist,
		TransparentType: configure.TransparentRedirect,
		PortSharing:     false,
	}
	if err := tmpl.generateDnsModuleConfigWithRules(nil, configure.DefaultDnsRules); err != nil {
		t.Fatal(err)
	}
	got = dnsCfg{}
	if err := json.Unmarshal(tmpl.DnsModuleConfig, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Listener.ExtraListenAddrs) != 0 {
		t.Fatalf("expected no extra_listen_addrs when port 53 unavailable, got %v", got.Listener.ExtraListenAddrs)
	}

	// 4. Linux + TransparentRedirect + couldLocalDnsListen=true -> includes 127.2.0.17:53
	if runtime.GOOS == "linux" {
		couldLocalDnsListen = func() (bool, error) { return true, nil }
		if err := tmpl.generateDnsModuleConfigWithRules(nil, configure.DefaultDnsRules); err != nil {
			t.Fatal(err)
		}
		got = dnsCfg{}
		if err := json.Unmarshal(tmpl.DnsModuleConfig, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Listener.ExtraListenAddrs) != 1 || got.Listener.ExtraListenAddrs[0] != "127.2.0.17:53" {
			t.Fatalf("expected [127.2.0.17:53], got %v", got.Listener.ExtraListenAddrs)
		}
	}
}

func TestGenerateDnsModuleConfigPrefixesRejectFallbackAndNodeCase(t *testing.T) {
	origCould := couldLocalDnsListen
	couldLocalDnsListen = func() (bool, error) { return false, nil }
	t.Cleanup(func() { couldLocalDnsListen = origCould })

	tmpl := &Template{
		Setting: &configure.Setting{
			Transparent: configure.TransparentClose,
		},
	}

	rules := []configure.DnsRule{
		{
			RuleID:   "r-direct-cn",
			Upstream: "223.5.5.5",
			Domain:   "full:Exact.Example.COM.\ndomain:Sub.Example.ORG.\nsuffix:Suf.NET.\nregexp:^Ads\\.[A-Z]+\\.com$\nkeyword:TrackMe\ngeosite:cn",
			Outbound: "direct",
		},
		{
			RuleID:   "r-proxy-google",
			Upstream: "1.1.1.1",
			Domain:   "geosite:google",
			Outbound: "proxy",
		},
		{
			RuleID:   "r-reject-empty-upstream",
			Upstream: "",
			Domain:   "domain:ads.example.com",
			Action:   "reject",
		},
		{
			RuleID:   "r-block-outbound",
			Upstream: "8.8.8.8",
			Domain:   "full:blocked.example.com",
			Outbound: "block",
		},
		{
			RuleID:    "r-qtype-only",
			Upstream:  "",
			QueryType: "AAAA",
			Action:    "reject",
		},
		{
			// Fallback reuses 223.5.5.5+direct (deduplicated with r-direct-cn!)
			RuleID:   "r-fallback",
			Upstream: "223.5.5.5",
			Domain:   "",
			Outbound: "direct",
		},
	}

	infos := []serverInfo{
		{Info: dnsNode(t, "Upper.Node.Example.COM.")},
	}

	if err := tmpl.generateDnsModuleConfigWithRules(infos, rules); err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		DefaultUpstream string `json:"default_upstream"`
		Upstreams       []struct {
			ID       string `json:"id"`
			Addr     string `json:"addr"`
			ProxyTag string `json:"proxy_tag"`
		} `json:"upstreams"`
		Rules []struct {
			ID           string   `json:"id"`
			Upstream     string   `json:"upstream"`
			Action       string   `json:"action"`
			Domain       []string `json:"domain"`
			DomainSuffix []string `json:"domain_suffix"`
			DomainRegex  []string `json:"domain_regex"`
			QueryType    []string `json:"query_type"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(tmpl.DnsModuleConfig, &parsed); err != nil {
		t.Fatal(err)
	}

	// Verify default_upstream is set and matches the deduplicated 223.5.5.5 direct upstream,
	// AND that upstream is moved to the end of upstreams!
	if parsed.DefaultUpstream == "" {
		t.Fatal("expected default_upstream to be populated")
	}
	lastUp := parsed.Upstreams[len(parsed.Upstreams)-1]
	if lastUp.ID != parsed.DefaultUpstream || lastUp.Addr != "223.5.5.5:53" || lastUp.ProxyTag != "direct" {
		t.Fatalf("expected fallback upstream 223.5.5.5:53 (direct) at end with id=%s, got last=%+v", parsed.DefaultUpstream, lastUp)
	}
	// Ensure no upstream has proxy_tag == "block"
	for _, u := range parsed.Upstreams {
		if u.ProxyTag == "block" {
			t.Fatalf("unexpected block proxy_tag in upstreams: %+v", u)
		}
	}

	byID := make(map[string]int)
	for i, r := range parsed.Rules {
		byID[r.ID] = i
	}

	// Node rule must have lowercased, trailing-dot-trimmed hostname
	nodeIdx, ok := byID["rule-node"]
	if !ok {
		t.Fatal("missing rule-node")
	}
	if len(parsed.Rules[nodeIdx].Domain) != 1 || parsed.Rules[nodeIdx].Domain[0] != "upper.node.example.com" {
		t.Fatalf("expected normalized node hostname, got %v", parsed.Rules[nodeIdx].Domain)
	}

	// Prefix translations
	cnIdx, ok := byID["r-direct-cn"]
	if !ok {
		t.Fatal("missing r-direct-cn")
	}
	cnRule := parsed.Rules[cnIdx]
	wantDomains := map[string]bool{"exact.example.com": true, "keyword:trackme": true, "geosite:cn": true}
	for _, d := range cnRule.Domain {
		delete(wantDomains, d)
	}
	if len(wantDomains) != 0 {
		t.Fatalf("missing expected domain entries %v in %v", wantDomains, cnRule.Domain)
	}
	wantSuffixes := map[string]bool{"sub.example.org": true, "suf.net": true}
	for _, s := range cnRule.DomainSuffix {
		delete(wantSuffixes, s)
	}
	if len(wantSuffixes) != 0 {
		t.Fatalf("missing expected domain_suffix entries %v in %v", wantSuffixes, cnRule.DomainSuffix)
	}
	if len(cnRule.DomainRegex) != 1 || cnRule.DomainRegex[0] != `^Ads\.[A-Z]+\.com$` {
		t.Fatalf("expected case-preserved domain_regex, got %v", cnRule.DomainRegex)
	}

	// Reject rule with empty upstream was NOT skipped
	rejIdx, ok := byID["r-reject-empty-upstream"]
	if !ok || parsed.Rules[rejIdx].Action != "reject" {
		t.Fatalf("reject rule with empty upstream missing or wrong action: ok=%v", ok)
	}

	// Block outbound rule became action=reject
	blkIdx, ok := byID["r-block-outbound"]
	if !ok || parsed.Rules[blkIdx].Action != "reject" {
		t.Fatalf("block outbound rule missing or wrong action: ok=%v", ok)
	}

	// QueryType-only rule was NOT dropped
	qtIdx, ok := byID["r-qtype-only"]
	if !ok {
		t.Fatal("QueryType-only rule was dropped")
	}
	if parsed.Rules[qtIdx].Action != "reject" || len(parsed.Rules[qtIdx].QueryType) != 1 || parsed.Rules[qtIdx].QueryType[0] != "AAAA" {
		t.Fatalf("unexpected QueryType-only rule: %+v", parsed.Rules[qtIdx])
	}
}

func TestSetAPIRoutePrecedesCustomRoutingRules(t *testing.T) {
	tmpl := NewEmptyTemplate(&configure.Setting{
		Transparent: configure.TransparentClose,
		RoutingRules: []configure.RoutingRuleItem{
			{Outbound: "direct", IP: []string{"127.0.0.0/8", "0.0.0.0/0"}},
		},
	})
	tmpl.Outbounds = []coreObj.OutboundObject{
		{Tag: "direct", Protocol: "freedom"},
		{Tag: "block", Protocol: "blackhole"},
	}
	tmpl.PrependCustomRoutingRules()
	if _, err := tmpl.SetAPI(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmpl.Close() })

	// Verify SetAPI appended api-in rule to internalRoutes (not tail of Routing.Rules)
	if len(tmpl.internalRoutes) == 0 || tmpl.internalRoutes[len(tmpl.internalRoutes)-1].OutboundTag != "api-out" {
		t.Fatalf("expected api-in -> api-out in internalRoutes, got %+v", tmpl.internalRoutes)
	}

	// Verify ToConfigBytes flushes internalRoutes before user custom rules
	raw := tmpl.ToConfigBytes()
	var cfg struct {
		Routing struct {
			Rules []coreObj.RoutingRule `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routing.Rules) < 2 {
		t.Fatalf("expected at least 2 rules, got %d", len(cfg.Routing.Rules))
	}
	if cfg.Routing.Rules[0].OutboundTag != "api-out" || len(cfg.Routing.Rules[0].InboundTag) != 1 || cfg.Routing.Rules[0].InboundTag[0] != "api-in" {
		t.Fatalf("api-in rule must be first, got %+v", cfg.Routing.Rules[0])
	}
}

func TestSetDualStackTransparentIPv6(t *testing.T) {
	origV6 := isIPv6Supported
	t.Cleanup(func() { isIPv6Supported = origV6 })

	// Case 1: TransparentRedirect + PortSharing=false + IPv6 supported -> 127.0.0.1 & ::1 twins
	isIPv6Supported = func() bool { return true }
	tmpl := &Template{
		Setting: &configure.Setting{
			Transparent:     configure.TransparentWhitelist,
			TransparentType: configure.TransparentRedirect,
			PortSharing:     false,
		},
	}
	tmpl.AppendDokodemoTProxy("redirect", 52345, "transparent")
	tmpl.Routing.Rules = []coreObj.RoutingRule{
		{Type: "field", InboundTag: []string{"transparent"}, OutboundTag: "proxy"},
	}
	tmpl.setDualStack()

	var hasV4, hasV6 bool
	for _, ib := range tmpl.Inbounds {
		if ib.Tag == "transparent_ipv4" && ib.Listen == "127.0.0.1" {
			hasV4 = true
		}
		if ib.Tag == "transparent_ipv6" && ib.Listen == "::1" {
			hasV6 = true
		}
	}
	if !hasV4 || !hasV6 {
		t.Fatalf("expected transparent_ipv4 (127.0.0.1) and transparent_ipv6 (::1), got %+v", tmpl.Inbounds)
	}
	ruleTags := tmpl.Routing.Rules[0].InboundTag
	if len(ruleTags) != 2 || ruleTags[0] != "transparent_ipv4" || ruleTags[1] != "transparent_ipv6" {
		t.Fatalf("expected routing rule tags [transparent_ipv4 transparent_ipv6], got %v", ruleTags)
	}

	// Case 2: PortSharing=true + IPv6 supported (Redirect & TProxy) -> dual-stack "::"
	for _, tt := range []configure.TransparentType{configure.TransparentRedirect, configure.TransparentTproxy} {
		isIPv6Supported = func() bool { return true }
		tmpl2 := &Template{
			Setting: &configure.Setting{
				Transparent:     configure.TransparentWhitelist,
				TransparentType: tt,
				PortSharing:     true,
			},
		}
		tmpl2.AppendDokodemoTProxy(string(tt), 52345, "transparent")
		tmpl2.setDualStack()
		if len(tmpl2.Inbounds) != 1 || tmpl2.Inbounds[0].Listen != "::" || tmpl2.Inbounds[0].Tag != "transparent" {
			t.Fatalf("mode %s with PortSharing=true & IPv6: expected single :: listener, got %+v", tt, tmpl2.Inbounds)
		}
		if err := tmpl2.checkDuplicatedInboundSockets(); err != nil {
			t.Fatalf("unexpected socket conflict for :: transparent inbound: %v", err)
		}

		// IPv4-only system must fall back to 0.0.0.0
		isIPv6Supported = func() bool { return false }
		tmpl3 := &Template{
			Setting: &configure.Setting{
				Transparent:     configure.TransparentWhitelist,
				TransparentType: tt,
				PortSharing:     true,
			},
		}
		tmpl3.AppendDokodemoTProxy(string(tt), 52345, "transparent")
		tmpl3.setDualStack()
		if len(tmpl3.Inbounds) != 1 || tmpl3.Inbounds[0].Listen != "0.0.0.0" {
			t.Fatalf("mode %s IPv4-only: expected 0.0.0.0 listener, got %+v", tt, tmpl3.Inbounds)
		}
	}
}

func TestSetTransparentRoutingSystemProxyIncludesSocks(t *testing.T) {
	modes := []configure.TransparentMode{
		configure.TransparentProxy,
		configure.TransparentWhitelist,
		configure.TransparentGfwlist,
		configure.TransparentFollowRule,
	}
	for _, mode := range modes {
		tmpl := &Template{
			Setting: &configure.Setting{
				Transparent:     mode,
				TransparentType: configure.TransparentSystemProxy,
				RulePortMode:    configure.WhitelistMode,
			},
			defaultProxyTag: "proxy",
		}
		if mode == configure.TransparentFollowRule {
			tmpl.Routing.Rules = []coreObj.RoutingRule{
				{Type: "field", InboundTag: []string{"rule-http", "rule-socks"}, OutboundTag: "direct"},
			}
		}
		if err := tmpl.setTransparentRouting(); err != nil {
			t.Fatalf("mode %v error: %v", mode, err)
		}
		if len(tmpl.Routing.Rules) == 0 {
			t.Fatalf("mode %v produced no rules", mode)
		}
		for i, r := range tmpl.Routing.Rules {
			hasHTTP, hasSocks := false, false
			for _, tag := range r.InboundTag {
				if tag == "transparent" {
					hasHTTP = true
				}
				if tag == "transparent-socks" {
					hasSocks = true
				}
			}
			if !hasHTTP || !hasSocks {
				t.Fatalf("mode %v rule #%d missing transparent or transparent-socks: %v", mode, i, r.InboundTag)
			}
		}
	}
}

func TestParseRoutingARulesSecurityAndFallback(t *testing.T) {
	validOutbounds := map[string]bool{
		"direct": false,
		"block":  false,
		"proxy":  false,
		"hkgrp":  true, // balancer group
	}

	// 1. Default directive parsed, inboundTag override ignored, fallback uses balancerTag
	text := `
default: hkgrp
domain(geosite:cn) && inboundTag(socks,http) -> direct
domain(full:ads.example.com) -> block
`
	rules, err := parseRoutingARules(text, "my-custom", "proxy", validOutbounds)
	if err != nil {
		t.Fatalf("parseRoutingARules error: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules (2 explicit + 1 fallback), got %d: %+v", len(rules), rules)
	}
	for i, r := range rules {
		if len(r.InboundTag) != 1 || r.InboundTag[0] != "my-custom" {
			t.Fatalf("rule #%d escaped pinned inbound tag: %v", i, r.InboundTag)
		}
	}
	if rules[0].OutboundTag != "direct" {
		t.Fatalf("rule 0 expected direct, got %+v", rules[0])
	}
	if rules[1].OutboundTag != "block" {
		t.Fatalf("rule 1 expected block, got %+v", rules[1])
	}
	if rules[2].BalancerTag != "hkgrp" || rules[2].OutboundTag != "" {
		t.Fatalf("fallback rule expected BalancerTag=hkgrp, got %+v", rules[2])
	}

	// 2. Absent default: falls back to ci.Outbound
	rules2, err := parseRoutingARules(`domain(geosite:cn) -> direct`, "my-custom", "proxy", validOutbounds)
	if err != nil {
		t.Fatalf("parseRoutingARules error: %v", err)
	}
	if last := rules2[len(rules2)-1]; last.OutboundTag != "proxy" || len(last.InboundTag) != 1 || last.InboundTag[0] != "my-custom" {
		t.Fatalf("expected fallback to ci.Outbound=proxy, got %+v", last)
	}

	// 3. Unknown outbound tag in rule rejected cleanly
	if _, err := parseRoutingARules(`domain(geosite:cn) -> nonexistent`, "my-custom", "proxy", validOutbounds); err == nil {
		t.Fatal("expected error for unknown outbound tag in rule")
	}

	// 4. Unknown outbound tag in default: rejected cleanly
	if _, err := parseRoutingARules(`default: nonexistent`, "my-custom", "proxy", validOutbounds); err == nil {
		t.Fatal("expected error for unknown outbound tag in default:")
	}
}

func TestSetWhitelistRoutingUsesFullPrefix(t *testing.T) {
	tmpl := &Template{}
	tmpl.setWhitelistRouting([]Addr{
		{host: "jp.example.com", port: "443"},
		{host: "1.2.3.4", port: "443"},
	})
	if len(tmpl.Routing.Rules) != 2 {
		t.Fatalf("expected 2 whitelist rules, got %d", len(tmpl.Routing.Rules))
	}
	if len(tmpl.Routing.Rules[0].Domain) != 1 || tmpl.Routing.Rules[0].Domain[0] != "full:jp.example.com" {
		t.Fatalf("expected full:jp.example.com, got %v", tmpl.Routing.Rules[0].Domain)
	}
	if len(tmpl.Routing.Rules[1].IP) != 1 || tmpl.Routing.Rules[1].IP[0] != "1.2.3.4" {
		t.Fatalf("expected IP 1.2.3.4, got %v", tmpl.Routing.Rules[1].IP)
	}
}

func TestCheckDuplicatedInboundSocketsWildcardsAndDns(t *testing.T) {
	// 1. 0.0.0.0:P collides with 127.0.0.1:P
	t1 := &Template{
		Inbounds: []coreObj.Inbound{
			{Listen: "0.0.0.0", Port: 20170, Tag: "a"},
			{Listen: "127.0.0.1", Port: 20170, Tag: "b"},
		},
	}
	if err := t1.checkDuplicatedInboundSockets(); err == nil {
		t.Fatal("expected collision between 0.0.0.0:20170 and 127.0.0.1:20170")
	}

	// 2. :::P collides with ::1:P and 127.0.0.1:P
	t2 := &Template{
		Inbounds: []coreObj.Inbound{
			{Listen: "::", Port: 52345, Tag: "a"},
			{Listen: "::1", Port: 52345, Tag: "b"},
		},
	}
	if err := t2.checkDuplicatedInboundSockets(); err == nil {
		t.Fatal("expected collision between [::]:52345 and [::1]:52345")
	}
	t3 := &Template{
		Inbounds: []coreObj.Inbound{
			{Listen: "::", Port: 52345, Tag: "a"},
			{Listen: "127.0.0.1", Port: 52345, Tag: "b"},
		},
	}
	if err := t3.checkDuplicatedInboundSockets(); err == nil {
		t.Fatal("expected collision between [::]:52345 and 127.0.0.1:52345")
	}

	// 3. 127.0.0.1:P and ::1:P do NOT collide (dual-stack loopback twins)
	t4 := &Template{
		Inbounds: []coreObj.Inbound{
			{Listen: "127.0.0.1", Port: 20170, Tag: "a_ipv4"},
			{Listen: "::1", Port: 20170, Tag: "a_ipv6"},
		},
	}
	if err := t4.checkDuplicatedInboundSockets(); err != nil {
		t.Fatalf("unexpected collision between 127.0.0.1 and ::1: %v", err)
	}

	// 4. DNS listen port collides with inbound on same port
	t5 := &Template{
		Inbounds: []coreObj.Inbound{
			{Listen: "127.0.0.1", Port: 52353, Tag: "custom"},
		},
		DnsModuleConfig: json.RawMessage(`{"listener":{"listen_addr":"0.0.0.0:52353"}}`),
	}
	if err := t5.checkDuplicatedInboundSockets(); err == nil {
		t.Fatal("expected collision between inbound 127.0.0.1:52353 and DNS 0.0.0.0:52353")
	}
}

func TestSetAPILeastPingCaseInsensitiveAndObservatoryFilter(t *testing.T) {
	n1 := dnsNode(t, "a.example.com")
	n2 := dnsNode(t, "b.example.com")
	sd := &ServerData{
		ServerInfos: []serverInfo{
			{Info: n1, OutboundName: "lpGroup"},
			{Info: n2, OutboundName: "lpGroup"},
			{Info: n1, OutboundName: "rndGroup"},
			{Info: n2, OutboundName: "rndGroup"},
		},
		OutboundName2ServerObjs: map[string][]serverObj.ServerObj{
			"lpGroup":  {n1, n2},
			"rndGroup": {n1, n2},
		},
		OutboundName2Setting: map[string]configure.OutboundSetting{
			"lpGroup":  {Type: configure.ObservatoryType("LeastPing"), ProbeInterval: "10s", ProbeURL: "https://gstatic.com/generate_204"},
			"rndGroup": {Type: configure.ObservatoryType("random"), ProbeInterval: "10s"},
		},
	}
	tmpl := &Template{
		Variant: where.V2rayaCore,
		Outbounds: []coreObj.OutboundObject{
			{Tag: "n1", Protocol: "vless", Balancers: []string{"lpGroup", "rndGroup"}},
			{Tag: "n2", Protocol: "vless", Balancers: []string{"lpGroup", "rndGroup"}},
			{Tag: "direct", Protocol: "freedom"},
			{Tag: "block", Protocol: "blackhole"},
		},
		OutboundTags: []string{"n1", "n2", "n1", "n2"},
	}
	if _, err := tmpl.SetAPI(sd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmpl.Close() })

	var lpFallback, rndFallback string
	for _, b := range tmpl.Routing.Balancers {
		switch b.Tag {
		case "lpGroup":
			lpFallback = b.FallbackTag
		case "rndGroup":
			rndFallback = b.FallbackTag
		}
	}
	if lpFallback != "block" {
		t.Fatalf("expected FallbackTag=block for case-insensitive LeastPing, got %q", lpFallback)
	}
	if rndFallback != "" {
		t.Fatalf("expected empty FallbackTag for random balancer, got %q", rndFallback)
	}
	if tmpl.MultiObservatory == nil || len(tmpl.MultiObservatory.Observers) != 1 || tmpl.MultiObservatory.Observers[0].Tag != "lpGroup" {
		t.Fatalf("expected MultiObservatory to contain only lpGroup, got %+v", tmpl.MultiObservatory)
	}
}

func TestResolveOutboundsBalancerRecordsServerInfoAndPluginPort(t *testing.T) {
	n1 := dnsNode(t, "node1.example.com")
	n2 := dnsNode(t, "node2.example.com")
	sd := NewServerData([]serverInfo{
		{Info: n1, OutboundName: "grp", PluginPort: 31234},
		{Info: n2, OutboundName: "grp", PluginPort: 31235},
	})
	tmpl := &Template{
		Setting: &configure.Setting{},
	}
	_, tags, err := tmpl.resolveOutbounds(sd)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected 2 outbound tags, got %v", tags)
	}
	for _, tag := range tags {
		info, ok := tmpl.serverInfoMap[tag]
		if !ok || info == nil {
			t.Fatalf("serverInfoMap missing balancer tag %q", tag)
		}
		if info.PluginPort != 31234 && info.PluginPort != 31235 {
			t.Fatalf("unexpected PluginPort for %q: %d", tag, info.PluginPort)
		}
	}
}

func TestTemplateCloseIdempotent(t *testing.T) {
	var calls atomic.Int32
	tmpl := &Template{
		ApiCloses: []func(){
			func() { calls.Add(1) },
			func() { calls.Add(1) },
		},
	}
	_ = tmpl.Close()
	_ = tmpl.Close()
	if calls.Load() != 2 {
		t.Fatalf("expected ApiCloses called exactly once each (total 2), got %d", calls.Load())
	}
}

func TestParseConnectionLineDetoursAndEmail(t *testing.T) {
	cases := []struct {
		line     string
		inbound  string
		outbound string
		dest     string
		network  string
	}{
		{
			line:     "from tcp:127.0.0.1:43496 accepted tcp:www.gstatic.com:80 [socks -> proxy]",
			inbound:  "socks",
			outbound: "proxy",
			dest:     "tcp:www.gstatic.com:80",
			network:  "tcp",
		},
		{
			line:     "from tcp:127.0.0.1:43496 accepted tcp:example.com:443 [transparent_ipv4 >> direct]",
			inbound:  "transparent_ipv4",
			outbound: "direct",
			dest:     "tcp:example.com:443",
			network:  "tcp",
		},
		{
			line:     "from udp:127.0.0.1:51234 accepted udp:1.1.1.1:53 [socks ==> proxy] email: alice@example.com",
			inbound:  "socks",
			outbound: "proxy",
			dest:     "udp:1.1.1.1:53",
			network:  "udp",
		},
	}
	for _, tc := range cases {
		ev, ok := parseConnectionLine(tc.line)
		if !ok {
			t.Fatalf("failed to parse %q", tc.line)
		}
		if ev.Inbound != tc.inbound || ev.Outbound != tc.outbound || ev.Dest != tc.dest || ev.Network != tc.network {
			t.Fatalf("line %q: got %+v, want inbound=%q outbound=%q dest=%q net=%q",
				tc.line, ev, tc.inbound, tc.outbound, tc.dest, tc.network)
		}
	}
}

func TestProcessLookupWhichIndexSharedNodes(t *testing.T) {
	p := &Process{
		tag2WhichIndex: map[string]int{
			"『nodeA』": 2,
		},
		groupTag2WhichIndex: map[string]int{
			"group1\x00『nodeA』": 0,
			"group2\x00『nodeA』": 2,
		},
	}
	if idx, ok := p.lookupWhichIndex("group1", "『nodeA』"); !ok || idx != 0 {
		t.Fatalf("expected (0, true) for group1, got (%d, %v)", idx, ok)
	}
	if idx, ok := p.lookupWhichIndex("group2", "『nodeA』"); !ok || idx != 2 {
		t.Fatalf("expected (2, true) for group2, got (%d, %v)", idx, ok)
	}
	if _, ok := p.lookupWhichIndex("group1", "unknown-tag"); ok {
		t.Fatal("expected ok=false for unknown tag")
	}
}

func TestSharedBalancerMembersHaveIndependentObservations(t *testing.T) {
	shared := dnsNode(t, "shared.example")
	second := dnsNode(t, "other.example")
	sd := NewServerData([]serverInfo{
		{Info: shared, OutboundName: "east"}, {Info: second, OutboundName: "east"},
		{Info: shared, OutboundName: "west"}, {Info: second, OutboundName: "west"},
		{Info: shared, OutboundName: "east"},
	})
	tmpl := &Template{}
	_, tags, err := tmpl.resolveOutbounds(sd)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, tag := range tags {
		if seen[tag] {
			t.Fatalf("shared observer tag %q in %v", tag, tags)
		}
		seen[tag] = true
	}
}

func TestDNSUpstreamTransportDefaults(t *testing.T) {
	for input, want := range map[string]string{
		"quic://resolver.example": "resolver.example:853", "dot://[2001:db8::1]": "[2001:db8::1]:853",
		"udp://[2001:db8::2]": "[2001:db8::2]:53", "tcp://resolver.example:1053": "resolver.example:1053",
	} {
		_, addr, _, err := parseDNSUpstream(input)
		if err != nil || addr != want {
			t.Errorf("parse %q: got %q, %v", input, addr, err)
		}
	}
}

func TestDNSRuleForEmptyGroupBlocksBothDispatchPaths(t *testing.T) {
	tmpl := &Template{Setting: &configure.Setting{Transparent: configure.TransparentClose}, ProbePorts: map[string]int{"active": 4567}}
	rules := []configure.DnsRule{
		{RuleID: "empty-group", Domain: "example.org", Upstream: "192.0.2.1", Outbound: "empty"},
		{RuleID: "active-group", Upstream: "192.0.2.2", Outbound: "active"},
	}
	if err := tmpl.generateDnsModuleConfigWithRules(nil, rules); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		ProxyMap map[string]string `json:"proxy_map"`
	}
	if err := json.Unmarshal(tmpl.DnsModuleConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if addr, ok := cfg.ProxyMap["empty"]; !ok || addr != "" || cfg.ProxyMap["active"] != "127.0.0.1:4567" {
		t.Fatalf("unexpected group SOCKS addresses: %v", cfg.ProxyMap)
	}
	if len(tmpl.internalRoutes) != 1 || tmpl.internalRoutes[0].OutboundTag != "block" ||
		len(tmpl.internalRoutes[0].InboundTag) != 1 || tmpl.internalRoutes[0].InboundTag[0] != internalGroupTag("dns", "empty") {
		t.Fatalf("missing DNS block route: %v", tmpl.internalRoutes)
	}
}

func TestConfiguredPluginManagerRunsUntilTemplateCloses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell plugin fixture")
	}
	env := conf.GetEnvironmentConfig()
	orig := env.PluginManager
	t.Cleanup(func() { env.PluginManager = orig })
	dir := t.TempDir()
	env.PluginManager = filepath.Join(dir, "plugin")
	argsFile := filepath.Join(dir, "arguments")
	t.Setenv("V2RAYA_TEST_PLUGIN_ARGS", argsFile)
	if err := os.WriteFile(env.PluginManager, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$V2RAYA_TEST_PLUGIN_ARGS\"\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmpl := &Template{pluginManagers: []pluginManagerNode{{link: "example://node", port: 1234}}}
	defer tmpl.Close()
	if err := tmpl.startPluginManagers(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(argsFile)
		if strings.Contains(string(data), "--stage=run\n--link=example://node\n--port=1234\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin arguments: %q", data)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := tmpl.Close(); err != nil {
		t.Fatal(err)
	}
}
