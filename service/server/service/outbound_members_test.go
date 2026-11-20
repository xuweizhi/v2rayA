package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/touch"
)

func TestSubscriptionSourcesReadOnlyMetadata(t *testing.T) {
	resetReviewDB(t)
	sources, err := SubscriptionSources()
	if err != nil || sources == nil || len(sources) != 0 {
		t.Fatalf("empty source list: %v, %v", sources, err)
	}
	for _, sub := range []*configure.SubscriptionRaw{
		{Address: "https://first.example/sub?token=one", Remarks: "First", Servers: []configure.ServerRaw{reviewNode(t, "old")}},
		{Address: "https://second.example/sub?token=two", Remarks: "https://private.example/secret"},
	} {
		if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
			t.Fatal(err)
		}
	}
	// A node from an unavailable plugin must not hide its subscription from the selector.
	if _, err := db.GetDB().Exec(`UPDATE servers SET config_json = '{"serverObj":{"protocol":"missing-plugin"}}'`); err != nil {
		t.Fatal(err)
	}
	sources, err = SubscriptionSources()
	if err != nil || len(sources) != 2 || sources[0].Label != "First" || sources[1].Label != "second.example" || sources[0].ID >= sources[1].ID {
		t.Fatalf("source labels, order or IDs changed: %v, %v", sources, err)
	}
}

func TestOutboundRuleRefreshAndFallback(t *testing.T) {
	resetReviewDB(t)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	require(configure.AddOutbound("asia"))
	links := func(names ...string) string {
		var urls []string
		for _, name := range names {
			urls = append(urls, reviewNode(t, name).ServerObj.ExportToURL())
		}
		return strings.Join(urls, "\n")
	}
	var bodies [2]atomic.Value
	bodies[0].Store(links("hk-one", "us"))
	bodies[1].Store(links("hk-two", "hk-disabled"))
	for i := range bodies {
		body := &bodies[i]
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body.Load().(string))
		}))
		defer h.Close()
		require(configure.AppendSubscriptions([]*configure.SubscriptionRaw{{Address: h.URL + "/hidden?token=secret", Extra: configure.SubscriptionExtra{DisabledTags: []string{"hk-disabled"}}}}))
		require(UpdateSubscription(i, false))
	}
	sources, err := SubscriptionSources()
	require(err)
	if len(sources) != 2 || sources[0].ID <= 0 || sources[0].ID == sources[1].ID || sources[0].Label != "127.0.0.1" {
		t.Fatalf("invalid source choices: %+v", sources)
	}
	setting := configure.DefaultOutboundSetting()
	setting.PreProxy = "proxy"
	setting.ProbeInterval = "47s"
	setting.MemberRule = &configure.OutboundMemberRule{SubscriptionIDs: []int64{sources[0].ID, sources[1].ID}, Mode: "keyword", Pattern: "HK"}
	preview, err := PreviewOutboundMembers("asia", *setting.MemberRule)
	require(err)
	if preview.Matched != 2 || len(preview.Added) != 2 || configure.GetConnectedServersByOutbound("asia").Len() != 0 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	require(ApplyOutboundSetting("asia", setting))
	checkNames := func(group string, want ...string) {
		t.Helper()
		var actual []string
		for _, w := range configure.GetConnectedServersByOutbound(group).Get() {
			n, err := w.LocateServerRaw()
			require(err)
			actual = append(actual, n.ServerObj.GetName())
		}
		if !slices.Equal(actual, want) {
			t.Fatalf("group %s: got %v, want %v", group, actual, want)
		}
	}
	checkNames("asia", "hk-one", "hk-two")
	require(configure.AddConnect(configure.Which{TYPE: configure.SubscriptionServerType, Sub: 0, ID: 2, Outbound: "proxy"}))
	if err := ReplaceOutboundConnections("asia", nil); err == nil {
		t.Fatal("dynamic group accepted manual replacement")
	}
	if err := Disconnect(configure.Which{Outbound: "asia"}, true); err == nil {
		t.Fatal("dynamic group accepted manual clearing")
	}
	bodies[0].Store(links("us", "hk-new"))
	bodies[1].Store(links("jp"))
	results := make(chan error, 2)
	for i := range bodies {
		go func(index int) { results <- UpdateSubscription(index, false) }(i)
	}
	for range bodies {
		require(<-results)
	}
	checkNames("asia", "hk-new")
	checkNames("proxy", "us")
	setting.MemberRule.Mode = "regex"
	setting.MemberRule.Pattern = "^hk-"
	require(ApplyOutboundSetting("asia", setting))
	bodies[0].Store(links("us", "jp-only"))
	require(UpdateSubscription(0, true))
	checkNames("asia", "hk-new")
	if nodes := configure.GetSubscription(0).Servers; len(nodes) != 3 || !nodes[2].Retained {
		t.Fatalf("fallback not recorded")
	}
	preview, err = PreviewOutboundMembers("asia", *setting.MemberRule)
	require(err)
	if preview.Matched != 0 || !preview.KeepPrevious {
		t.Fatalf("stale endpoint became a provider match: %+v", preview)
	}
	// Adding a member in another source must drop the retained former member.
	bodies[1].Store(links("hk-returned"))
	require(UpdateSubscription(1, false))
	checkNames("asia", "hk-returned")
	checkNames("proxy", "us")
	// A rejected SQL write must roll back both the new order and the selection.
	_, err = db.GetDB().Exec("CREATE TRIGGER veto_members BEFORE INSERT ON system_config WHEN NEW.key='outbound.asia:connectedServers' BEGIN SELECT RAISE(ABORT,'membership write failed'); END")
	require(err)
	t.Cleanup(func() { _, _ = db.GetDB().Exec("DROP TRIGGER IF EXISTS veto_members") })
	bodies[1].Store(links("jp", "hk-replacement"))
	if err := UpdateSubscription(1, false); err == nil {
		t.Fatal("refresh ignored SQL error")
	}
	checkNames("asia", "hk-returned")
	if got := configure.GetSubscription(1).Servers; len(got) != 1 || got[0].ServerObj.GetName() != "hk-returned" {
		t.Fatal("failed refresh changed provider nodes")
	}
	setting.MemberRule.Pattern = "["
	if err := ApplyOutboundSetting("asia", setting); err == nil {
		t.Fatal("invalid regular expression accepted")
	}
	if got := configure.GetOutboundSetting("asia"); got.MemberRule.Pattern != "^hk-" || got.PreProxy != "proxy" || got.ProbeInterval != "47s" {
		t.Fatalf("lost previous settings: %+v", got)
	}
	setting.MemberRule = nil
	require(ApplyOutboundSetting("asia", setting))
	checkNames("asia", "hk-returned")
}

