// Package mieru implements a mieru proxy client (TCP transport) for
// v2raya-core, based on the public mieru protocol documentation
// (https://github.com/enfein/mieru/blob/main/docs/protocol.md).
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package mieru

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/pbkdf2"

	"github.com/xtls/xray-core/common"
	xray_buf "github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	xray_net "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

const (
	// MetadataLength is the fixed size of a plaintext segment metadata block.
	MetadataLength = 32
	// nonceSize is the XChaCha20-Poly1305 nonce size.
	nonceSize = 24
	// overhead is the AEAD authentication tag size.
	overhead = 16

	// keyIter is the PBKDF2 iteration count used by the mieru protocol.
	keyIter = 64
	// keyRefreshInterval is the amount of time the key-generation salt is
	// rounded to.
	keyRefreshInterval = 2 * time.Minute

	// maxPDU is the maximum payload size of one TCP data segment.
	maxPDU = 32 * 1024
	// maxSessionOpenPayload is the maximum payload carried by the
	// openSessionRequest segment.
	maxSessionOpenPayload = 1024

	// heartbeatInterval is how often an ACK is sent when the connection is idle.
	heartbeatInterval = 5 * time.Second

	// advertisedReceiveWindow is the receive window (in segments) announced
	// to the server. On TCP transports mieru relies on TCP backpressure, so a
	// generous constant is sufficient.
	advertisedReceiveWindow = 1024
)

// Protocol type bytes.
const (
	protoOpenSessionRequest   = 2
	protoOpenSessionResponse  = 3
	protoCloseSessionRequest  = 4
	protoCloseSessionResponse = 5
	protoDataClientToServer   = 6
	protoDataServerToClient   = 7
	protoAckClientToServer    = 8
	protoAckServerToClient    = 9
)

// Client is the mieru outbound handler.
type Client struct {
	config *ClientConfig
}

// NewClient creates a new mieru outbound handler.
func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	if config.Address == "" {
		return nil, errors.New("mieru: no server address")
	}
	if config.Username == "" || config.Password == "" {
		return nil, errors.New("mieru: username and password are required")
	}
	if config.Transport != "" && !strings.EqualFold(config.Transport, "TCP") {
		return nil, errors.New("mieru: only TCP transport is supported")
	}
	return &Client{config: config}, nil
}

// Process implements proxy.Outbound.
func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := session.OutboundsFromContext(ctx)
	ob := outbounds[len(outbounds)-1]
	if !ob.Target.IsValid() {
		return errors.New("target not specified")
	}
	target := ob.Target

	host, portStr, err := net.SplitHostPort(c.config.Address)
	if err != nil {
		return errors.New("mieru: invalid server address ", c.config.Address).Base(err)
	}
	var port uint32
	if p, err := net.LookupPort("tcp", portStr); err == nil {
		port = uint32(p)
	} else {
		return errors.New("mieru: invalid server port ", portStr).Base(err)
	}

	conn, err := dialer.Dial(ctx, xray_net.Destination{
		Network: xray_net.Network_TCP,
		Address: xray_net.ParseAddress(host),
		Port:    xray_net.Port(port),
	})
	if err != nil {
		return errors.New("mieru: failed to dial server").Base(err)
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	// Bound the session-opening phase; the relay phase afterwards relies on
	// the context-cancellation watcher above.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	mc, err := newMieruConn(conn, c.config.Username, c.config.Password)
	if err != nil {
		return errors.New("mieru: init connection").Base(err)
	}
	if err := mc.openSession(ctx, target); err != nil {
		return errors.New("mieru: open session failed").Base(err)
	}
	_ = conn.SetDeadline(time.Time{})

	go mc.heartbeatLoop(ctx)

	postRequest := func() error {
		return xray_buf.Copy(link.Reader, xray_buf.NewWriter(mc))
	}
	getResponse := func() error {
		return xray_buf.Copy(xray_buf.NewReader(mc), link.Writer)
	}

	responseDoneAndCloseWriter := task.OnSuccess(getResponse, task.Close(link.Writer))
	if err := task.Run(ctx, postRequest, responseDoneAndCloseWriter); err != nil {
		mc.closeSession()
		return errors.New("mieru: connection ends").Base(err)
	}
	return nil
}

// blockCipher is a stateful XChaCha20-Poly1305 cipher block. On the first
// seal/open call in a direction the 24-byte nonce is exchanged over the wire;
// afterwards the nonce is advanced by one before every operation.
type blockCipher struct {
	aead interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}
	nonce []byte
	mu    sync.Mutex
}

