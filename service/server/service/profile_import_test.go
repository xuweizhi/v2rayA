package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

func TestImportLinkPreview(t *testing.T) {
	embedded := "https://provider.example/profile?token=a%2Fb%2Bc&outbound-dns=1.1.1.1#inner"
	for _, scheme := range []string{"karing://install-config", "clash://install-config", "sing-box://import-remote-profile"} {
		u := scheme + "?" + url.Values{"url": {embedded}, "name": {"中文 节点"}, "outbound-dns": {"8.8.8.8,8.8.8.8"}}.Encode()
		got, err := ParseImportLink(u)
		if err != nil || got.URL != embedded || got.Remarks != "中文 节点" || !reflect.DeepEqual(got.NodeDNS, []string{"udp://8.8.8.8:53"}) {
			t.Fatalf("preview %s: %+v, %v", scheme, got, err)
		}
	}
	for _, source := range []string{embedded, "sub://" + base64.RawURLEncoding.EncodeToString([]byte(embedded))} {
		got, err := ParseImportLink(source)
		if err != nil || got.URL != embedded || got.Remarks != "inner" || !reflect.DeepEqual(got.NodeDNS, []string{"udp://1.1.1.1:53"}) {
			t.Fatalf("ordinary or legacy link preview: %+v, %v", got, err)
		}
	}
	for _, raw := range []string{"karing://connect?url=https://provider.example", "clash://install-config?url=file:///private", "sing-box://import-remote-profile?url=https://one&url=https://two", "https://provider.example/?outbound-dns=udp%3A%2F%2Fuser%3Asecret%40example.com"} {
		if _, err := ParseImportLink(raw); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("invalid preview: %v", err)
		}
	}
}

func TestSharedImportAcrossEntryPoints(t *testing.T) {
	const profile = "dns:\n  proxy-server-nameserver: [1.1.1.1]\nproxies:\n" +
		"  - &node {type: socks5, server: node.example, port: 1080, name: alpha, username: alice, password: 'p:a?s'}\n" +
		"  - *node\n  - {type: other-secret, password: top-secret}\n  - malformed\n  - {type: socks5, server: node.example, port: 1080, tls: true}\n"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, profile)
	}))
	defer provider.Close()
	encoded := base64.RawStdEncoding.EncodeToString([]byte(profile))
	for _, req := range []ImportRequest{{URL: profile}, {URL: profile, Content: true}, {URL: encoded}, {URL: "karing://install-config?" + url.Values{"url": {provider.URL}, "name": {"saved"}}.Encode()}} {
		resetReviewDB(t)
		report, err := ImportProfile(req)
		if err != nil || report.Imported != 1 || report.Skipped != 4 {
			t.Fatalf("import counts: %+v, %v", report, err)
		}
		why := strings.Join(report.Reasons, "|")
		for _, expected := range []string{"duplicate", "unsupported proxy type", "expected a proxy mapping", "TLS for SOCKS5"} {
			if !strings.Contains(why, expected) {
				t.Errorf("missing %q in %s", expected, why)
			}
		}
		if strings.Contains(why, "secret") || strings.Contains(why, "p:a?s") {
			t.Errorf("credential leaked in report: %s", why)
		}
		var node serverObj.ServerObj
		if strings.HasPrefix(req.URL, "karing:") {
			sub := configure.GetSubscription(0)
			if sub == nil || len(sub.Servers) != 1 || sub.Address != provider.URL || sub.Remarks != "saved" || !sub.Extra.PreferNodeDNS || len(sub.Extra.ProviderNodeDNS) != 1 {
				t.Fatal("subscription metadata missing")
			}
			node = sub.Servers[0].ServerObj
		} else {
			got := configure.GetServers()
			if len(got) != 1 {
				t.Fatalf("got %d standalone nodes", len(got))
			}
			node = got[0].ServerObj
		}
		socks, ok := node.(*serverObj.SOCKS)
		if !ok || socks.Server != "node.example" || socks.Port != 1080 || socks.Name != "alpha" || socks.Username != "alice" || socks.Password != "p:a?s" {
			t.Fatal("SOCKS5 settings changed after persistence")
		}
	}
	resetReviewDB(t)
	name := "edited"
	_, err := ImportProfile(ImportRequest{URL: provider.URL, Remarks: &name, NodeDNS: []string{}})
	sub := configure.GetSubscription(0)
	if err != nil || sub == nil || sub.Remarks != name || sub.Extra.PreferNodeDNS || len(sub.Extra.ProviderNodeDNS) == 0 {
		t.Fatalf("confirmed overrides were not saved: %v", err)
	}
}