func TestOutboundRuleSourceIdentitySurvivesDeletion(t *testing.T) {
	resetReviewDB(t)
	for _, n := range []string{"skip", "hk-kept"} {
		if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{Address: fmt.Sprintf("https://example.test/%s", n), Servers: []configure.ServerRaw{reviewNode(t, n)}}}); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := SubscriptionSources()
	if err != nil {
		t.Fatal(err)
	}
	id := sources[1].ID
	if err := DeleteWhich([]*configure.Which{{TYPE: configure.SubscriptionType, ID: 1}}); err != nil {
		t.Fatal(err)
	}
	rule := configure.OutboundMemberRule{SubscriptionIDs: []int64{id}, Mode: "regex", Pattern: "^hk-"}
	preview, err := PreviewOutboundMembers("proxy", rule)
	if err != nil || preview.Matched != 1 || len(preview.Added) != 1 || !strings.Contains(preview.Added[0], "hk-kept") {
		t.Fatalf("%+v: %v", preview, err)
	}
}

func TestConcurrentRefreshOfSameSourceKeepsNewerNodes(t *testing.T) {
	resetReviewDB(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	older := reviewNode(t, "delayed-result").ServerObj.ExportToURL()
	newer := reviewNode(t, "latest-result").ServerObj.ExportToURL()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := newer
		if requests.Add(1) == 1 {
			close(started)
			<-release
			body = older
		}
		_, _ = io.WriteString(w, body)
	}))
	defer source.Close()
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{Address: source.URL, Servers: []configure.ServerRaw{reviewNode(t, "original")}}}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- UpdateSubscription(0, false) }()
	<-started
	err := UpdateSubscription(0, false)
	close(release)
	delayedErr := <-finished
	if err != nil || delayedErr == nil {
		t.Fatalf("latest: %v, delayed: %v", err, delayedErr)
	}
	if got := configure.GetSubscription(0).Servers; len(got) != 1 || got[0].ServerObj.GetName() != "latest-result" {
		t.Fatal("stale refresh replaced newer nodes")
	}
}

