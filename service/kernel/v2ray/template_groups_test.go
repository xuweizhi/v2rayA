package v2ray

import (
	"testing"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

func TestPreProxyClonesSharedNodeAndKeepsOtherGroupsDirect(t *testing.T) {
	info := dnsNode(t, "landing.example")
	sd := &ServerData{ServerInfos: []serverInfo{{Info: info, OutboundName: "landing"}, {Info: info, OutboundName: "other"}}, OutboundName2Setting: map[string]configure.OutboundSetting{"landing": {PreProxy: "front"}, "other": {}, "front": {}}}
	tmpl := &Template{Outbounds: []coreObj.OutboundObject{{Tag: "shared", Protocol: "vless", Balancers: []string{"landing", "other"}}, {Tag: "front", Protocol: "socks"}}, OutboundTags: []string{"shared", "shared"}, serverInfoMap: map[string]*serverInfo{}}
	if err := tmpl.applyPreProxies(sd); err != nil {
		t.Fatal(err)
	}
	if tmpl.OutboundTags[0] == tmpl.OutboundTags[1] {
		t.Fatal("shared node was not isolated")
	}
	for _, out := range tmpl.Outbounds {
		if out.Tag == "shared" && (len(out.Balancers) != 1 || out.Balancers[0] != "other" || out.StreamSettings != nil) {
			t.Fatal("unrelated group changed")
		}
		if out.Tag == tmpl.OutboundTags[0] && (out.StreamSettings == nil || out.StreamSettings.Sockopt.DialerProxy == "") {
			t.Fatal("transport does not use pre-proxy")
		}
	}
	tmpl.Routing.Rules = tmpl.internalRoutes
	tmpl.setGroupRouting()
	if len(tmpl.Routing.Rules) != 1 || tmpl.Routing.Rules[0].OutboundTag != "front" {
		t.Fatalf("invalid bridge routing: %+v", tmpl.Routing.Rules)
	}
}

func TestPreProxyRejectsUnsupportedProtocol(t *testing.T) {
	sd := &ServerData{ServerInfos: []serverInfo{{Info: dnsNode(t, "landing.example"), OutboundName: "landing"}}, OutboundName2Setting: map[string]configure.OutboundSetting{"landing": {PreProxy: "front"}, "front": {}}}
	tmpl := &Template{Outbounds: []coreObj.OutboundObject{{Tag: "landing", Protocol: "hysteria2"}}, OutboundTags: []string{"landing"}, serverInfoMap: map[string]*serverInfo{}}
	if err := tmpl.applyPreProxies(sd); err == nil {
		t.Fatal("unsupported protocol silently bypassed pre-proxy")
	}
}
