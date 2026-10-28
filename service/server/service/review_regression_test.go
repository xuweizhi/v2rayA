package service

import (
	"os"
	"testing"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

// Tests never open the user's database, including when another package helper
// starts reading settings as part of a regression test.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "v2raya-service-test-")
	if err != nil {
		panic(err)
	}
	conf.GetEnvironmentConfig().Config = dir
	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func resetReviewDB(t *testing.T) {
	t.Helper()
	for _, table := range []string{"outbound_connections", "outbound_settings", "outbound_names", "servers", "subscriptions", "system_config"} {
		if _, err := db.GetDB().Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	if err := configure.InitDefaultOutbound(); err != nil {
		t.Fatal(err)
	}
}

func reviewNode(t *testing.T, name string) configure.ServerRaw {
	t.Helper()
	obj, err := serverObj.NewFromLink("vless", "vless://00000000-0000-0000-0000-000000000001@"+name+".example:443#"+name)
	if err != nil {
		t.Fatal(err)
	}
	return configure.ServerRaw{ServerObj: obj}
}

func TestSubscriptionNodeEditIsScoped(t *testing.T) {
	resetReviewDB(t)
	a, b, c := reviewNode(t, "standalone"), reviewNode(t, "sub-one"), reviewNode(t, "sub-two")
	if err := configure.AppendServers([]*configure.ServerRaw{&a}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{Address: "https://one.example", Servers: []configure.ServerRaw{b}}, {Address: "https://two.example", Servers: []configure.ServerRaw{c}}}); err != nil {
		t.Fatal(err)
	}
	changed := reviewNode(t, "changed")
	if err := setImportedServer(configure.Which{TYPE: configure.SubscriptionServerType, Sub: 1, ID: 1}, &changed); err != nil {
		t.Fatal(err)
	}
	if configure.GetServers()[0].ServerObj.GetName() != "standalone" || configure.GetSubscription(0).Servers[0].ServerObj.GetName() != "sub-one" || configure.GetSubscription(1).Servers[0].ServerObj.GetName() != "changed" {
		t.Fatal("edited the wrong node list")
	}
}

func TestDeleteMultipleConnectedNodesKeepsIndices(t *testing.T) {
	resetReviewDB(t)
	for _, name := range []string{"one", "two", "three", "four"} {
		n := reviewNode(t, name)
		if err := configure.AppendServers([]*configure.ServerRaw{&n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := configure.AddOutbound("other"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []configure.Which{{TYPE: configure.ServerType, ID: 1, Outbound: "proxy"}, {TYPE: configure.ServerType, ID: 3, Outbound: "proxy"}, {TYPE: configure.ServerType, ID: 4, Outbound: "other"}} {
		if err := configure.AddConnect(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteWhich([]*configure.Which{{TYPE: configure.ServerType, ID: 1}, {TYPE: configure.ServerType, ID: 3}}); err != nil {
		t.Fatal(err)
	}
	kept := configure.GetServers()
	if len(kept) != 2 || kept[0].ServerObj.GetName() != "two" || kept[1].ServerObj.GetName() != "four" {
		t.Fatalf("wrong retained nodes: %+v", kept)
	}
	connections := configure.GetConnectedServers().Get()
	if len(connections) != 1 || connections[0].ID != 2 || connections[0].Outbound != "other" {
		t.Fatalf("wrong connections: %+v", connections)
	}
}

func TestSubscriptionFlagsCanBeCleared(t *testing.T) {
	n := reviewNode(t, "node")
	n.Disabled = true
	n.Fav = true
	applyServerFlags(&n, configure.SubscriptionExtra{})
	if n.Disabled || n.Fav {
		t.Fatal("removed flags remained set")
	}
}

func TestRollbackRestoresEmptyConnectionGroup(t *testing.T) {
	resetReviewDB(t)
	n := reviewNode(t, "node")
	if err := configure.AppendServers([]*configure.ServerRaw{&n}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AddConnect(configure.Which{TYPE: configure.ServerType, ID: 1, Outbound: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if err := restoreOutboundConnections("proxy", nil); err != nil {
		t.Fatal(err)
	}
	if configure.GetConnectedServers().Len() != 0 {
		t.Fatal("failed first connection remained selected")
	}
}

func TestInvalidPortsDoNotPersist(t *testing.T) {
	resetReviewDB(t)
	for _, ports := range []*configure.Ports{nil, {Socks5: -1}, {Http: 65536}, {Socks5: 1080, Api: configure.ApiPort{Port: 1080}}} {
		if err := SetPorts(ports); err == nil {
			t.Fatalf("invalid ports accepted: %+v", ports)
		}
	}
	if GetPorts().Socks5 != 0 || GetPorts().Http != 0 {
		t.Fatal("invalid ports persisted")
	}
}

func TestInvalidReplacementPreservesSelection(t *testing.T) {
	resetReviewDB(t)
	node := reviewNode(t, "selected")
	if err := configure.AppendServers([]*configure.ServerRaw{&node}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AddConnect(configure.Which{TYPE: configure.ServerType, ID: 1, Outbound: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceOutboundConnections("proxy", []configure.Which{{TYPE: configure.ServerType, ID: 99}}); err == nil {
		t.Fatal("nonexistent node accepted")
	}
	if configure.GetConnectedServersByOutbound("proxy").Get()[0].ID != 1 {
		t.Fatal("selection changed")
	}
	if err := ReplaceOutboundConnections("missing", nil); err == nil {
		t.Fatal("nonexistent group accepted")
	}
}
