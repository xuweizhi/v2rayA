package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

func isEncryptedDNS(protocol string) bool {
	switch protocol {
	case "https", "doh", "tcp-tls", "tls", "dot":
		return true
	}
	return false
}

func encryptedDNSAddress(address, protocol string) (string, string, error) {
	if protocol == "https" || protocol == "doh" {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return "", "", fmt.Errorf("invalid DoH URL %q", address)
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		return u.Hostname(), port, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", err
	}
	return host, port, nil
}

// TLS keeps the original DNS hostname for certificate verification even after
// bootstrap has resolved the dial address. No system DNS lookup is needed then.
func (m *UpstreamManager) exchangeEncrypted(upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	return m.exchangeEncryptedWithTLS(upstream, query, &tls.Config{MinVersion: tls.VersionTLS12})
}

func (m *UpstreamManager) exchangeEncryptedWithTLS(upstream *UpstreamInstance, query *DnsQuery, tlsConfig *tls.Config) (*DnsResponse, error) {
	host, port, err := encryptedDNSAddress(upstream.Addr, upstream.Protocol)
	if err != nil {
		return nil, err
	}
	tlsConfig = tlsConfig.Clone()
	tlsConfig.ServerName = host
	target := net.JoinHostPort(host, port)
	if upstream.DialAddress != "" {
		target = upstream.DialAddress
	}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		if upstream.ProxyTag == "" || upstream.ProxyTag == "direct" {
			if net.ParseIP(host) == nil && upstream.DialAddress == "" {
				return nil, fmt.Errorf("DNS bootstrap unresolved: %s", host)
			}
			return markedDialer().DialContext(ctx, network, target)
		}
		if dispatcher := m.getDispatcher(); dispatcher != nil {
			return dispatcher.Dispatch(ctx, network, target, upstream.ProxyTag)
		}
		address := m.resolveProxyAddr(upstream.ProxyTag)
		if address == "" {
			return nil, fmt.Errorf("DNS proxy %q is unavailable", upstream.ProxyTag)
		}
		d, err := proxy.SOCKS5("tcp", address, nil, markedDialer())
		if err != nil {
			return nil, err
		}
		return d.(proxy.ContextDialer).DialContext(ctx, network, target)
	}
	timeout := 8 * time.Second
	if upstream.attemptTimeout > 0 {
		timeout = upstream.attemptTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	message := new(dns.Msg)
	message.SetQuestion(dns.Fqdn(query.Name), uint16(query.QType))
	wire, err := message.Pack()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	var response *dns.Msg
	if upstream.Protocol == "https" || upstream.Protocol == "doh" {
		transport := &http.Transport{DialContext: dial, TLSClientConfig: tlsConfig}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.Addr, bytes.NewReader(wire))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if res.StatusCode != http.StatusOK || media != "application/dns-message" {
			return nil, fmt.Errorf("invalid DoH response: status=%d type=%q", res.StatusCode, media)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 65536))
		if err != nil {
			return nil, err
		}
		if len(body) > 65535 {
			return nil, fmt.Errorf("oversized DNS response")
		}
		response = new(dns.Msg)
		if err := response.Unpack(body); err != nil {
			return nil, err
		}
	} else {
		conn, err := dial(ctx, "tcp", target)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		dnsConn := &dns.Conn{Conn: secure}
		if err := dnsConn.WriteMsg(message); err != nil {
			return nil, err
		}
		response, err = dnsConn.ReadMsg()
		if err != nil {
			return nil, err
		}
	}
	if response.Id != message.Id {
		return nil, fmt.Errorf("DNS response ID mismatch")
	}
	if err := ValidateResponse(response); err != nil {
		return nil, err
	}
	if err := ValidateQuestionMatch(response, query.Name, query.QType); err != nil {
		return nil, err
	}
	ttl := uint32(0)
	for i, answer := range response.Answer {
		if i == 0 || answer.Header().Ttl < ttl {
			ttl = answer.Header().Ttl
		}
	}
	return &DnsResponse{Query: *query, RawMsg: response, Rcode: response.Rcode, Answer: response.Answer, Authority: response.Ns, Additional: response.Extra, TTL: ttl, Upstream: upstream.Addr, ProxyTag: upstream.ProxyTag, RTT: time.Since(start)}, nil
}