func TestProfileTextAndSIP008(t *testing.T) {
	const a = "socks5://one:secret@node.example:1234#A"
	const b = "socks5://two:secret@node.example:1234#A"
	raw := "\ufeff# sample\r\n" + a + "\r\n" + b + "\r\n" + a + "\nunsupported://secret@example.invalid\nno scheme secret\nSTATUS=info\n"
	for _, content := range []string{raw, base64.RawURLEncoding.EncodeToString([]byte(raw))} {
		got, err := parseProfile(content)
		if err != nil || got.report.Imported != 2 || got.report.Skipped != 3 || got.status != "info" || strings.Contains(strings.Join(got.report.Reasons, "|"), "secret") {
			t.Fatalf("text parse: %+v, %v", got.report, err)
		}
	}
	unauth := "socks5://node.example:1080"
	userOnly := "socks5://alice@node.example:1080"
	got, err := parseProfile(unauth + "\n" + userOnly)
	if err != nil || got.report.Imported != 2 {
		t.Fatalf("SOCKS5 identities with an empty password collided: %+v, %v", got.report, err)
	}
	const sip = `{"servers":[{"server":"s.example","server_port":1234,"method":"aes-256-gcm","password":"secret","remarks":"one"},{"password":"secret"},{"server_port":{"password":"secret"}}]}`
	got, err = parseProfile(sip)
	if err != nil || got.report.Imported != 1 || got.report.Skipped != 2 || got.nodes[0].GetName() != "one" || strings.Contains(strings.Join(got.report.Reasons, "|"), "secret") {
		t.Fatalf("SIP008 parse: %+v, %v", got.report, err)
	}
}

func TestRejectedAndFailedImportsDoNotWrite(t *testing.T) {
	resetReviewDB(t)
	for _, test := range []struct{ raw, message string }{
		{"# comment\nunknown://user:secret@node.example", "no valid nodes"},
		{"proxies: []", "no valid nodes"},
		{"{\"outbounds\":[{\"type\":\"socks\"}]}", "sing-box"},
	} {
		_, err := ImportProfile(ImportRequest{URL: test.raw, Content: true})
		if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("rejected import error: %v", err)
		}
	}
	if _, err := db.GetDB().Exec("CREATE TRIGGER import_abort BEFORE INSERT ON servers WHEN NEW.sort=1 BEGIN SELECT RAISE(ABORT, 'injected write failure'); END"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.GetDB().Exec("DROP TRIGGER import_abort"); err != nil {
			t.Error(err)
		}
	})
	const links = "socks5://127.0.0.1:1080#one\nsocks5://127.0.0.2:1080#two"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, links) }))
	defer provider.Close()
	for _, source := range []string{links, provider.URL} {
		if _, err := ImportProfile(ImportRequest{URL: source}); err == nil || !strings.Contains(err.Error(), "injected write failure") {
			t.Fatalf("batch did not encounter injected failure: %v", err)
		}
	}
	for _, table := range []string{"servers", "subscriptions"} {
		var count int
		if err := db.GetDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Errorf("partial writes in %s: %d, %v", table, count, err)
		}
	}
}

func TestImportBatchPositions(t *testing.T) {
	resetReviewDB(t)
	for _, pair := range []string{"ab", "cd"} {
		const node = "socks5://a.example:1080#"
		result, err := ImportProfile(ImportRequest{URL: node + pair[:1] + "\n" + node + pair[1:]})
		if err != nil || result.Imported != 2 {
			t.Fatalf("batch append: %+v, %v", result, err)
		}
	}
	for i, name := range []string{"a", "b", "c", "d"} {
		which := configure.Which{TYPE: configure.ServerType, ID: i + 1}
		got, err := which.LocateServerRaw()
		if err != nil || got == nil || got.ServerObj.GetName() != name {
			t.Errorf("wrong node at position %d", i)
		}
	}
}
