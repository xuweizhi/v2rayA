package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/touch"
)

func TestEditAgeCredentialFromOverview(t *testing.T) {
	resetReviewDB(t)
	initial := &configure.SubscriptionRaw{
		Address: "https://sub.example", DecryptPassword: "independent AES password",
		Extra: configure.SubscriptionExtra{AgePassword: "original AGE passphrase"},
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{initial}); err != nil {
		t.Fatal(err)
	}
	check := func(want string) touch.Subscription {
		t.Helper()
		sub := touch.GenerateTouch().Subscriptions[0]
		payload, err := json.Marshal(sub)
		if err != nil || strings.Contains(string(payload), `"agePassword"`) || sub.HasAgePassword != (want != "") {
			t.Fatal("unexpected public credential fields")
		}
		saved := configure.GetSubscription(0)
		if saved.Extra.AgePassword != want || saved.DecryptPassword != initial.DecryptPassword {
			t.Fatal("incorrect stored credentials")
		}
		return sub
	}
	sub := check(initial.Extra.AgePassword)
	sub.Remarks = "new label"
	for _, want := range []string{initial.Extra.AgePassword, "new AGE passphrase", ""} {
		if want != initial.Extra.AgePassword {
			sub.AgePassword = &want
		}
		if err := ModifySubscriptionRemark(sub); err != nil {
			t.Fatal(err)
		}
		sub = check(want)
	}
}

func TestAgeSubscriptionImportRefreshAndFailedKeyRotation(t *testing.T) {
	resetReviewDB(t)
	read := func(file string) []byte {
		t.Helper()
		b, err := os.ReadFile("../../common/testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	key := strings.TrimSpace(string(read("mihomo-x25519.identity")))
	var ciphertext atomic.Value
	ciphertext.Store(read("mihomo-x25519.age"))
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), key) {
			t.Error("subscription URL leaked the age identity")
		}
		for _, values := range r.Header {
			if strings.Contains(strings.Join(values, " "), key) {
				t.Error("request header leaked the age identity")
			}
		}
		w.Header().Set("Subscription-Userinfo", "total=1024")
		_, _ = w.Write(ciphertext.Load().([]byte))
	}))
	defer source.Close()
	req := ImportRequest{URL: source.URL + "/subscription", Password: "separate AES password"}
	if _, err := ImportProfile(req); err == nil || len(configure.GetSubscriptions()) != 0 {
		t.Fatal("import without an age identity should fail without creating a subscription")
	}
	req.AgePassword = key
	if _, err := ImportProfile(req); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSubscription(0, false); err != nil {
		t.Fatal(err)
	}
	sub := configure.GetSubscription(0)
	if sub == nil || sub.Extra.AgePassword != key || sub.DecryptPassword != req.Password ||
		sub.Extra.Total != 1024 || len(sub.Servers) != 1 || sub.Servers[0].ServerObj.GetName() != "AGE-node" {
		t.Fatal("import or refresh failed to persist the decrypted subscription")
	}
	ciphertext.Store(read("mihomo-pq.age"))
	if err := UpdateSubscription(0, false); err == nil || strings.Contains(err.Error(), key) {
		t.Fatal("refresh with a rotated key should fail without exposing the stored identity")
	}
	after := configure.GetSubscription(0)
	if after == nil || len(after.Servers) != 1 || after.Servers[0].ServerObj.GetName() != "AGE-node" ||
		after.Info != sub.Info || after.Status != sub.Status || after.Extra.LastUpdateTime != sub.Extra.LastUpdateTime ||
		after.Extra.Total != sub.Extra.Total || after.Extra.AgePassword != key {
		t.Fatal("failed refresh modified the previous subscription")
	}
}
