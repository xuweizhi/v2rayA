// Package shadowtls implements a ShadowTLS proxy client for v2raya-core.
// Only protocol version 3 is implemented, matching the wire protocol spoken
// by the sing-box / sing-shadowtls ecosystem (HMAC-SHA1 with 4-byte
// truncation, session-id authentication in the TLS ClientHello, and
// per-direction HMAC chains with the "C"/"S" suffixes).
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package shadowtls

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"hash"
	"io"
	"net"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/xtls/xray-core/common"
	xray_buf "github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	xray_net "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

// TLS record types.
const (
	recTypeChangeCipherSpec = 20
	recTypeAlert            = 21
	recTypeHandshake        = 22
	recTypeApplicationData  = 23

	hsTypeClientHello = 1
	hsTypeServerHello = 2
)

const (
	tlsHeaderSize    = 5
	tlsRandomSize    = 32
	tlsSessionIDSize = 32
	hmacSize         = 4
	// clientHelloStart is the offset of the ClientHello message inside its
	// TLS record (5-byte record header).
	clientHelloStart = tlsHeaderSize
	// sessionIDStart is the offset of the legacy session id inside the
	// ClientHello message (handshake header 4 + version 2 + random 32 + 1).
	sessionIDStart = 1 + 3 + 2 + tlsRandomSize + 1
	// serverRandomIndex is the offset of the ServerHello random inside its
	// TLS record.
	serverRandomIndex = tlsHeaderSize + 1 + 3 + 2
	// hmacIndex is the offset of the session-id HMAC slot inside the
	// ClientHello TLS record.
	hmacIndex = clientHelloStart + sessionIDStart + tlsSessionIDSize - hmacSize
	// tlsHmacHeaderSize is the size of a data record header including the
	// HMAC slot.
	tlsHmacHeaderSize = tlsHeaderSize + hmacSize
	// maxDataRecordPayload matches the reference client's 16KB chunks.
	maxDataRecordPayload = 16384
)

// Client is the shadowtls outbound handler.
type Client struct {
	config *ClientConfig
}

// NewClient creates a new shadowtls outbound handler.
func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	if config.Address == "" {
		return nil, errors.New("shadowtls: no server address")
	}
	if config.Password == "" {
		return nil, errors.New("shadowtls: password is required")
	}
	if config.Version != 0 && config.Version != 3 {
		return nil, errors.New("shadowtls: only protocol version 3 is supported")
	}
	return &Client{config: config}, nil
}

