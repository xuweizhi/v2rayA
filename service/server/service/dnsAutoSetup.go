package service

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

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
func DnsAutoSetup() (result DnsAutoSetupResult, err error) {
	result.Direct = measureDirectDNS()
	result.Proxy = measureProxyDNS()

	fastestDirect := fastestServer(result.Direct)
	fastestProxy := fastestServer(result.Proxy)

	rules := []configure.DnsRule{
		{Server: "localhost", Domains: "geosite:private", Outbound: "direct"},
	}
	if fastestDirect != "" {
		rules = append(rules, configure.DnsRule{Server: fastestDirect, Domains: "geosite:cn", Outbound: "direct"})
	}
	if fastestProxy != "" {
		rules = append(rules, configure.DnsRule{Server: fastestProxy, Domains: "", Outbound: "proxy"})
	}
	if len(rules) <= 1 {
		return result, fmt.Errorf("no DNS server is reachable, giving up")
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
func measureDirectDNS() []DnsLatencyItem {
	query := buildDNSQuery(dnsTestDomain, 0x0100) // RD flag
	items := make([]DnsLatencyItem, len(directDNSServers))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, server := range directDNSServers {
		wg.Add(1)
		go func(i int, server string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			conn, err := net.DialTimeout("udp", net.JoinHostPort(server, "53"), 3*time.Second)
			if err != nil {
				items[i] = DnsLatencyItem{Server: server, Error: err.Error()}
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err = conn.Write(query); err != nil {
				items[i] = DnsLatencyItem{Server: server, Error: err.Error()}
				return
			}
			resp := make([]byte, 512)
			n, err := conn.Read(resp)
			elapsed := time.Since(start).Milliseconds()
			if err != nil || n < 12 {
				items[i] = DnsLatencyItem{Server: server, LatencyMs: elapsed, Error: "no response"}
				return
			}
			items[i] = DnsLatencyItem{Server: server, LatencyMs: elapsed, Ok: true}
		}(i, server)
	}
	wg.Wait()
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Ok != items[j].Ok {
			return items[i].Ok
		}
		return items[i].LatencyMs < items[j].LatencyMs
	})
	return items
}

// measureProxyDNS measures the latency of reaching each proxy-side DNS server
// through the local proxy port. DoH servers are queried with a real DNS
// message; DoT servers are probed with a TCP dial through the proxy.
func measureProxyDNS() []DnsLatencyItem {
	ports := configure.GetPortsNotNil()
	proxyClient := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "socks5", Host: fmt.Sprintf("127.0.0.1:%d", ports.Socks5)}),
		},
	}
	query := buildDNSQuery(dnsTestDomain, 0x0100)
	items := make([]DnsLatencyItem, len(proxyDNSServers))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, server := range proxyDNSServers {
		wg.Add(1)
		go func(i int, server string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var elapsed int64
			var err error
			start := time.Now()
			switch {
			case strings.HasPrefix(server, "https://"):
				req, e := http.NewRequest("POST", server, bytes.NewReader(query))
				if e != nil {
					err = e
					break
				}
				req.Header.Set("Content-Type", "application/dns-message")
				req.Header.Set("Accept", "application/dns-message")
				var resp *http.Response
				resp, e = proxyClient.Do(req)
				elapsed = time.Since(start).Milliseconds()
				if e != nil {
					err = e
					break
				}
				defer resp.Body.Close()
				body, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = body
				if e != nil {
					err = e
				} else if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("status %d", resp.StatusCode)
				}
			case strings.HasPrefix(server, "tls://"):
				host := strings.TrimPrefix(server, "tls://")
				conn, e := proxyDialTCP(proxyClient, host)
				elapsed = time.Since(start).Milliseconds()
				if e != nil {
					err = e
				} else {
					conn.Close()
				}
			default:
				err = fmt.Errorf("unsupported scheme")
				elapsed = time.Since(start).Milliseconds()
			}
			item := DnsLatencyItem{Server: server, LatencyMs: elapsed}
			if err != nil {
				item.Error = err.Error()
			} else {
				item.Ok = true
			}
			items[i] = item
		}(i, server)
	}
	wg.Wait()
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Ok != items[j].Ok {
			return items[i].Ok
		}
		return items[i].LatencyMs < items[j].LatencyMs
	})
	return items
}

// proxyDialTCP dials a TCP address through the proxy port using CONNECT.
func proxyDialTCP(client *http.Client, addr string) (net.Conn, error) {
	if p, ok := client.Transport.(*http.Transport); ok && p.Proxy != nil {
		if proxyURL, err := p.Proxy(nil); err == nil && proxyURL != nil {
			host, port, _ := net.SplitHostPort(proxyURL.Host)
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 8*time.Second)
			if err != nil {
				return nil, err
			}
			if err := socks5Connect(conn, addr); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, nil
		}
	}
	return nil, fmt.Errorf("no proxy configured")
}

// socks5Connect performs a SOCKS5 CONNECT handshake on conn for addr.
func socks5Connect(conn net.Conn, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	defer conn.SetDeadline(time.Time{})
	if _, err = conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err = io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		return fmt.Errorf("socks5 negotiation failed: %v", resp)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, []byte(host)...)
	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, uint16(port))
	req = append(req, pb...)
	if _, err = conn.Write(req); err != nil {
		return err
	}
	// Read the reply header; the bound address may be IPv4/domain/IPv6.
	hdr := make([]byte, 4)
	if _, err = io.ReadFull(conn, hdr); err != nil {
		return err
	}
	if hdr[1] != 0x00 {
		return fmt.Errorf("socks5 CONNECT failed: %d", hdr[1])
	}
	addrLen := 0
	switch hdr[3] {
	case 0x01:
		addrLen = 4
	case 0x04:
		addrLen = 16
	case 0x03:
		var l [1]byte
		if _, err = io.ReadFull(conn, l[:]); err != nil {
			return err
		}
		addrLen = int(l[0])
	}
	skip := make([]byte, addrLen+2)
	_, err = io.ReadFull(conn, skip)
	return err
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