func TestDisabledRuleMembersApplyImmediatelyAndRollback(t *testing.T) {
	resetReviewDB(t)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	first := reviewNode(t, "first")
	second := reviewNode(t, "second")
	first.ServerObj.SetName("hk")
	second.ServerObj.SetName("hk")
	sub := &configure.SubscriptionRaw{Address: "https://source.test/", Servers: []configure.ServerRaw{reviewNode(t, "remove"), first, second}}
	require(configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}))
	require(configure.AddOutbound("manual"))
	for _, id := range []int{2, 3} {
		require(configure.AddConnect(configure.Which{Outbound: "manual", TYPE: configure.SubscriptionServerType, Sub: 0, ID: id}))
	}
	setting := configure.DefaultOutboundSetting()
	setting.MemberRule = &configure.OutboundMemberRule{SubscriptionIDs: []int64{configure.GetSubscription(0).DatabaseID}, Mode: "regex", Pattern: "^hk$"}
	require(ApplyOutboundSetting("proxy", setting))
	patch := touch.Subscription{ID: 1, Address: sub.Address, Extra: configure.SubscriptionExtra{DisabledTags: []string{"hk"}, RemovedTags: []string{"remove"}}}
	_, err := db.GetDB().Exec("CREATE TRIGGER veto_patch BEFORE INSERT ON system_config WHEN NEW.key='outbound.proxy:connectedServers' BEGIN SELECT RAISE(ABORT,'test abort'); END")
	require(err)
	t.Cleanup(func() { _, _ = db.GetDB().Exec("DROP TRIGGER IF EXISTS veto_patch") })
	if err := ModifySubscriptionRemark(patch); err == nil {
		t.Fatal("expected patch to fail")
	}
	if old := configure.GetSubscription(0); len(old.Servers) != 3 || old.Servers[1].Disabled || configure.GetConnectedServersByOutbound("manual").Get()[0].ID != 2 || configure.GetConnectedServersByOutbound("proxy").Len() != 2 {
		t.Fatal("failed transaction changed source or memberships")
	}
	_, err = db.GetDB().Exec("DROP TRIGGER veto_patch")
	require(err)
	require(ModifySubscriptionRemark(patch))
	if got := configure.GetConnectedServersByOutbound("proxy"); got.Len() != 0 {
		t.Fatal("disabled nodes still in automatic group")
	}
	rows := configure.GetSubscription(0).Servers
	if len(rows) != 2 || !rows[0].Disabled || !rows[1].Disabled {
		t.Fatal("patch flags or removal not applied")
	}
	manual := configure.GetConnectedServersByOutbound("manual").Get()
	if len(manual) != 2 || manual[0].ID != 1 || manual[1].ID != 2 || rows[0].ServerObj.GetHostname() == rows[1].ServerObj.GetHostname() {
		t.Fatal("manual group lost distinct same-name nodes during reindex")
	}
	// A prior version could already have stored these flags yet left an
	// affected node selected. Repeating the PATCH must repair that state.
	require(configure.AddConnect(configure.Which{Outbound: "proxy", TYPE: configure.SubscriptionServerType, Sub: 0, ID: 1}))
	require(ModifySubscriptionRemark(patch))
	if configure.GetConnectedServersByOutbound("proxy").Len() != 0 {
		t.Fatal("identical patch retained a previously disabled node")
	}
	patch.Extra.DisabledTags = nil
	require(ModifySubscriptionRemark(patch))
	if got := configure.GetConnectedServersByOutbound("proxy").Get(); len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatal("re-enabled nodes not re-added on patch")
	}
}