// Process implements proxy.Outbound.
func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		return errors.New("target not specified")
	}
	ob := outbounds[len(outbounds)-1]
	if !ob.Target.IsValid() {
		return errors.New("target not specified")
	}

	host, portStr, err := net.SplitHostPort(c.config.Address)
	if err != nil {
		return errors.New("shadowtls: invalid server address ", c.config.Address).Base(err)
	}
	var port uint32
	if p, err := net.LookupPort("tcp", portStr); err == nil {
		port = uint32(p)
	} else {
		return errors.New("shadowtls: invalid server port ", portStr).Base(err)
	}

	conn, err := dialer.Dial(ctx, xray_net.Destination{
		Network: xray_net.Network_TCP,
		Address: xray_net.ParseAddress(host),
		Port:    xray_net.Port(port),
	})
	if err != nil {
		return errors.New("shadowtls: failed to dial server").Base(err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stop()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	stlsConn, err := c.handshake(ctx, conn, host)
	if err != nil {
		return errors.New("shadowtls: handshake failed").Base(err)
	}
	_ = conn.SetDeadline(time.Time{})
	defer stlsConn.Close()

	postRequest := func() error {
		return xray_buf.Copy(link.Reader, xray_buf.NewWriter(stlsConn))
	}
	getResponse := func() error {
		return xray_buf.Copy(xray_buf.NewReader(stlsConn), link.Writer)
	}

	responseDoneAndCloseWriter := task.OnSuccess(getResponse, task.Close(link.Writer))
	if err := task.Run(ctx, postRequest, responseDoneAndCloseWriter); err != nil {
		return errors.New("shadowtls: connection ends").Base(err)
	}
	return nil
}

// handshake performs the TLS handshake with session-id authentication and
// returns a conn that frames application data with HMAC records.
func (c *Client) handshake(ctx context.Context, conn net.Conn, host string) (net.Conn, error) {
	sni := c.config.Sni
	if sni == "" {
		sni = host
	}

	// Use a Chrome-shaped ClientHello with a custom legacy session id:
	// 28 random bytes followed by the 4-byte HMAC slot (initially zero).
	// The per-connection marshal hook fills the slot with the HMAC before the
	// message is hashed into the transcript and written to the wire, so both agree.
	password := c.config.Password
	utlsConfig := &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: c.config.Insecure, // #nosec G402 -- user-configurable
		MinVersion:         utls.VersionTLS13,
		MaxVersion:         utls.VersionTLS13,
		ClientHelloMarshalHook: func(marshaledHello []byte) {
			if len(marshaledHello) < sessionIDStart+tlsSessionIDSize {
				return
			}
			sid := marshaledHello[sessionIDStart : sessionIDStart+tlsSessionIDSize]
			// Zero the HMAC slot first so repeated hook invocations on the same
			// buffer stay idempotent.
			for i := tlsSessionIDSize - hmacSize; i < tlsSessionIDSize; i++ {
				sid[i] = 0
			}
			h := newHMACHash(password)
			h.Write(marshaledHello[:sessionIDStart])
			h.Write(sid)
			h.Write(marshaledHello[sessionIDStart+tlsSessionIDSize:])
			mac := h.Sum()[:hmacSize]
			copy(sid[tlsSessionIDSize-hmacSize:], mac)
		},
		SessionIDProvider: func() [32]byte {
			var sid [32]byte
			if _, err := rand.Read(sid[:tlsSessionIDSize-hmacSize]); err != nil {
				for i := range sid {
					sid[i] = byte(time.Now().UnixNano() >> uint(8*i))
				}
			}
			for i := tlsSessionIDSize - hmacSize; i < tlsSessionIDSize; i++ {
				sid[i] = 0
			}
			return sid
		},
		// The shadowtls server relays a real web server's handshake, which echoes
		// our HMAC-carrying session ID; the internal session ID differs, so skip
		// the echo verification (handled by our own authentication instead).
		SkipSessionIDEchoCheck: true,
	}

	hw := newHandshakeWrapper(conn, password)

	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		return nil, err
	}
	uconn := utls.UClient(hw, utlsConfig, utls.HelloCustom)
	if err := uconn.ApplyPreset(&spec); err != nil {
		return nil, err
	}
	if err := uconn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	serverRandom, ok := hw.serverRandom()
	if !ok {
		return nil, errors.New("shadowtls: server random not captured")
	}

	ignoreChain := hw.ignoreHMAC()
	if ignoreChain == nil {
		// The auth application-data record was not observed during the
		// handshake; the server is not speaking the expected protocol.
		return nil, errors.New("shadowtls: missing auth record")
	}

	// After the handshake the TLS layer is abandoned; application data flows
	// through the raw connection framed with HMAC records. Stop intercepting
	// while preserving any unread buffered bytes so coalesced post-handshake
	// records are never dropped.
	hw.finish(uconn.UnreadRawInput())

	return &verifiedConn{
		Conn:       hw,
		writeHMAC:  newChainHMAC(c.config.Password, serverRandom, "C"),
		readHMAC:   newChainHMAC(c.config.Password, serverRandom, "S"),
		ignoreHMAC: ignoreChain,
	}, nil
}

// chainHMAC is a running HMAC-SHA1 chain used to authenticate data records
// in one direction.
type chainHMAC struct {
	inner *hmacHash
}

func newChainHMAC(password string, serverRandom []byte, suffix string) *chainHMAC {
	h := newHMACHash(password)
	h.Write(serverRandom)
	h.Write([]byte(suffix))
	return &chainHMAC{inner: h}
}

// auth appends payload to the chain and returns the truncated MAC.
func (c *chainHMAC) auth(payload []byte) []byte {
	c.inner.Write(payload)
	mac := c.inner.Sum()[:hmacSize]
	c.inner.Write(mac)
	return mac
}

// verify checks payload against the chain without advancing it.
func (c *chainHMAC) verify(payload, mac []byte) bool {
	h := c.inner.clone()
	h.Write(payload)
	return hmac.Equal(h.Sum()[:hmacSize], mac)
}

// hmacHash is a streaming, cloneable HMAC-SHA1 state using O(1) memory.
type hmacHash struct {
	inner hash.Hash
	opad  [sha1.BlockSize]byte
}

