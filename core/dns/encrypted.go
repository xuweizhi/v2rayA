package dns

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
	"strings"
	"time"

	"github.com/apernet/quic-go"
	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

func isEncryptedDNS(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "https", "doh", "tcp-tls", "tls", "dot", "quic", "doq":
		return true
	}
	return false
}

func isDoQProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "quic", "doq":
		return true
	}
	return false
}

func encryptedDNSAddress(address, protocol string) (string, string, error) {
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "https" || proto == "doh" {
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
	trimmed := strings.TrimSpace(address)
	trimmed = strings.TrimPrefix(trimmed, "tls://")
	trimmed = strings.TrimPrefix(trimmed, "quic://")
	host, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		host = strings.Trim(trimmed, "[]")
		if host == "" {
			return "", "", fmt.Errorf("invalid encrypted DNS address %q", address)
		}
		return host, "853", nil
	}
	return host, port, nil
}

// TLS keeps the original DNS hostname for certificate verification even after
// bootstrap has resolved the dial address. No system DNS lookup is needed then.
func (m *UpstreamManager) exchangeEncrypted(upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	return m.exchangeEncryptedContext(context.Background(), upstream, query)
}

func (m *UpstreamManager) exchangeEncryptedContext(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery) (*DnsResponse, error) {
	return m.exchangeEncryptedWithTLSContext(ctx, upstream, query, &tls.Config{MinVersion: tls.VersionTLS12})
}

func (m *UpstreamManager) exchangeEncryptedWithTLS(upstream *UpstreamInstance, query *DnsQuery, tlsConfig *tls.Config) (*DnsResponse, error) {
	return m.exchangeEncryptedWithTLSContext(context.Background(), upstream, query, tlsConfig)
}

func (m *UpstreamManager) exchangeEncryptedWithTLSContext(ctx context.Context, upstream *UpstreamInstance, query *DnsQuery, tlsConfig *tls.Config) (*DnsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	proto := strings.ToLower(strings.TrimSpace(upstream.Protocol))
	host, port, err := encryptedDNSAddress(upstream.Addr, proto)
	if err != nil {
		return nil, err
	}
	tlsConfig = tlsConfig.Clone()
	tlsConfig.ServerName = host
	target := net.JoinHostPort(host, port)
	if upstream.DialAddress != "" {
		target = upstream.DialAddress
	}
	proxied := isProxiedUpstream(upstream)

	dial := func(dialCtx context.Context, network, _ string) (net.Conn, error) {
		if !proxied {
			if net.ParseIP(host) == nil && upstream.DialAddress == "" {
				return nil, fmt.Errorf("DNS bootstrap unresolved: %s", host)
			}
			return markedDialer().DialContext(dialCtx, network, target)
		}
		if dispatcher := m.getDispatcher(); dispatcher != nil {
			return dispatcher.Dispatch(dialCtx, network, target, upstream.ProxyTag)
		}
		address := m.resolveProxyAddr(upstream.ProxyTag)
		if address == "" {
			return nil, fmt.Errorf("DNS proxy %q is unavailable", upstream.ProxyTag)
		}
		d, err := proxy.SOCKS5("tcp", address, nil, markedDialer())
		if err != nil {
			return nil, err
		}
		return d.(proxy.ContextDialer).DialContext(dialCtx, network, target)
	}

	timeout := 8 * time.Second
	if upstream.attemptTimeout > 0 {
		timeout = upstream.attemptTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	message := buildUpstreamQuestion(query, proxied)
	expectedID := message.Id

	start := time.Now()
	var response *dns.Msg
	switch {
	case proto == "https" || proto == "doh":
		wire, err := message.Pack()
		if err != nil {
			return nil, err
		}
		transport := &http.Transport{DialContext: dial, TLSClientConfig: tlsConfig}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, upstream.Addr, bytes.NewReader(wire))
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

	case isDoQProtocol(proto):
		if proxied {
			return nil, fmt.Errorf("DNS-over-QUIC (DoQ) over proxy %q is not supported; use DoH, DoT, or TCP for proxied upstreams", upstream.ProxyTag)
		}
		if net.ParseIP(host) == nil && upstream.DialAddress == "" {
			return nil, fmt.Errorf("DNS bootstrap unresolved: %s", host)
		}
		expectedID = 0
		response, err = exchangeDoQ(reqCtx, target, tlsConfig, message, timeout)
		if err != nil {
			return nil, err
		}

	default:
		conn, err := dial(reqCtx, "tcp", target)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		stopWatch := context.AfterFunc(reqCtx, func() { _ = conn.Close() })
		defer stopWatch()
		if deadline, ok := reqCtx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(reqCtx); err != nil {
			if reqCtx.Err() != nil {
				return nil, reqCtx.Err()
			}
			return nil, err
		}
		dnsConn := &dns.Conn{Conn: secure}
		if err := dnsConn.WriteMsg(message); err != nil {
			if reqCtx.Err() != nil {
				return nil, reqCtx.Err()
			}
			return nil, err
		}
		response, err = dnsConn.ReadMsg()
		if err != nil {
			if reqCtx.Err() != nil {
				return nil, reqCtx.Err()
			}
			return nil, err
		}
	}

	if response.Id != expectedID {
		return nil, fmt.Errorf("DNS response ID mismatch")
	}
	if err := ValidateResponse(response); err != nil {
		return nil, err
	}
	if err := validateQuestionMatchWithClass(response, query.Name, query.QType, query.QClass); err != nil {
		return nil, err
	}
	ttl := uint32(0)
	for i, answer := range response.Answer {
		if i == 0 || answer.Header().Ttl < ttl {
			ttl = answer.Header().Ttl
		}
	}
	return &DnsResponse{
		Query:      *query,
		RawMsg:     response,
		Rcode:      response.Rcode,
		Answer:     response.Answer,
		Authority:  response.Ns,
		Additional: response.Extra,
		TTL:        ttl,
		Upstream:   upstream.Addr,
		UpstreamID: upstream.ID,
		ProxyTag:   upstream.ProxyTag,
		RTT:        time.Since(start),
	}, nil
}

