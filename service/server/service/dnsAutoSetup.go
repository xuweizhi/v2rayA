package service

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/v2rayA/v2rayA/db/configure"
)

// built-in DNS server lists used by the one-click DNS auto setup.
var directDNSServers = []string{
	"223.5.5.5",       // AliDNS
	"119.29.29.29",    // DNSPod
	"114.114.114.114", // 114DNS
	"180.76.76.76",    // Baidu
	"1.1.1.1",         // Cloudflare
	"8.8.8.8",         // Google
	"9.9.9.9",         // Quad9
	"208.67.222.222",  // OpenDNS
}

var proxyDNSServers = []string{
	"https://dns.alidns.com/dns-query",
	"https://doh.pub/dns-query",
	"https://1.1.1.1/dns-query",
	"https://8.8.8.8/dns-query",
	"https://dns.google/dns-query",
	"https://9.9.9.9/dns-query",
	"https://208.67.222.222/dns-query",
	"tls://1.1.1.1:853",
	"tls://8.8.8.8:853",
}

// dnsTestDomain is resolved during latency measurements.
const dnsTestDomain = "www.gstatic.com"

// DnsLatencyItem is one measured DNS server.
type DnsLatencyItem struct {
	Outbound  string `json:"outbound,omitempty"`
	Path      string `json:"path,omitempty"`
	Server    string `json:"server"`
	LatencyMs int64  `json:"latencyMs"`
	Ok        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// DnsAutoSetupResult is the response of the one-click DNS auto setup.
type DnsAutoSetupResult struct {
	Direct []DnsLatencyItem `json:"direct"`
	Proxy  []DnsLatencyItem `json:"proxy"`
	// Rules is the resulting DNS rule set that has been persisted.
	Rules []configure.DnsRule `json:"rules"`
}

// DnsAutoSetup measures the built-in DNS servers (direct + via the proxy)
// and persists an optimized DNS rule set: a direct UDP resolver for
// geosite:cn, a DoH resolver through the proxy as the fallback.
func DnsAutoSetup(outbound string) (result DnsAutoSetupResult, err error) {
	if outbound == "" {
		outbound = "proxy"
	}
	if outbound == "direct" {
		return result, fmt.Errorf("select a proxy group for proxy-side DNS")
	}
	if _, _, err := dnsProbeRoute(outbound); err != nil {
		return result, err
	}
	result.Direct = measureDirectDNS()
	result.Proxy = measureDNSList(proxyDNSServers, outbound)

	fastestDirect := fastestServer(result.Direct)
	fastestProxy := fastestServer(result.Proxy)

	rules := []configure.DnsRule{
		{Server: "localhost", Domains: "geosite:private", Outbound: "direct"},
	}
	if fastestDirect != "" {
		rules = append(rules, configure.DnsRule{Server: fastestDirect, Domains: "geosite:cn", Outbound: "direct"})
	}
	if fastestProxy != "" {
		rules = append(rules, configure.DnsRule{Server: fastestProxy, Domains: "", Outbound: outbound})
	}
	if fastestDirect == "" || fastestProxy == "" {
		return result, fmt.Errorf("both direct and proxy DNS must pass; existing rules were preserved")
	}
	if err = configure.SetDnsRules(rules); err != nil {
		return result, err
	}
	result.Rules = rules
	return result, nil
}

func fastestServer(items []DnsLatencyItem) string {
	best := ""
	var bestMs int64
	for _, item := range items {
		if !item.Ok {
			continue
		}
		if best == "" || item.LatencyMs < bestMs {
			best, bestMs = item.Server, item.LatencyMs
		}
	}
	return best
}

// measureDirectDNS measures the round-trip time of a DNS A query over UDP
// against each built-in direct DNS server.
func measureDirectDNS() []DnsLatencyItem { return measureDNSList(directDNSServers, "direct") }
func measureDirectDNSList(servers []string, items []DnsLatencyItem) {
	copy(items, measureDNSList(servers, "direct"))
}

// buildDNSQuery builds a minimal DNS A query for the given domain.
func buildDNSQuery(domain string, flags uint16) []byte {
	buf := make([]byte, 0, 12+len(domain)+2+4)
	var id [2]byte
	_, _ = rand.Read(id[:])
	buf = append(buf, id[0], id[1])
	var fl [2]byte
	binary.BigEndian.PutUint16(fl[:], flags)
	buf = append(buf, fl[:]...)
	buf = append(buf, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // QDCOUNT=1, others 0
	for _, label := range strings.Split(domain, ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0x00)                   // root
	buf = append(buf, 0x00, 0x01, 0x00, 0x01) // type A, class IN
	return buf
}