func newBlockCipher(username, password string) (*blockCipher, error) {
	hashedInput := append([]byte(password), 0x00)
	hashedInput = append(hashedInput, []byte(username)...)
	hashedPassword := sha256.Sum256(hashedInput)

	rounded := time.Now().Round(keyRefreshInterval)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(rounded.Unix()))
	timeSalt := sha256.Sum256(b[:])

	key := pbkdf2.Key(hashedPassword[:], timeSalt[:], keyIter, 32, sha256.New)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return &blockCipher{aead: aead}, nil
}

func (c *blockCipher) advanceNonce() {
	for i := len(c.nonce) - 1; i >= 0; i-- {
		c.nonce[i]++
		if c.nonce[i] != 0 {
			break
		}
	}
}

// common64Set is the character set used to rewrite the first bytes of a fresh
// nonce, mirroring the reference mieru client so the wire traffic keeps the
// same entropy profile.
const common64Set = "!@#$%^&*()ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz<>"

func rewriteNoncePrefix(nonce []byte) {
	for i := 0; i < 8 && i < len(nonce); i++ {
		nonce[i] = common64Set[nonce[i]&0x3f]
	}
}

// seal encrypts plaintext. The first call prepends a freshly generated nonce.
func (c *blockCipher) seal(plaintext []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nonce == nil {
		c.nonce = make([]byte, nonceSize)
		if _, err := rand.Read(c.nonce); err != nil {
			return nil, err
		}
		rewriteNoncePrefix(c.nonce)
		out := make([]byte, 0, nonceSize+len(plaintext)+overhead)
		out = append(out, c.nonce...)
		return c.aead.Seal(out, c.nonce, plaintext, nil), nil
	}
	c.advanceNonce()
	return c.aead.Seal(nil, c.nonce, plaintext, nil), nil
}

// open decrypts ciphertext. The first call expects a nonce prefix.
func (c *blockCipher) open(ciphertext []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var nonce []byte
	if c.nonce == nil {
		if len(ciphertext) < nonceSize {
			return nil, fmt.Errorf("ciphertext smaller than nonce size")
		}
		c.nonce = make([]byte, nonceSize)
		copy(c.nonce, ciphertext[:nonceSize])
		nonce = c.nonce
		ciphertext = ciphertext[nonceSize:]
	} else {
		c.advanceNonce()
		nonce = c.nonce
	}
	return c.aead.Open(nil, nonce, ciphertext, nil)
}

// mieruConn wraps a TCP connection with the mieru segment protocol for one
// proxy session.
type mieruConn struct {
	conn    net.Conn
	send    *blockCipher
	recv    *blockCipher
	writeMu sync.Mutex

	sessionID uint32
	nextSend  uint32
	nextRecv  uint32

	// readBuf holds decrypted payload that has been read ahead of the
	// application (e.g. data after the socks5 reply).
	readBuf bytes.Buffer
	closed  bool

	lastWriteMu sync.Mutex
	lastWrite   time.Time
}

func newMieruConn(conn net.Conn, username, password string) (*mieruConn, error) {
	send, err := newBlockCipher(username, password)
	if err != nil {
		return nil, fmt.Errorf("init send cipher: %w", err)
	}
	recv, err := newBlockCipher(username, password)
	if err != nil {
		return nil, fmt.Errorf("init recv cipher: %w", err)
	}
	return &mieruConn{
		conn:      conn,
		send:      send,
		recv:      recv,
		sessionID: newSessionID(),
	}, nil
}

func newSessionID() uint32 {
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return uint32(time.Now().UnixNano()) | 1
		}
		id := binary.BigEndian.Uint32(b[:])
		if id != 0 {
			return id
		}
	}
}

func nowMinutes() uint32 {
	return uint32(time.Now().Unix() / 60)
}

// writeSessionSegment writes a session-protocol segment (open/close).
func (mc *mieruConn) writeSessionSegment(proto byte, status byte, payload []byte) error {
	if len(payload) > maxSessionOpenPayload {
		return fmt.Errorf("payload too large for session segment: %d", len(payload))
	}
	meta := make([]byte, MetadataLength)
	meta[0] = proto
	binary.BigEndian.PutUint32(meta[2:6], nowMinutes())
	binary.BigEndian.PutUint32(meta[6:10], mc.sessionID)
	binary.BigEndian.PutUint32(meta[10:14], mc.nextSend)
	mc.nextSend++
	meta[14] = status
	binary.BigEndian.PutUint16(meta[15:17], uint16(len(payload)))
	return mc.writeEncrypted(meta, payload)
}

// writeDataSegment writes a data/ack-protocol segment.
func (mc *mieruConn) writeDataSegment(proto byte, seq, unAckSeq uint32, payload []byte) error {
	meta := make([]byte, MetadataLength)
	meta[0] = proto
	binary.BigEndian.PutUint32(meta[2:6], nowMinutes())
	binary.BigEndian.PutUint32(meta[6:10], mc.sessionID)
	binary.BigEndian.PutUint32(meta[10:14], seq)
	binary.BigEndian.PutUint32(meta[14:18], unAckSeq)
	binary.BigEndian.PutUint16(meta[18:20], advertisedReceiveWindow)
	// fragment 0, prefixLen 0
	binary.BigEndian.PutUint16(meta[22:24], uint16(len(payload)))
	return mc.writeEncrypted(meta, payload)
}