func TestDisabledLegacyFallbackAcrossSubscriptions(t *testing.T) {
	resetReviewDB(t)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := reviewNode(t, "new").ServerObj.ExportToURL()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, fresh) }))
	defer provider.Close()
	keep := reviewNode(t, "retained")
	keep.Retained = true
	stale := reviewNode(t, "disabled-legacy")
	named := reviewNode(t, "source-a")
	require(configure.AppendSubscriptions([]*configure.SubscriptionRaw{
		{Address: provider.URL + "/a", Servers: []configure.ServerRaw{named}},
		{Address: provider.URL + "/b", Servers: []configure.ServerRaw{stale, keep}, Extra: configure.SubscriptionExtra{DisabledTags: []string{"disabled-legacy"}}},
	}))
	standalone := reviewNode(t, "manual-old")
	require(configure.AppendServers([]*configure.ServerRaw{&standalone}))
	group := "proxy"
	rule := configure.OutboundMemberRule{SubscriptionIDs: []int64{configure.GetSubscription(0).DatabaseID}, Mode: "keyword", Pattern: "missing"}
	setting := configure.DefaultOutboundSetting()
	setting.MemberRule = &rule
	require(configure.SetOutboundSetting(group, setting))
	bad := configure.Which{TYPE: configure.SubscriptionServerType, Sub: 1, ID: 1, Outbound: group}
	for _, member := range []configure.Which{bad, {TYPE: configure.SubscriptionServerType, Sub: 1, ID: 2, Outbound: group}, {TYPE: configure.ServerType, ID: 1, Outbound: group}} {
		require(configure.AddConnect(member))
	}
	check := func(want ...string) {
		t.Helper()
		var got []string
		for _, w := range configure.GetConnectedServersByOutbound(group).Get() {
			raw, err := w.LocateServerRaw()
			require(err)
			got = append(got, raw.ServerObj.GetName())
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	p, err := PreviewOutboundMembers(group, rule)
	require(err)
	if !p.KeepPrevious || p.Matched != 0 || len(p.Members) != 2 || len(p.Removed) != 1 || !strings.HasSuffix(p.Removed[0], "disabled-legacy") {
		t.Fatalf("incorrect fallback preview: %+v", p)
	}
	check("disabled-legacy", "retained", "manual-old")
	require(ApplyOutboundSetting(group, setting))
	check("retained", "manual-old")
	// Simulate a pre-fix saved selection on restart. A refresh of the
	// selected source must also drop disabled former members from elsewhere.
	require(configure.AddConnect(bad))
	require(UpdateSubscription(0, false))
	check("retained", "manual-old")
	require(configure.AddConnect(bad))
	require(UpdateSubscription(1, true))
	check("retained", "manual-old")
	if nodes := configure.GetSubscription(1).Servers; len(nodes) != 2 || !nodes[1].Retained || nodes[1].ServerObj.GetName() != "retained" {
		t.Fatal("recreated a disabled absent node or lost the enabled fallback")
	}
	// Source B is no longer selected by the rule, but still supplies the
	// retained endpoint. Its explicit disable must take effect immediately.
	require(ModifySubscriptionRemark(touch.Subscription{ID: 2, Address: provider.URL + "/b", Extra: configure.SubscriptionExtra{DisabledTags: []string{"retained"}}}))
	check("manual-old")
}

// An unrelated provider may contain a plugin that is currently unavailable.
// A metadata-only edit must never require decoding that provider's nodes.
func TestSubscriptionMetadataPatchReadsOnlyTarget(t *testing.T) {
	resetReviewDB(t)
	for _, name := range []string{"target", "unrelated"} {
		err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{Address: "https://" + name + ".test/", Servers: []configure.ServerRaw{reviewNode(t, name)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GetDB().Exec("UPDATE servers SET config_json=? WHERE sub_id=?", `{"serverObj":{"protocol":"missing-plugin"}}`, configure.GetSubscription(1).DatabaseID); err != nil {
		t.Fatal(err)
	}
	patch := touch.Subscription{ID: 1, Address: "https://target.test/", Remarks: "edited", Extra: configure.SubscriptionExtra{FavTags: []string{"target"}}}
	if err := ModifySubscriptionRemark(patch); err != nil {
		t.Fatal(err)
	}
	if got := configure.GetSubscription(0); got.Remarks != "edited" || !got.Servers[0].Fav {
		t.Fatal("metadata edits were lost")
	}
}
