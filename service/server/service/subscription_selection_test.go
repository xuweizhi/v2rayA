package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

func TestScheduledRefreshPreservesUnrelatedMembers(t *testing.T) {
	resetReviewDB(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	var succeed atomic.Bool
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if !succeed.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		io.WriteString(w, "mieru://unconfigured.example:443#invalid")
	}))
	defer h.Close()
	manual := reviewNode(t, "manual")
	must(configure.AppendServers([]*configure.ServerRaw{&manual}))
	src := configure.SubscriptionRaw{AutoSelect: true, Address: h.URL, Servers: []configure.ServerRaw{reviewNode(t, "old")}, Extra: configure.SubscriptionExtra{UpdateIntervalHour: 2, LastUpdateTime: time.Now().Unix()}}
	other := configure.SubscriptionRaw{Address: "https://other.example", Servers: []configure.ServerRaw{reviewNode(t, "other")}}
	must(configure.AppendSubscriptions([]*configure.SubscriptionRaw{&src, &other}))
	manualMember := configure.Which{TYPE: configure.ServerType, ID: 1, Outbound: "proxy"}
	oldMember := configure.Which{TYPE: configure.SubscriptionServerType, Sub: 0, ID: 1, Outbound: "proxy"}
	otherMember := configure.Which{TYPE: configure.SubscriptionServerType, Sub: 1, ID: 1, Outbound: "proxy"}
	before := []*configure.Which{&manualMember, &oldMember, &otherMember}
	for _, m := range before {
		must(configure.AddConnect(*m))
	}
	current := configure.GetSubscription(0)
	members := func() []*configure.Which { return configure.GetConnectedServersByOutbound("proxy").Get() }
	must(UpdateScheduledSubscription(0, current.DatabaseID))
	if requests.Load() != 0 || !sameMembers(before, members()) {
		t.Fatal("not-yet-due subscription changed")
	}
	current.Extra.LastUpdateTime = 0
	must(configure.SetSubscription(0, current))
	if err := UpdateScheduledSubscription(0, current.DatabaseID); err == nil || !sameMembers(before, members()) {
		t.Fatal("failed refresh changed selection")
	}
	succeed.Store(true)
	must(UpdateScheduledSubscription(0, current.DatabaseID))
	if !sameMembers([]*configure.Which{&manualMember, &otherMember}, members()) {
		t.Fatal("successful refresh removed unrelated members or selected an invalid node")
	}
	good := reviewNode(t, "new")
	disabled := reviewNode(t, "disabled")
	disabled.Disabled = true
	retained := reviewNode(t, "retained")
	retained.Retained = true
	result, start := subscriptionSelection(0, configure.NewWhiches(before), []configure.ServerRaw{disabled, good, retained}, configure.SubscriptionExtra{})
	newMember := oldMember
	newMember.ID = 2
	if !start || !sameMembers([]*configure.Which{&manualMember, &newMember, &otherMember}, result.Get()) {
		t.Fatal("new members not inserted at the source's position")
	}
}
func TestSupportCheckValidatesStoredNode(t *testing.T) {
	resetReviewDB(t)
	valid := reviewNode(t, "valid")
	invalid, err := serverObj.NewFromLink("mieru", "mieru://missing.example:443#test")
	if err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendServers([]*configure.ServerRaw{&valid, {ServerObj: invalid}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id    int
		valid bool
	}{{1, true}, {2, false}, {3, false}} {
		supported, err := IsSupported(configure.Which{TYPE: configure.ServerType, ID: tc.id})
		if supported != tc.valid || (err == nil) != tc.valid {
			t.Errorf("node %d: supported=%t, error=%v", tc.id, supported, err)
		}
	}
}