func (mc *mieruConn) writeEncrypted(meta, payload []byte) error {
	encMeta, err := mc.send.seal(meta)
	if err != nil {
		return err
	}
	out := encMeta
	if len(payload) > 0 {
		encPayload, err := mc.send.seal(payload)
		if err != nil {
			return err
		}
		out = append(out, encPayload...)
	}
	mc.writeMu.Lock()
	defer mc.writeMu.Unlock()
	if _, err := mc.conn.Write(out); err != nil {
		return err
	}
	mc.lastWriteMu.Lock()
	mc.lastWrite = time.Now()
	mc.lastWriteMu.Unlock()
	return nil
}

type segment struct {
	proto    byte
	status   byte
	session  uint32
	seq      uint32
	unAckSeq uint32
	payload  []byte
}

// readSegment reads and decrypts one segment from the server.
func (mc *mieruConn) readSegment() (*segment, error) {
	hdrLen := MetadataLength + overhead
	if mc.recv.nonce == nil {
		hdrLen += nonceSize
	}
	hdr := make([]byte, hdrLen)
	if _, err := io.ReadFull(mc.conn, hdr); err != nil {
		return nil, err
	}
	meta, err := mc.recv.open(hdr)
	if err != nil {
		return nil, fmt.Errorf("decrypt metadata: %w", err)
	}
	seg := &segment{
		proto:   meta[0],
		session: binary.BigEndian.Uint32(meta[6:10]),
		seq:     binary.BigEndian.Uint32(meta[10:14]),
	}
	switch seg.proto {
	case protoOpenSessionResponse, protoCloseSessionRequest, protoCloseSessionResponse:
		seg.status = meta[14]
		payloadLen := binary.BigEndian.Uint16(meta[15:17])
		suffixLen := meta[17]
		if payloadLen > 0 {
			enc := make([]byte, int(payloadLen)+overhead)
			if _, err := io.ReadFull(mc.conn, enc); err != nil {
				return nil, err
			}
			if seg.payload, err = mc.recv.open(enc); err != nil {
				return nil, fmt.Errorf("decrypt payload: %w", err)
			}
		}
		if err := mc.skipPadding(int(suffixLen)); err != nil {
			return nil, err
		}
	case protoDataServerToClient, protoAckServerToClient:
		prefixLen := meta[21]
		payloadLen := binary.BigEndian.Uint16(meta[22:24])
		suffixLen := meta[24]
		seg.unAckSeq = binary.BigEndian.Uint32(meta[14:18])
		if err := mc.skipPadding(int(prefixLen)); err != nil {
			return nil, err
		}
		if payloadLen > 0 {
			enc := make([]byte, int(payloadLen)+overhead)
			if _, err := io.ReadFull(mc.conn, enc); err != nil {
				return nil, err
			}
			if seg.payload, err = mc.recv.open(enc); err != nil {
				return nil, fmt.Errorf("decrypt payload: %w", err)
			}
		}
		if err := mc.skipPadding(int(suffixLen)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unexpected protocol type %d", seg.proto)
	}
	return seg, nil
}

func (mc *mieruConn) skipPadding(n int) error {
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(mc.conn, buf)
	return err
}

// sendAck sends an ackClientToServer segment.
func (mc *mieruConn) sendAck() error {
	seq := uint32(0)
	if mc.nextSend > 0 {
		seq = mc.nextSend - 1
	}
	return mc.writeDataSegment(protoAckClientToServer, seq, mc.nextRecv, nil)
}

// openSession performs the mieru session opening and the embedded socks5
// CONNECT handshake.
func (mc *mieruConn) openSession(ctx context.Context, target xray_net.Destination) error {
	request := buildSocks5ConnectRequest(target)
	if len(request) > maxSessionOpenPayload {
		return fmt.Errorf("socks5 request too large: %d", len(request))
	}
	if err := mc.writeSessionSegment(protoOpenSessionRequest, 0, request); err != nil {
		return err
	}

	seg, err := mc.readSegment()
	if err != nil {
		return err
	}
	if seg.proto != protoOpenSessionResponse {
		return fmt.Errorf("expected openSessionResponse, got %d", seg.proto)
	}
	if seg.session != mc.sessionID {
		return fmt.Errorf("session ID mismatch: got %d, want %d", seg.session, mc.sessionID)
	}
	if seg.status != 0 {
		return fmt.Errorf("session rejected with status %d", seg.status)
	}
	mc.nextRecv = seg.seq + 1

	// Read the socks5 reply carried in data segments.
	var reply []byte
	for {
		seg, err = mc.readSegment()
		if err != nil {
			return err
		}
		switch seg.proto {
		case protoDataServerToClient:
			if seg.seq != mc.nextRecv {
				return fmt.Errorf("unexpected data sequence %d, want %d", seg.seq, mc.nextRecv)
			}
			mc.nextRecv++
			reply = append(reply, seg.payload...)
			if err := mc.sendAck(); err != nil {
				return err
			}
			if len(reply) < 4 {
				continue
			}
			if reply[0] != 0x05 {
				return fmt.Errorf("bad socks5 reply version %d", reply[0])
			}
			var addrLen int
			switch reply[3] {
			case 0x01:
				addrLen = 4
			case 0x04:
				addrLen = 16
			case 0x03:
				if len(reply) < 5 {
					continue
				}
				addrLen = int(reply[4]) + 1
			default:
				return fmt.Errorf("bad socks5 reply address type %d", reply[3])
			}
			need := 4 + addrLen + 2
			if len(reply) < need {
				continue
			}
			if reply[1] != 0x00 {
				return fmt.Errorf("socks5 connect failed with reply %d", reply[1])
			}
			if rest := reply[need:]; len(rest) > 0 {
				_, _ = mc.readBuf.Write(rest)
			}
			return nil
		case protoAckServerToClient:
			// ignore on TCP
		case protoCloseSessionRequest, protoCloseSessionResponse:
			return fmt.Errorf("session closed during open")
		default:
			return fmt.Errorf("unexpected protocol type %d", seg.proto)
		}
	}
}

func (mc *mieruConn) closeSession() {
	if mc.closed {
		return
	}
	mc.closed = true
	_ = mc.writeSessionSegment(protoCloseSessionRequest, 0, nil)
}

func (mc *mieruConn) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mc.lastWriteMu.Lock()
			idle := time.Since(mc.lastWrite) >= heartbeatInterval-time.Second
			mc.lastWriteMu.Unlock()
			if idle {
				_ = mc.sendAck()
			}
		}
	}
}

