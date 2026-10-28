package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func probeAnswer(t *testing.T, query []byte) []byte {
	t.Helper()
	var message dnsmessage.Message
	if err := message.Unpack(query); err != nil {
		t.Error(err)
		return nil
	}
	message.Response = true
	message.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: message.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}}}
	body, err := message.Pack()
	if err != nil {
		t.Error(err)
	}
	return body
}

func TestDNSProbeRejectsFalseSuccess(t *testing.T) {
	query := buildDNSQuery(dnsTestDomain, 0x0100)
	body := probeAnswer(t, query)
	if err := validateDNSProbe(query, body); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"html": []byte("<html>ok</html>"), "query echo": query, "truncated": body[:10]}
	badID := append([]byte(nil), body...)
	badID[0] ^= 1
	cases["wrong ID"] = badID
	badCode := append([]byte(nil), body...)
	badCode[3] = 2
	cases["SERVFAIL"] = badCode
	for name, packet := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateDNSProbe(query, packet); err == nil {
				t.Fatal("false success")
			}
		})
	}
}

func TestDNSProbeDoHAndTLSCertificates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(probeAnswer(t, query))
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dial := (&net.Dialer{}).DialContext
	if err := probeDNSWithTLS(ctx, server.URL+"/dns-query", false, dial, &tls.Config{RootCAs: roots}); err != nil {
		t.Fatal(err)
	}
	if err := probeDNS(ctx, server.URL+"/dns-query", false, dial); err == nil {
		t.Fatal("untrusted DoH certificate was accepted")
	}
}

func TestDNSProbeDoTUsesFramedQueryAndSelectedDialer(t *testing.T) {
	certSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer certSource.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", certSource.TLS)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		var header [2]byte
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			finished <- err
			return
		}
		query := make([]byte, binary.BigEndian.Uint16(header[:]))
		if _, err = io.ReadFull(conn, query); err != nil {
			finished <- err
			return
		}
		answer := probeAnswer(t, query)
		binary.BigEndian.PutUint16(header[:], uint16(len(answer)))
		// Split the length prefix to exercise proper stream framing.
		_, err = conn.Write(header[:1])
		if err == nil {
			_, err = conn.Write(append(header[1:], answer...))
		}
		finished <- err
	}()
	roots := x509.NewCertPool()
	roots.AddCert(certSource.Certificate())
	called := false
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		called = true
		if network != "tcp" || address != listener.Addr().String() {
			t.Errorf("unexpected path %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := probeDNSWithTLS(ctx, "tls://"+listener.Addr().String(), true, dial, &tls.Config{RootCAs: roots}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("selected route bypassed")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestDNSProbeRejectsPlainTCPAsDoT(t *testing.T) {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			packet := make([]byte, 1024)
			b.Read(packet)
			b.Write([]byte("not a TLS server"))
		}()
		return a, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := probeDNS(ctx, "tls://127.0.0.1:853", true, dial)
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatal("open TCP endpoint treated as valid DoT")
	}
}
