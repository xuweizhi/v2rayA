package configure

import (
	"reflect"
	"testing"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
)

func setupConfigureTestDB(t *testing.T) {
	t.Helper()
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = db.Close() })
	if err := InitDefaultOutbound(); err != nil {
		t.Fatal(err)
	}
}

func TestWhichesSortSameTypeReverse(t *testing.T) {
	ws := NewWhiches([]*Which{
		{TYPE: ServerType, ID: 1},
		{TYPE: SubscriptionType, ID: 1},
		{TYPE: SubscriptionType, ID: 2},
	})
	ws.SortSameTypeReverse()
	got := ws.Get()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].TYPE != ServerType || got[0].ID != 1 ||
		got[1].TYPE != SubscriptionType || got[1].ID != 2 ||
		got[2].TYPE != SubscriptionType || got[2].ID != 1 {
		t.Fatalf("unexpected order: %+v %+v %+v", got[0], got[1], got[2])
	}

	ws3 := NewWhiches([]*Which{
		{TYPE: ServerType, ID: 1},
		{TYPE: ServerType, ID: 3},
		{TYPE: SubscriptionType, ID: 1},
		{TYPE: SubscriptionType, ID: 4},
		{TYPE: SubscriptionServerType, Sub: 0, ID: 1},
		{TYPE: SubscriptionServerType, Sub: 0, ID: 2},
	})
	ws3.SortSameTypeReverse()
	g3 := ws3.Get()
	if g3[0].ID != 3 || g3[1].ID != 1 || g3[2].ID != 4 || g3[3].ID != 1 || g3[4].ID != 2 || g3[5].ID != 1 {
		t.Fatalf("unexpected 3-group reverse order: %+v", g3)
	}
}

func TestAddOutboundValidationAndCaseInsensitiveUniqueness(t *testing.T) {
	setupConfigureTestDB(t)
	for _, invalid := range []string{"", "   ", "proxy", "Proxy", "DIRECT", "Block", "v2raya-internal", "V2RAYA-test"} {
		if err := AddOutbound(invalid); err == nil {
			t.Errorf("AddOutbound(%q) succeeded, want error", invalid)
		}
	}
	if err := AddOutbound("  Asia  "); err != nil {
		t.Fatalf("AddOutbound trimmed valid name failed: %v", err)
	}
	if err := AddOutbound("asia"); err == nil {
		t.Fatal("AddOutbound allowed case-insensitive duplicate")
	}
	found := false
	for _, ob := range GetOutbounds() {
		if ob == "Asia" {
			found = true
		}
	}
	if !found {
		t.Fatalf("trimmed outbound 'Asia' not stored: %v", GetOutbounds())
	}
}

func TestValidateOutboundSettingRequiresPositiveProbeInterval(t *testing.T) {
	setupConfigureTestDB(t)
	for _, interval := range []string{"0s", "-10s", "invalid"} {
		s := DefaultOutboundSetting()
		s.ProbeInterval = interval
		if err := ValidateOutboundSetting("proxy", s); err == nil {
			t.Errorf("ProbeInterval=%q accepted, want error", interval)
		}
	}
	s := DefaultOutboundSetting()
	s.ProbeInterval = "30s"
	if err := ValidateOutboundSetting("proxy", s); err != nil {
		t.Fatalf("valid ProbeInterval rejected: %v", err)
	}
}

func TestGetLenSubscriptionServersOutOfRangeDoesNotCrash(t *testing.T) {
	setupConfigureTestDB(t)
	if n := GetLenSubscriptionServers(99); n != 0 {
		t.Fatalf("GetLenSubscriptionServers(99) = %d, want 0", n)
	}
}

