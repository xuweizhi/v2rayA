package conf

import (
	multiobs "github.com/v2rayA/v2raya-core/hint/app/observatory/multiobservatory"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/cmdarg"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/confloader/external"
)

func TestOutboundOrder(t *testing.T) {
	const base = `{"outbounds":[
		{"tag":"first","protocol":"mieru","streamSettings":{"sockopt":{"mark":128}}},
		{"tag":"landing","protocol":"freedom"},
		{"tag":"last","protocol":"tuic"}]}`
	check := func(config *core.Config, err error, wantTags []string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, ob := range config.Outbound {
			got = append(got, ob.Tag)
		}
		if !slices.Equal(got, wantTags) {
			t.Fatalf("outbound order %v, want %v", got, wantTags)
		}
	}
	config, err := core.LoadConfig("json", strings.NewReader(base))
	check(config, err, []string{"first", "landing", "last"})
	sender, err := config.Outbound[0].SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if mark := sender.(*proxyman.SenderConfig).GetStreamSettings().GetSocketSettings().GetMark(); mark != 128 {
		t.Fatalf("custom outbound socket mark = %d, want 128", mark)
	}

	dir := t.TempDir()
	baseFile := filepath.Join(dir, "base.json")
	overlay := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(baseFile, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	// Xray prepends new tags and replaces existing tags in place.
	const updates = `{"outbounds":[{"tag":"new","protocol":"anytls"},
		{"tag":"first","protocol":"freedom"},{"tag":"landing","protocol":"mieru"}]}`
	if err := os.WriteFile(overlay, []byte(updates), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = core.LoadConfig("auto", cmdarg.Arg{baseFile, overlay})
	check(config, err, []string{"new", "first", "landing", "last"})
	if !strings.Contains(config.Outbound[2].ProxySettings.Type, "mieru") {
		t.Fatalf("replacement outbound has type %q", config.Outbound[2].ProxySettings.Type)
	}
}

func TestCustomSenderAndJSONCObservatory(t *testing.T) {
	const source = `{
		// Both the standard and extension decoders accept comments.
		"outbounds":[{"tag":"custom","protocol":"anytls","sendThrough":"127.0.0.1",
			"targetStrategy":"UseIPv4","mux":{"enabled":true,"concurrency":2},
			"proxySettings":{"tag":"upstream"}}],
		"burstObservatory":{"subjectSelector":["custom"],"pingConfig":{
			"destination":"https://example.org","timeout":"3s","samplingCount":2,"httpMethod":"HEAD"}}
	}`
	cfg, err := core.LoadConfig("json", strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cfg.Outbound[0].SenderSettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	s := msg.(*proxyman.SenderConfig)
	if !s.GetMultiplexSettings().GetEnabled() || s.GetProxySettings().GetTag() != "upstream" ||
		s.GetTargetStrategy().String() != "USE_IP4" || len(s.GetVia().GetIp()) == 0 {
		t.Fatalf("incomplete sender settings: %v", s)
	}
	var observerCount int
	for _, app := range cfg.App {
		if strings.Contains(app.Type, ".observatory.") {
			observerCount++
			message, err := app.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			config, ok := message.(*multiobs.Config)
			if !ok || len(config.GetObservers()) != 1 {
				t.Fatalf("unexpected observatory %v", message)
			}
			obs := config.GetObservers()[0]
			if obs.GetTimeout() != int64(3*time.Second) || obs.GetHttpMethod() != "HEAD" || obs.GetSamplingCount() != 2 {
				t.Fatalf("lost burst settings: %v", obs)
			}
		}
	}
	if observerCount != 1 {
		t.Fatalf("built %d observatories", observerCount)
	}
}