func newHMACHash(password string) *hmacHash {
	var keyBlock [sha1.BlockSize]byte
	key := []byte(password)
	if len(key) > sha1.BlockSize {
		sum := sha1.Sum(key)
		copy(keyBlock[:], sum[:])
	} else {
		copy(keyBlock[:], key)
	}
	var ipad [sha1.BlockSize]byte
	h := &hmacHash{inner: sha1.New()}
	for i := range sha1.BlockSize {
		ipad[i] = keyBlock[i] ^ 0x36
		h.opad[i] = keyBlock[i] ^ 0x5c
	}
	_, _ = h.inner.Write(ipad[:])
	return h
}

// Write feeds data into the running chain in O(1) memory.
func (h *hmacHash) Write(p []byte) {
	_, _ = h.inner.Write(p)
}

func (h *hmacHash) cloneInner() hash.Hash {
	state, err := h.inner.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		panic("shadowtls: sha1 MarshalBinary failed: " + err.Error())
	}
	cloned := sha1.New()
	if err := cloned.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
		panic("shadowtls: sha1 UnmarshalBinary failed: " + err.Error())
	}
	return cloned
}

// Sum returns the current HMAC-SHA1 value without mutating the running state.
func (h *hmacHash) Sum() []byte {
	var innerSum [sha1.Size]byte
	h.cloneInner().Sum(innerSum[:0])
	outer := sha1.New()
	_, _ = outer.Write(h.opad[:])
	_, _ = outer.Write(innerSum[:])
	return outer.Sum(nil)
}

// clone returns an independent O(1) copy of the current chain state.
func (h *hmacHash) clone() *hmacHash {
	return &hmacHash{
		inner: h.cloneInner(),
		opad:  h.opad,
	}
}

// verifiedConn frames application data with HMAC-protected TLS records.
type verifiedConn struct {
	net.Conn
	writeHMAC  *chainHMAC
	readHMAC   *chainHMAC
	ignoreHMAC *chainHMAC
	readBuf    []byte
}

// Read implements net.Conn.
func (c *verifiedConn) Read(p []byte) (int, error) {
	for {
		if len(c.readBuf) > 0 {
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		}
		header := make([]byte, tlsHeaderSize)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint16(header[3:]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.Conn, payload); err != nil {
			return 0, err
		}
		switch header[0] {
		case recTypeAlert:
			return 0, io.EOF
		case recTypeApplicationData:
			if len(payload) < hmacSize {
				return 0, errors.New("shadowtls: short application data record")
			}
			mac := payload[:hmacSize]
			data := payload[hmacSize:]
			if c.ignoreHMAC != nil {
				c.ignoreHMAC.inner.Write(data)
				sum := c.ignoreHMAC.inner.Sum()[:hmacSize]
				if hmac.Equal(sum, mac) {
					// Post-handshake ticket record; drop it.
					continue
				}
				c.ignoreHMAC = nil
			}
			if !c.readHMAC.verify(data, mac) {
				return 0, errors.New("shadowtls: application data verification failed")
			}
			_ = c.readHMAC.auth(data) // advance the chain
			if len(data) == 0 {
				continue
			}
			c.readBuf = data
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		default:
			return 0, errors.New("shadowtls: unexpected TLS record type ", int(header[0]))
		}
	}
}

// Write implements net.Conn.
func (c *verifiedConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxDataRecordPayload {
			chunk = chunk[:maxDataRecordPayload]
		}
		header := make([]byte, tlsHeaderSize)
		header[0] = recTypeApplicationData
		header[1] = 3
		header[2] = 3
		binary.BigEndian.PutUint16(header[3:], uint16(hmacSize+len(chunk)))
		mac := c.writeHMAC.auth(chunk)
		out := make([]byte, 0, tlsHmacHeaderSize+len(chunk))
		out = append(out, header...)
		out = append(out, mac...)
		out = append(out, chunk...)
		if _, err := c.Conn.Write(out); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// handshakeWrapper intercepts handshake reads to capture the server random
// and to authenticate and decrypt the auth application-data record.
type handshakeWrapper struct {
	net.Conn
	password          string
	writeMu           sync.Mutex
	readMu            sync.Mutex
	serverRandomBytes []byte
	ignoreHMACChain   *chainHMAC
	readBuf           []byte
	done              bool
}

func newHandshakeWrapper(conn net.Conn, password string) *handshakeWrapper {
	return &handshakeWrapper{Conn: conn, password: password}
}

// finish switches the wrapper into pass-through mode after the handshake,
// preserving any unread bytes already buffered by the TLS stack or wrapper.
func (w *handshakeWrapper) finish(extra []byte) {
	w.readMu.Lock()
	defer w.readMu.Unlock()
	if len(extra) > 0 {
		combined := make([]byte, 0, len(extra)+len(w.readBuf))
		combined = append(combined, extra...)
		combined = append(combined, w.readBuf...)
		w.readBuf = combined
	}
	w.done = true
}

// Write passes through; the ClientHello session ID is finalized by the
// per-connection utls ClientHelloMarshalHook set up in handshake().
func (w *handshakeWrapper) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return w.Conn.Write(p)
}