func TestGetSettingNotNilTproxyExcludedInterfaces(t *testing.T) {
	setupConfigureTestDB(t)
	// Legacy JSON without tproxyExcludedInterfaces should get the default value.
	if _, err := db.GetDB().Exec(`INSERT OR REPLACE INTO system_config(key, value) VALUES ('system:setting', '{"logLevel":"info"}')`); err != nil {
		t.Fatal(err)
	}
	s := GetSettingNotNil()
	if s.TproxyExcludedInterfaces != NewSetting().TproxyExcludedInterfaces {
		t.Fatalf("missing field lost default: got %q want %q", s.TproxyExcludedInterfaces, NewSetting().TproxyExcludedInterfaces)
	}
	// Explicitly empty string in JSON should stay empty.
	if _, err := db.GetDB().Exec(`INSERT OR REPLACE INTO system_config(key, value) VALUES ('system:setting', '{"logLevel":"info","tproxyExcludedInterfaces":""}')`); err != nil {
		t.Fatal(err)
	}
	s = GetSettingNotNil()
	if s.TproxyExcludedInterfaces != "" {
		t.Fatalf("explicitly cleared field was overwritten: got %q", s.TproxyExcludedInterfaces)
	}
}

func TestSaveLatenciesGuardsNilAndOutOfBounds(t *testing.T) {
	setupConfigureTestDB(t)
	ws := NewWhiches([]*Which{
		{TYPE: SubscriptionServerType, Sub: 5, ID: 1, Latency: "10ms"},
	})
	if err := ws.SaveLatencies(); err != nil {
		t.Fatalf("SaveLatencies returned unexpected error: %v", err)
	}
}

func TestSubscriptionExtraDownloadStrategyValidation(t *testing.T) {
	for _, valid := range []string{"", "onlyDirect", "onlyProxy", "preferDirect", "preferProxy"} {
		e := SubscriptionExtra{DownloadStrategy: valid}
		if err := e.Validate(); err != nil {
			t.Errorf("valid strategy %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"auto", "direct", "proxy", "pac", "invalid"} {
		e := SubscriptionExtra{DownloadStrategy: invalid}
		if err := e.Validate(); err == nil {
			t.Errorf("invalid strategy %q accepted", invalid)
		}
	}
}

func TestMigrateDnsRulesUniqueRuleIDs(t *testing.T) {
	rules := []DnsRule{
		{Server: "1.1.1.1"},
		{Server: "8.8.8.8"},
		{ID: 42, Server: "9.9.9.9"},
	}
	migrated := MigrateDnsRules(rules)
	if migrated[0].RuleID != "rule-0" || migrated[1].RuleID != "rule-1" || migrated[2].RuleID != "rule-42" {
		t.Fatalf("unexpected migrated RuleIDs: %q, %q, %q", migrated[0].RuleID, migrated[1].RuleID, migrated[2].RuleID)
	}
}

func TestGetDnsRulesNotNilReturnsClonedDefault(t *testing.T) {
	setupConfigureTestDB(t)
	rules := GetDnsRulesNotNil()
	origServer := DefaultDnsRules[0].Server
	rules[0].Server = "mutated.example"
	if DefaultDnsRules[0].Server != origServer {
		t.Fatal("mutating GetDnsRulesNotNil() corrupted global DefaultDnsRules")
	}
}

func TestPortWhiteListCompressedBoundsCheck(t *testing.T) {
	pwl := &PortWhiteList{
		TCP: []string{"80", "1:70000", "70000:80000", "99999999999999999999999:100", "443:100"},
		UDP: []string{"65530:65540"},
	}
	got := pwl.Compressed()
	wantTCP := []string{"1:65535"}
	wantUDP := []string{"65530:65535"}
	if !reflect.DeepEqual(got.TCP, wantTCP) || !reflect.DeepEqual(got.UDP, wantUDP) {
		t.Fatalf("Compressed() = TCP:%v UDP:%v, want TCP:%v UDP:%v", got.TCP, got.UDP, wantTCP, wantUDP)
	}
}

func TestRemovingGroupPreservesColonNamedSibling(t *testing.T) {
	setupConfigureTestDB(t)
	for _, name := range []string{"region", "region:east"} {
		if err := AddOutbound(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveOutbound("region"); err != nil {
		t.Fatal(err)
	}
	if !db.Exists("outbound.region:east", "setting") {
		t.Fatal("removing region erased the sibling group")
	}
}