// Write implements io.Writer. Data is chunked into encrypted data segments.
func (mc *mieruConn) Write(p []byte) (int, error) {
	if mc.closed {
		return 0, io.ErrClosedPipe
	}
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxPDU {
			n = maxPDU
		}
		if err := mc.writeDataSegment(protoDataClientToServer, mc.nextSend, mc.nextRecv, p[:n]); err != nil {
			return written, err
		}
		mc.nextSend++
		written += n
		p = p[n:]
	}
	return written, nil
}

// Read implements io.Reader. It returns decrypted payload from data segments,
// sending acks as data arrives.
func (mc *mieruConn) Read(p []byte) (int, error) {
	if mc.readBuf.Len() > 0 {
		return mc.readBuf.Read(p)
	}
	for {
		seg, err := mc.readSegment()
		if err != nil {
			return 0, err
		}
		switch seg.proto {
		case protoDataServerToClient:
			if seg.seq != mc.nextRecv {
				return 0, fmt.Errorf("unexpected data sequence %d, want %d", seg.seq, mc.nextRecv)
			}
			mc.nextRecv++
			if len(seg.payload) > 0 {
				mc.readBuf.Write(seg.payload)
			}
			_ = mc.sendAck()
			if mc.readBuf.Len() > 0 {
				return mc.readBuf.Read(p)
			}
		case protoAckServerToClient:
			// ignore on TCP
		case protoCloseSessionRequest:
			_ = mc.writeSessionSegment(protoCloseSessionResponse, 0, nil)
			return 0, io.EOF
		case protoCloseSessionResponse:
			return 0, io.EOF
		default:
			return 0, fmt.Errorf("unexpected protocol type %d", seg.proto)
		}
	}
}

// buildSocks5ConnectRequest builds the socks5 CONNECT request carried inside
// the openSessionRequest payload.
func buildSocks5ConnectRequest(target xray_net.Destination) []byte {
	req := []byte{0x05, 0x01, 0x00}
	switch target.Address.Family() {
	case xray_net.AddressFamilyDomain:
		domain := target.Address.Domain()
		req = append(req, 0x03, byte(len(domain)))
		req = append(req, []byte(domain)...)
	case xray_net.AddressFamilyIPv4:
		ip := target.Address.IP()
		if len(ip) != 4 {
			ip = ip.To4()
		}
		req = append(req, 0x01)
		req = append(req, ip[:4]...)
	case xray_net.AddressFamilyIPv6:
		ip := target.Address.IP().To16()
		req = append(req, 0x04)
		req = append(req, ip[:16]...)
	default:
		s := target.Address.String()
		req = append(req, 0x03, byte(len(s)))
		req = append(req, []byte(s)...)
	}
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, uint16(target.Port))
	req = append(req, port...)
	return req
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
