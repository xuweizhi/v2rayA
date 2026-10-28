package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
)

type dnsProbeDial func(context.Context, string, string) (net.Conn, error)

func dnsProbeRoute(outbound string) (dnsProbeDial, string, error) {
	if outbound == "direct" {
		return httpClient.GetDirectDialer().DialContext, "direct", nil
	}
	if outbound == "" {
		outbound = "proxy"
	}
	tmpl := v2ray.ProcessManager.GetRunningTemplate()
	if tmpl == nil || !v2ray.ProcessManager.ServiceRunning() {
		return nil, "", fmt.Errorf("proxy core is not running")
	}
	port := tmpl.ProbePorts[outbound]
	if port == 0 {
		return nil, "", fmt.Errorf("outbound group %q has no active probe entry; connect its nodes first", outbound)
	}
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), nil, &net.Dialer{Timeout: 8 * time.Second})
	if err != nil {
		return nil, "", err
	}
	return dialer.(proxy.ContextDialer).DialContext, tmpl.ProbePaths[outbound], nil
}

// validateDNSProbe requires an actual answer to the exact question. HTTP 200,
// an open TCP port, SERVFAIL, or an unrelated DNS response is not a passing test.
func validateDNSProbe(query, body []byte) error {
	var sent, received dnsmessage.Message
	if err := sent.Unpack(query); err != nil {
		return err
	}
	if err := received.Unpack(body); err != nil {
		return fmt.Errorf("invalid DNS response: %w", err)
	}
	if received.ID != sent.ID || !received.Response || received.Truncated || received.RCode != dnsmessage.RCodeSuccess {
		return fmt.Errorf("DNS response failed validation (rcode=%v)", received.RCode)
	}
	if len(received.Questions) != 1 || len(sent.Questions) != 1 || received.Questions[0] != sent.Questions[0] {
		return fmt.Errorf("DNS question mismatch")
	}
	for _, answer := range received.Answers {
		if answer.Header.Type == dnsmessage.TypeA {
			return nil
		}
	}
	return fmt.Errorf("DNS response contains no A answer")
}

func exchangeProbeStream(conn net.Conn, query []byte) ([]byte, error) {
	packet := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(packet, uint16(len(query)))
	copy(packet[2:], query)
	if _, err := io.Copy(conn, bytes.NewReader(packet)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	body := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return body, nil
}

func probeDNS(ctx context.Context, server string, throughProxy bool, dial dnsProbeDial) error {
	return probeDNSWithTLS(ctx, server, throughProxy, dial, &tls.Config{MinVersion: tls.VersionTLS12})
}

func probeDNSWithTLS(ctx context.Context, server string, throughProxy bool, dial dnsProbeDial, tlsConfig *tls.Config) error {
	normalized, err := configure.NormalizeNodeDNS([]string{server})
	if err != nil {
		return err
	}
	if len(normalized) != 1 {
		return fmt.Errorf("empty DNS server")
	}
	u, _ := url.Parse(normalized[0])
	query := buildDNSQuery(dnsTestDomain, 0x0100)
	var body []byte
	if u.Scheme == "https" {
		transport := &http.Transport{DialContext: dial, TLSClientConfig: tlsConfig.Clone()}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(query))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if response.StatusCode != 200 || media != "application/dns-message" {
			return fmt.Errorf("invalid DoH response: status=%d content-type=%q", response.StatusCode, media)
		}
		body, err = io.ReadAll(io.LimitReader(response.Body, 65536))
		if err != nil {
			return err
		}
	} else {
		network := "tcp"
		if u.Scheme == "udp" && !throughProxy {
			network = "udp"
		}
		conn, err := dial(ctx, network, u.Host)
		if err != nil {
			return err
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		if u.Scheme == "tls" {
			config := tlsConfig.Clone()
			config.ServerName = u.Hostname()
			secure := tls.Client(conn, config)
			if err := secure.HandshakeContext(ctx); err != nil {
				return err
			}
			conn = secure
		}
		if network == "udp" {
			if _, err := conn.Write(query); err != nil {
				return err
			}
			packet := make([]byte, 65535)
			n, err := conn.Read(packet)
			if err != nil {
				return err
			}
			body = packet[:n]
			if n >= 4 && body[2]&0x02 != 0 {
				tcp, err := dial(ctx, "tcp", u.Host)
				if err != nil {
					return err
				}
				defer tcp.Close()
				tcp.SetDeadline(deadline)
				body, err = exchangeProbeStream(tcp, query)
				if err != nil {
					return err
				}
			}
		} else {
			body, err = exchangeProbeStream(conn, query)
			if err != nil {
				return err
			}
		}
	}
	return validateDNSProbe(query, body)
}

func measureDNSList(servers []string, outbound string) []DnsLatencyItem {
	items := make([]DnsLatencyItem, len(servers))
	dial, path, routeErr := dnsProbeRoute(outbound)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, server := range servers {
		wg.Add(1)
		go func(i int, server string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := DnsLatencyItem{Server: server, Outbound: outbound, Path: path}
			err := routeErr
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				start := time.Now()
				err = probeDNS(ctx, server, outbound != "direct", dial)
				item.LatencyMs = time.Since(start).Milliseconds()
			}
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