// exchangeDoQ performs a DNS-over-QUIC exchange per RFC 9250 (2-byte length
// prefix over a bidirectional QUIC stream, message ID 0, ALPN "doq") using a
// marked UDP socket so transparent proxy rules do not loop the query back.
func exchangeDoQ(ctx context.Context, target string, tlsConfig *tls.Config, message *dns.Msg, timeout time.Duration) (*dns.Msg, error) {
	doqMsg := message.Copy()
	doqMsg.Id = 0 // RFC 9250 §4.2.1: Message ID MUST be 0.
	wire, err := doqMsg.Pack()
	if err != nil {
		return nil, err
	}
	if len(wire) > 65535 {
		return nil, fmt.Errorf("DoQ query too large: %d bytes", len(wire))
	}

	udpAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, fmt.Errorf("DoQ resolve target %q: %w", target, err)
	}
	udpNet := "udp4"
	if udpAddr.IP.To4() == nil {
		udpNet = "udp6"
	}

	lc := net.ListenConfig{Control: markFd}
	pc, err := lc.ListenPacket(ctx, udpNet, ":0")
	if err != nil {
		return nil, fmt.Errorf("DoQ listen UDP: %w", err)
	}
	tr := &quic.Transport{Conn: pc}
	defer func() {
		_ = tr.Close()
		_ = pc.Close()
	}()

	doqTLS := tlsConfig.Clone()
	if doqTLS.MinVersion < tls.VersionTLS13 {
		doqTLS.MinVersion = tls.VersionTLS13
	}
	doqTLS.NextProtos = []string{"doq"}

	quicCfg := &quic.Config{
		HandshakeIdleTimeout: timeout,
		MaxIdleTimeout:       timeout,
	}
	qconn, err := tr.Dial(ctx, udpAddr, doqTLS, quicCfg)
	if err != nil {
		return nil, fmt.Errorf("DoQ dial %s: %w", target, err)
	}
	defer qconn.CloseWithError(0, "")

	stream, err := qconn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("DoQ open stream: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}
	stopWatch := context.AfterFunc(ctx, func() {
		stream.CancelRead(0)
		stream.CancelWrite(0)
	})
	defer stopWatch()

	// RFC 9250 §4.2: 2-byte big-endian message length prefix followed by wire DNS message.
	frame := make([]byte, 2+len(wire))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(wire)))
	copy(frame[2:], wire)
	if _, err := stream.Write(frame); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("DoQ write query: %w", err)
	}
	// Close the write side (FIN) to indicate end of DNS query message.
	if err := stream.Close(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("DoQ close stream: %w", err)
	}

	var lenBuf [2]byte
	if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("DoQ read response length: %w", err)
	}
	respLen := binary.BigEndian.Uint16(lenBuf[:])
	if respLen == 0 {
		return nil, fmt.Errorf("DoQ empty response")
	}
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(stream, respBuf); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("DoQ read response body: %w", err)
	}

	resp := new(dns.Msg)
	if err := resp.Unpack(respBuf); err != nil {
		return nil, fmt.Errorf("DoQ unpack response: %w", err)
	}
	return resp, nil
}
