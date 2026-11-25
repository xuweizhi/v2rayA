package serverObj

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

func TestImportedCredentialsAndTransportRoundTrip(t *testing.T) {
	for _, link := range []string{
		// Standard-base64 userinfo contains an unescaped slash.
		"ss://YWVzLTI1Ni1nY206Pz8/@host.example:443?plugin=v2ray-plugin",
		"ss://2022-blake3-aes-256-gcm:a%2Fb%3D@host.example:443",
	} {
		ss, err := ParseSSURL(link)
		if err != nil {
			t.Fatal(err)
		}
		other, err := ParseSSURL(ss.ExportToURL())
		if err != nil || *other != *ss {
			t.Fatalf("SS round trip lost credentials or plugin: %v", err)
		}
	}
	tr, err := ParseTrojanURL("trojan://a:b%3Ac@host.example:443?type=kcp&seed=s%26d")
	if err != nil || tr.Password != "a:b:c" || tr.Path != "s&d" {
		t.Fatalf("Trojan parsing failed: %v", err)
	}
	c, err := tr.Configuration(PriorInfo{Tag: "trojan"})
	if err != nil {
		t.Fatal(err)
	}
	stream := c.CoreOutbound.StreamSettings
	if stream.Network != "mkcp" || stream.FinalMask == nil || len(stream.FinalMask.Udp) != 1 {
		t.Fatalf("missing KCP settings: %v", stream)
	}
	var mask struct{ Value string }
	if err := json.Unmarshal(stream.FinalMask.Udp[0].Settings, &mask); err != nil || mask.Value != tr.Path {
		t.Fatal("lost KCP seed")
	}
	proxy, err := ParseHttpURL("https-proxy://name:pw@[::1]:80#test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(proxy.ExportToURL(), "https-proxy://") {
		t.Fatal("HTTP proxy exported as subscription")
	}
	h, err := proxy.Configuration(PriorInfo{Tag: "http"})
	if err != nil || h.CoreOutbound.Protocol != "http" || h.CoreOutbound.StreamSettings.Security != "tls" || h.CoreOutbound.Settings.Servers.([]coreObj.Server)[0].Users[0].Pass != "pw" {
		t.Fatal("invalid native HTTPS outbound")
	}
}

func TestVlessTransportAliases(t *testing.T) {
	for _, tc := range []struct{ network, canonical, query string }{
		{"raw", "tcp", "&headerType=http&path=%2Fhome"},
		{"splithttp", "xhttp", "&mode=stream-one&path=%2Fhome"},
		{"websocket", "ws", "&path=%2Fhome"},
	} {
		node, err := ParseVlessURL("vless://id@host.example:443?type=" + tc.network + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		node.Net = tc.network // Existing database entries can retain the original spelling.
		cfg, err := node.Configuration(PriorInfo{})
		if err != nil || cfg.CoreOutbound.StreamSettings.Network != tc.canonical {
			t.Fatalf("%s configuration failed: %v", tc.network, err)
		}
		roundTrip, err := ParseVlessURL(node.ExportToURL())
		if err != nil || roundTrip.Net != tc.canonical || roundTrip.Path != "/home" || roundTrip.XHTTPMode != node.XHTTPMode {
			t.Fatalf("%s share link lost transport settings: %v", tc.network, err)
		}
	}
}

func TestCorePinSettingsSurviveShareLinks(t *testing.T) {
	for _, scheme := range []string{"anytls", "tuic"} {
		u := url.URL{Scheme: scheme, Host: "[::1]:443", User: url.UserPassword("id", "p@ss"),
			RawQuery: url.Values{"pinnedPeerCertSha256": {"leaf"}, "verifyPeerCertByName": {"example.org"}}.Encode(), Fragment: "old"}
		node, err := NewFromLink(scheme, u.String())
		if err != nil {
			t.Fatal(err)
		}
		node.SetName("新名称")
		node, err = NewFromLink(scheme, node.ExportToURL())
		if err != nil || node.GetName() != "新名称" {
			t.Fatalf("%s rename: %v", scheme, err)
		}
		c, err := node.Configuration(PriorInfo{Tag: scheme})
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]interface{}
		if err := json.Unmarshal(c.CoreOutbound.Settings.Inlined, &settings); err != nil {
			t.Fatal(err)
		}
		if settings["pinned_peer_cert_sha256"] != "leaf" || settings["verify_peer_cert_by_name"] != "example.org" {
			t.Fatalf("%s certificate settings lost", scheme)
		}
	}
}
