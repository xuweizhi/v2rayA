package v2ray

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
)

// Run with V2RAYA_TEST_CORE pointing at a freshly built custom core. All
// listeners and traffic are local; no live config, proxy or DNS is used.
func TestPreProxyWithRealCore(t *testing.T) {
	bin := os.Getenv("V2RAYA_TEST_CORE")
	if bin == "" {
		t.Skip("set V2RAYA_TEST_CORE for core integration")
	}
	front, frontCalls := chainTestSOCKS(t)
	landing, landingCalls := chainTestSOCKS(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "chain-ok") }))
	defer endpoint.Close()
	outbound := func(tag, address string, groups []string) coreObj.OutboundObject {
		host, port, _ := net.SplitHostPort(address)
		p, _ := net.LookupPort("tcp", port)
		raw, _ := json.Marshal(map[string]interface{}{"servers": []interface{}{map[string]interface{}{"address": host, "port": p}}})
		return coreObj.OutboundObject{Tag: tag, Protocol: "socks", Settings: coreObj.Settings{Inlined: raw}, Balancers: groups}
	}
	node := dnsNode(t, "landing.example")
	sd := &ServerData{ServerInfos: []serverInfo{{Info: node, OutboundName: "landing"}, {Info: node, OutboundName: "other"}}, OutboundName2Setting: map[string]configure.OutboundSetting{"landing": {PreProxy: "front"}, "other": {}, "front": {}}, OutboundName2ServerObjs: map[string][]serverObj.ServerObj{"landing": {node}, "other": {node}, "front": {node}}}
	tmpl := &Template{Outbounds: []coreObj.OutboundObject{outbound("shared", landing, []string{"landing", "other"}), outbound("front", front, nil)}, OutboundTags: []string{"shared", "shared"}, serverInfoMap: map[string]*serverInfo{}}
	if err := tmpl.applyPreProxies(sd); err != nil {
		t.Fatal(err)
	}
	if err := tmpl.addProbeInbounds(sd); err != nil {
		t.Fatal(err)
	}
	defer tmpl.releaseProbeReservations()
	for i, group := range []string{"landing", "other"} {
		tmpl.Routing.Balancers = append(tmpl.Routing.Balancers, coreObj.Balancer{Tag: group, Selector: []string{tmpl.OutboundTags[i]}, Strategy: coreObj.BalancerStrategy{Type: "random"}})
	}
	tmpl.Routing.Rules = tmpl.internalRoutes
	tmpl.setGroupRouting()
	dnsEndpoint := chainTestDNS(t)
	reservation, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dnsAddress := reservation.LocalAddr().String()
	defer reservation.Close()
	tmpl.DnsModuleConfig, _ = json.Marshal(map[string]interface{}{
		"listener":  map[string]interface{}{"listen_addr": dnsAddress, "timeout": 3},
		"upstreams": []interface{}{map[string]interface{}{"id": "selected-group", "addr": dnsEndpoint, "protocol": "tcp", "proxy_tag": "landing"}},
	})
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, tmpl.ToConfigBytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "run", "-c", config)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	tmpl.releaseProbeReservations()
	reservation.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	}()
	for _, group := range []string{"landing", "other"} {
		address := fmt.Sprintf("127.0.0.1:%d", tmpl.ProbePorts[group])
		ready := false
		for n := 0; n < 100; n++ {
			conn, err := net.DialTimeout("tcp", address, 20*time.Millisecond)
			if err == nil {
				conn.Close()
				ready = true
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if !ready {
			t.Fatal("core probe inbound did not start")
		}
		dialer, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Get(endpoint.URL)
		if err != nil {
			t.Fatalf("%s request: %v", group, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "chain-ok" {
			t.Fatalf("%s: %q", group, body)
		}
	}
	if frontCalls.Load() != 1 || landingCalls.Load() != 2 {
		t.Fatalf("wrong chain isolation: front=%d landing=%d", frontCalls.Load(), landingCalls.Load())
	}
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 17, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("node.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	wire, _ := query.Pack()
	conn, err := net.DialTimeout("udp", dnsAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err = conn.Write(wire); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var response dnsmessage.Message
	if err = response.Unpack(buf[:n]); err != nil || !response.Response || response.ID != 17 || len(response.Answers) != 1 {
		t.Fatalf("DNS via group failed: %v %+v", err, response)
	}
	if frontCalls.Load() != 2 || landingCalls.Load() != 3 {
		t.Fatalf("DNS dispatcher bypassed selected group: front=%d landing=%d", frontCalls.Load(), landingCalls.Load())
	}
}

func chainTestDNS(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				size := make([]byte, 2)
				if _, err := io.ReadFull(conn, size); err != nil {
					return
				}
				buf := make([]byte, int(binary.BigEndian.Uint16(size)))
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
				var query dnsmessage.Message
				if err := query.Unpack(buf); err != nil || len(query.Questions) != 1 {
					return
				}
				query.Response = true
				query.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}}}
				wire, err := query.Pack()
				if err != nil {
					return
				}
				binary.BigEndian.PutUint16(size, uint16(len(wire)))
				_, _ = conn.Write(append(size, wire...))
			}()
		}
	}()
	return listener.Addr().String()
}

func chainTestSOCKS(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	calls := new(atomic.Int32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				_, _ = conn.Write([]byte{5, 0})
				h := make([]byte, 4)
				if _, err := io.ReadFull(conn, h); err != nil || h[1] != 1 {
					return
				}
				var host string
				switch h[3] {
				case 1:
					b := make([]byte, 4)
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 4:
					b := make([]byte, 16)
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case 3:
					b := make([]byte, 1)
					if _, err := io.ReadFull(conn, b); err != nil {
						return
					}
					d := make([]byte, int(b[0]))
					if _, err := io.ReadFull(conn, d); err != nil {
						return
					}
					host = string(d)
				default:
					return
				}
				port := make([]byte, 2)
				if _, err := io.ReadFull(conn, port); err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port))), 3*time.Second)
				if err != nil {
					return
				}
				defer target.Close()
				calls.Add(1)
				_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				go func() { _, _ = io.Copy(target, conn); _ = target.Close() }()
				_, _ = io.Copy(conn, target)
			}()
		}
	}()
	return listener.Addr().String(), calls
}
