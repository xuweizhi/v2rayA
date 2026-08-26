package shadowtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

// TestE2E runs the client against a local reference ShadowTLS v3 server
// implementing the same wire protocol.
func TestE2E(t *testing.T) {
	password := "test-password"
	cert := makeCert(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runTestServer(ln, cert, password)
	}()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	client := &Client{config: &ClientConfig{
		Address:  ln.Addr().String(),
		Password: password,
		Sni:      "shadowtls.test",
		Insecure: true,
		Version:  3,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stls, err := client.handshake(ctx, conn, "127.0.0.1")
	if err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	defer stls.Close()

	// Give the server time to finish its TLS handshake and send the ticket
	// records so the data record does not get consumed by the server's TLS
	// read-ahead buffer.
	time.Sleep(500 * time.Millisecond)

	msg := "hello shadowtls"
	if _, err := stls.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	resp := make([]byte, 128)
	n, err := stls.Read(resp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(resp[:n])
	want := "echo:" + msg
	if got != want {
		t.Fatalf("unexpected echo: %q, want %q", got, want)
	}
	// Drain server error asynchronously.
	select {
	case err := <-serverErr:
		if err != nil && err != io.EOF {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Log("server still running")
	}
}

// makeCert builds a self-signed certificate for the test server.
func makeCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "shadowtls.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"shadowtls.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

// runTestServer accepts one connection, speaks the shadowtls v3 protocol and
// echoes data prefixed with "echo:".
func runTestServer(ln net.Listener, cert tls.Certificate, password string) error {
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	sw := newTestServerWrap(conn, password)
	tlsConn := tls.Server(sw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	_ = tlsConn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	fmt.Println("SRV hs done")
	if len(sw.serverRandom) != tlsRandomSize {
		return hmacErr("server random not captured")
	}
	// Wait until the automatic NewSessionTicket (the auth record) has been
	// transformed and sent before writing application data.
	select {
	case <-sw.authSent:
	case <-time.After(10 * time.Second):
		return hmacErr("auth record not sent")
	}

	// Application data bypasses the TLS layer; read/write on the raw
	// connection with the per-direction HMAC chains.
	data := &testDataConn{
		Conn:      sw.Conn,
		readHMAC:  newChainHMAC(password, sw.serverRandom, "C"),
		writeHMAC: newChainHMAC(password, sw.serverRandom, "S"),
	}
	buf := make([]byte, 128)
	n, err := data.Read(buf)
	if err != nil {
		return err
	}
	reply := append([]byte("echo:"), buf[:n]...)
	_, err = data.Write(reply)
	return err
}

func hmacErr(s string) error { return &testError{s} }

type testError struct{ s string }

func (e *testError) Error() string { return e.s }

// testServerWrap intercepts the server side of the handshake: it verifies
// the ClientHello session-id HMAC, captures the ServerHello random and
// transforms the first application-data record into the HMAC/XOR auth form.
type testServerWrap struct {
	net.Conn
	password        string
	serverRandom    []byte
	ignoreChain     *chainHMAC
	helloSeen       bool
	authTransformed bool
	authSent        chan struct{}
	dump            bool
	readBuf         []byte
}

func newTestServerWrap(conn net.Conn, password string) *testServerWrap {
	return &testServerWrap{
		Conn:     conn,
		password: password,
		authSent: make(chan struct{}, 1),
	}
}

func (w *testServerWrap) Read(p []byte) (int, error) {
	if !w.helloSeen {
		header := make([]byte, tlsHeaderSize)
		if _, err := io.ReadFull(w.Conn, header); err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint16(header[3:]))
		body := make([]byte, length)
		if _, err := io.ReadFull(w.Conn, body); err != nil {
			return 0, err
		}
		record := append(header, body...)
		if err := w.verifyClientHello(record); err != nil {
			return 0, err
		}
		w.helloSeen = true
		w.readBuf = record
	}
	if len(w.readBuf) > 0 {
		n := copy(p, w.readBuf)
		w.readBuf = w.readBuf[n:]
		return n, nil
	}
	// Pass through directly; never return (0, nil) which would make the TLS
	// stack busy-loop.
	return w.Conn.Read(p)
}

// verifyClientHello checks the session-id HMAC of the ClientHello record.
func (w *testServerWrap) verifyClientHello(record []byte) error {
	if w.dump {
		fmt.Printf("SERVER hello hex (first 100): %x\n", record[:100])
		fmt.Printf("SERVER hello record len=%d\n", len(record))
	}
	const minLen = tlsHeaderSize + 1 + 3 + 2 + tlsRandomSize + 1 + tlsSessionIDSize
	if len(record) < minLen {
		return hmacErr("client hello too short")
	}
	if record[0] != recTypeHandshake || record[tlsHeaderSize] != hsTypeClientHello {
		return hmacErr("first record is not a ClientHello")
	}
	if record[clientHelloStart+sessionIDStart-1] != tlsSessionIDSize {
		return hmacErr("unexpected session id length")
	}
	h := newHMACHash(w.password)
	h.Write(record[tlsHeaderSize:hmacIndex])
	h.Write([]byte{0, 0, 0, 0})
	h.Write(record[hmacIndex+hmacSize:])
	mac := record[hmacIndex : hmacIndex+hmacSize]
	if !hmac.Equal(h.Sum()[:hmacSize], mac) {
		return hmacErr("session id hmac mismatch")
	}
	return nil
}

// Write intercepts outgoing records to capture the server random and to
// transform the first application-data record into the auth form.
func (w *testServerWrap) Write(p []byte) (int, error) {
	total := len(p)
	out := make([]byte, 0, len(p)+tlsHmacHeaderSize)
	for len(p) > 0 {
		if len(p) < tlsHeaderSize {
			out = append(out, p...)
			break
		}
		length := int(binary.BigEndian.Uint16(p[3:]))
		if len(p) < tlsHeaderSize+length {
			out = append(out, p...)
			break
		}
		rec := p[:tlsHeaderSize+length]
		p = p[tlsHeaderSize+length:]
		switch rec[0] {
		case recTypeHandshake:
			if len(rec) >= serverRandomIndex+tlsRandomSize && rec[tlsHeaderSize] == hsTypeServerHello {
				w.serverRandom = append([]byte(nil), rec[serverRandomIndex:serverRandomIndex+tlsRandomSize]...)
				w.ignoreChain = newChainHMAC(w.password, w.serverRandom, "")
			}
			out = append(out, rec...)
		case recTypeApplicationData:
			if w.ignoreChain == nil {
				return total, hmacErr("application data before server hello")
			}
			// Ticket/auth records use a plain running chain without the
			// hash-value reinsertion used by the data phase.
			data := append([]byte(nil), rec[tlsHeaderSize:]...)
			key := shadowTLSKDF(w.password, w.serverRandom)
			xorBytes(data, key)
			w.ignoreChain.inner.Write(data)
			mac := w.ignoreChain.inner.Sum()[:hmacSize]
			hdr := make([]byte, tlsHeaderSize)
			hdr[0] = recTypeApplicationData
			hdr[1] = 3
			hdr[2] = 3
			binary.BigEndian.PutUint16(hdr[3:], uint16(hmacSize+len(data)))
			out = append(out, hdr...)
			out = append(out, mac...)
			out = append(out, data...)
			if !w.authTransformed {
				w.authTransformed = true
				close(w.authSent)
			}
		default:
			out = append(out, rec...)
		}
	}
	_, err := w.Conn.Write(out)
	return total, err
}

// testDataConn frames server-side application data with the HMAC chains.
type testDataConn struct {
	net.Conn
	readHMAC  *chainHMAC
	writeHMAC *chainHMAC
	readBuf   []byte
}

func (c *testDataConn) Read(p []byte) (int, error) {
	for len(c.readBuf) == 0 {
		header := make([]byte, tlsHeaderSize)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		fmt.Printf("SRV data rec hdr=%x\n", header)
		length := int(binary.BigEndian.Uint16(header[3:]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.Conn, payload); err != nil {
			return 0, err
		}
		fmt.Printf("SRV data hdr=%02x len=%d\n", header[0], length)
		if header[0] != recTypeApplicationData || len(payload) < hmacSize {
			return 0, hmacErr("unexpected record type")
		}
		mac := payload[:hmacSize]
		data := payload[hmacSize:]
		if !c.readHMAC.verify(data, mac) {
			return 0, hmacErr("data verification failed")
		}
		_ = c.readHMAC.auth(data)
		fmt.Printf("SRV data rec ok data=%q\n", data)
		c.readBuf = data
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *testDataConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxDataRecordPayload {
			chunk = chunk[:maxDataRecordPayload]
		}
		mac := c.writeHMAC.auth(chunk)
		hdr := make([]byte, tlsHeaderSize)
		hdr[0] = recTypeApplicationData
		hdr[1] = 3
		hdr[2] = 3
		binary.BigEndian.PutUint16(hdr[3:], uint16(hmacSize+len(chunk)))
		out := append(append(append([]byte{}, hdr...), mac...), chunk...)
		if _, err := c.Conn.Write(out); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}