// Read intercepts handshake records to capture the server random and to
// authenticate and decrypt the auth application-data record.
func (w *handshakeWrapper) Read(p []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()
	if len(w.readBuf) > 0 {
		n := copy(p, w.readBuf)
		w.readBuf = w.readBuf[n:]
		if len(w.readBuf) == 0 {
			w.readBuf = nil
		}
		return n, nil
	}
	if w.done {
		return w.Conn.Read(p)
	}
	for len(w.readBuf) == 0 {
		header := make([]byte, tlsHeaderSize)
		if _, err := io.ReadFull(w.Conn, header); err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint16(header[3:]))
		body := make([]byte, length)
		if _, err := io.ReadFull(w.Conn, body); err != nil {
			return 0, err
		}
		full := append(header, body...)
		w.readBuf = w.processRecord(full)
	}
	n := copy(p, w.readBuf)
	w.readBuf = w.readBuf[n:]
	if len(w.readBuf) == 0 {
		w.readBuf = nil
	}
	return n, nil
}

// processRecord inspects a server record during the handshake and returns
// the bytes that should be exposed to the TLS stack.
func (w *handshakeWrapper) processRecord(record []byte) []byte {
	switch record[0] {
	case recTypeHandshake:
		if len(record) >= serverRandomIndex+tlsRandomSize && record[tlsHeaderSize] == hsTypeServerHello {
			w.serverRandomBytes = append([]byte(nil), record[serverRandomIndex:serverRandomIndex+tlsRandomSize]...)
			// The no-suffix chain authenticates the server's post-handshake
			// ticket/auth records.
			w.ignoreHMACChain = newChainHMAC(w.password, w.serverRandomBytes, "")
		}
		return record
	case recTypeApplicationData:
		if len(record) >= tlsHmacHeaderSize && len(w.serverRandomBytes) == tlsRandomSize && w.ignoreHMACChain != nil {
			data := record[tlsHmacHeaderSize:]
			mac := record[tlsHeaderSize:tlsHmacHeaderSize]
			// Ticket/auth records use a plain running chain without the
			// hash-value reinsertion used by the data phase.
			w.ignoreHMACChain.inner.Write(data)
			sum := w.ignoreHMACChain.inner.Sum()[:hmacSize]
			if hmac.Equal(sum, mac) {
				// Authenticated ticket/auth record: XOR the payload with the
				// kdf key and strip the HMAC so the TLS stack sees a plain
				// record of the same total length.
				key := shadowTLSKDF(w.password, w.serverRandomBytes)
				xorBytes(data, key)
				copy(record[hmacSize:], record[:tlsHeaderSize])
				binary.BigEndian.PutUint16(record[hmacSize+3:], uint16(len(data)))
				return record[hmacSize:]
			}
			// Not a ticket record; stop intercepting from now on.
			w.ignoreHMACChain = nil
		}
		return record
	default:
		return record
	}
}

func (w *handshakeWrapper) serverRandom() ([]byte, bool) {
	w.readMu.Lock()
	defer w.readMu.Unlock()
	if len(w.serverRandomBytes) == tlsRandomSize {
		return w.serverRandomBytes, true
	}
	return nil, false
}

func (w *handshakeWrapper) ignoreHMAC() *chainHMAC {
	w.readMu.Lock()
	defer w.readMu.Unlock()
	return w.ignoreHMACChain
}

// shadowTLSKDF derives the XOR key for the auth record: SHA-256(password ||
// serverRandom).
func shadowTLSKDF(password string, serverRandom []byte) []byte {
	h := sha256.New()
	h.Write([]byte(password))
	h.Write(serverRandom)
	return h.Sum(nil)
}

func xorBytes(data, key []byte) {
	for i := range data {
		data[i] ^= key[i%len(key)]
	}
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
