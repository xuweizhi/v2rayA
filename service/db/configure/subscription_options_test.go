package configure

import (
	"reflect"
	"testing"
)

func TestNormalizeNodeDNS(t *testing.T) {
	got, err := NormalizeNodeDNS([]string{"1.1.1.1", "udp://1.1.1.1:53", "2001:4860:4860::8888", "tls://dns.google", "https://dns.google/dns-query"})
	want := []string{"udp://1.1.1.1:53", "udp://[2001:4860:4860::8888]:53", "tls://dns.google:853", "https://dns.google:443/dns-query"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, address := range []string{"file:///etc/hosts", "udp://1.1.1.1:70000", "https://user:pass@dns.google/query", "udp://1.1.1.1/path", "https://"} {
		if _, err := NormalizeNodeDNS([]string{address}); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

func TestPreProxyGraph(t *testing.T) {
	settings := map[string]OutboundSetting{"landing": {PreProxy: "middle"}, "middle": {PreProxy: "front"}, "front": {}}
	if err := ValidatePreProxies(settings); err != nil {
		t.Fatal(err)
	}
	settings["front"] = OutboundSetting{PreProxy: "landing"}
	if err := ValidatePreProxies(settings); err == nil {
		t.Fatal("accepted transitive cycle")
	}
	settings["front"] = OutboundSetting{PreProxy: "missing"}
	if err := ValidatePreProxies(settings); err == nil {
		t.Fatal("accepted missing group")
	}
	settings["front"] = OutboundSetting{PreProxy: "front"}
	if err := ValidatePreProxies(settings); err == nil {
		t.Fatal("accepted self cycle")
	}
}
