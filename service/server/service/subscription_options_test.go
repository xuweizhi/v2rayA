package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

func TestSubscriptionFiltersCombineAttributes(t *testing.T) {
	nodes := []serverObj.ServerObj{}
	for _, link := range []string{
		"vless://00000000-0000-0000-0000-000000000001@hk.example:443#HongKong",
		"vless://00000000-0000-0000-0000-000000000002@us.example:443#USA",
		"trojan://password@hk.example:443#HongKong-Trojan",
	} {
		obj, err := ResolveURL(link)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, obj)
	}
	extra := configure.SubscriptionExtra{IncludeProtocols: []string{"vless"}, FilterField: "host", IncludeRegex: `^hk\.`}
	filtered := filterServersByExtra(nodes, extra)
	if len(filtered) != 1 || filtered[0].GetName() != "HongKong" {
		t.Fatalf("unexpected filter: %v", filtered)
	}
	extra.ExcludeRegex = "hk"
	if len(filterServersByExtra(nodes, extra)) != 0 {
		t.Fatal("exclude was ignored")
	}
	extra.ExcludeRegex = ""
	extra.RemovedTags = []string{"HongKong"}
	if len(filterServersByExtra(nodes, extra)) != 0 {
		t.Fatal("removed node was restored")
	}
	extra.IncludeRegex = "["
	if err := extra.Validate(); err == nil {
		t.Fatal("invalid expression accepted")
	}
}

func TestFetchSubscriptionPreservesPrivateDNS(t *testing.T) {
	profile := `dns:
  nameserver: [9.9.9.9]
  proxy-server-nameserver: [1.1.1.1, 'tls://dns.google']
proxies:
  - name: Test
    type: vless
    server: node.example
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
`
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(profile)), Request: r}, nil
	})}
	result, err := ResolveSubscriptionWithClient("https://provider.example/sub", client, "", configure.SubscriptionExtra{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.infos) != 1 || len(result.nodeDNS) != 2 || result.nodeDNS[0] != "udp://1.1.1.1:53" {
		t.Fatalf("unexpected fetch result: %+v", result)
	}
}

func TestSubscriptionDownloadStrategyRoutes(t *testing.T) {
	resetReviewDB(t)
	if err := configure.SetPorts(&configure.Ports{Socks5: 1080}); err != nil {
		t.Fatal(err)
	}
	system := &http.Client{Timeout: time.Second, Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { panic("system route used") })}
	for _, strategy := range []string{"", "onlyDirect", "onlyProxy", "preferDirect", "preferProxy"} {
		clients, err := subscriptionDownloadClients(system, configure.SubscriptionExtra{DownloadStrategy: strategy})
		if err != nil {
			t.Fatal(err)
		}
		if strategy == "" {
			if len(clients) != 1 || clients[0] != system {
				t.Fatal("system route changed")
			}
			continue
		}
		expected := []bool{strategy == "onlyProxy" || strategy == "preferProxy"}
		if strings.HasPrefix(strategy, "prefer") {
			expected = append(expected, !expected[0])
		}
		if len(clients) != len(expected) {
			t.Fatalf("%s: wrong fallback count", strategy)
		}
		for i, client := range clients {
			transport, ok := client.Transport.(*http.Transport)
			if !ok || (transport.Proxy != nil) != expected[i] || client.Timeout != system.Timeout {
				t.Fatalf("%s: wrong route at %d", strategy, i)
			}
		}
	}
}

func TestSubscriptionRefreshPreservesOldNodesOnFetchFailure(t *testing.T) {
	resetReviewDB(t)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "/unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(w, "no subscription available")
	}))
	defer source.Close()
	for i, path := range []string{"/forbidden", "/unavailable", "/invalid"} {
		sub := &configure.SubscriptionRaw{
			Address: source.URL + path, Status: "old", Info: "existing quota",
			Servers: []configure.ServerRaw{reviewNode(t, "known")},
			Extra:   configure.SubscriptionExtra{LastUpdateTime: 1234},
		}
		if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
			t.Fatal(err)
		}
		if err := UpdateSubscription(i, false); err == nil {
			t.Fatalf("%s: expected refresh to fail", path)
		}
		got := configure.GetSubscription(i)
		if got == nil || len(got.Servers) != 1 || got.Servers[0].ServerObj.GetName() != "known" ||
			got.Info != sub.Info || got.Status != sub.Status || got.Extra.LastUpdateTime != sub.Extra.LastUpdateTime {
			t.Fatalf("%s: failed refresh changed subscription: %+v", path, got)
		}
	}
}

func TestSubscriptionImportAndRefreshSharedEndpoint(t *testing.T) {
	resetReviewDB(t)
	first := "vless://00000000-0000-0000-0000-000000000001@shared.example:443#one"
	second := "vless://00000000-0000-0000-0000-000000000002@shared.example:443#two"
	var body atomic.Value
	body.Store(strings.Join([]string{first, second, first}, "\n"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body.Load().(string))
	}))
	defer provider.Close()
	if err := Import(provider.URL+"/sub", nil, ""); err != nil {
		t.Fatal(err)
	}
	checkNodes := func() {
		t.Helper()
		sub := configure.GetSubscription(0)
		if sub == nil || len(sub.Servers) != 2 || sub.Servers[0].ServerObj.GetName() != "one" || sub.Servers[1].ServerObj.GetName() != "two" {
			t.Fatalf("unexpected shared-endpoint subscription: %+v", sub)
		}
	}
	checkNodes()
	if err := UpdateSubscription(0, false); err != nil {
		t.Fatal(err)
	}
	checkNodes()
	if err := configure.AddConnect(configure.Which{TYPE: configure.SubscriptionServerType, Sub: 0, ID: 1, Outbound: "proxy"}); err != nil {
		t.Fatal(err)
	}
	body.Store(second)
	if err := UpdateSubscription(0, false); err != nil {
		t.Fatal(err)
	}
	connections := configure.GetConnectedServers().Get()
	if len(connections) != 1 {
		t.Fatalf("unexpected connected nodes: %+v", connections)
	}
	node, err := connections[0].LocateServerRaw()
	if err != nil || node.ServerObj.GetName() != "one" {
		t.Fatalf("shared endpoint changed the connected node: %v, %v", node, err)
	}
}

func TestSubscriptionAcceptsEmptyFilterResult(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("trojan://example@node.example:443#Other"))}, nil
	})}
	result, err := ResolveSubscriptionWithClient("https://provider.example", client, "", configure.SubscriptionExtra{IncludeRegex: "^Desired$"})
	if err != nil || len(result.infos) != 0 {
		t.Fatalf("expected empty filtered subscription, got %v, %v", result.infos, err)
	}
}
