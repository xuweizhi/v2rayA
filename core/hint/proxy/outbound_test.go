package proxy_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"anytls/proxy/padding"
	anysession "anytls/proxy/session"
	juice "github.com/daeuniverse/outbound/protocol/juicity"
	wire "github.com/daeuniverse/outbound/protocol/tuic"
	dquic "github.com/daeuniverse/quic-go"
	"github.com/google/uuid"
	tuicsdk "github.com/sagernet/sing-quic/tuic"
	sbuf "github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	stls "github.com/sagernet/sing/common/tls"
	"github.com/sagernet/sing/common/uot"

	"github.com/v2rayA/v2raya-core/hint/proxy/anytls"
	"github.com/v2rayA/v2raya-core/hint/proxy/juicity"
	"github.com/v2rayA/v2raya-core/hint/proxy/tuic"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
)

const userID = "01234567-89ab-4def-8123-0123456789ab"

type redirectDialer struct {
	internet.Dialer
	addr  net.Addr
	calls atomic.Int32
}

func (d *redirectDialer) Dial(ctx context.Context, dest xnet.Destination) (stat.Connection, error) {
	if dest.NetAddr() != "edge.test:444" || dest.Network.SystemString() != d.addr.Network() {
		return nil, fmt.Errorf("unexpected server destination %s for %s", dest, d.addr)
	}
	d.calls.Add(1)
	return (&net.Dialer{}).DialContext(ctx, d.addr.Network(), d.addr.String())
}

type handler interface {
	Process(context.Context, *transport.Link, internet.Dialer) error
	Close() error
}

func exercise(t *testing.T, h handler, addr net.Addr) {
	t.Helper()
	d := &redirectDialer{addr: addr}
	defer h.Close()
	for _, network := range []xnet.Network{xnet.Network_TCP, xnet.Network_UDP} {
		dest := xnet.Destination{Network: network, Address: xnet.ParseAddress("2001:db8::1"), Port: 443}
		ctx, cancel := context.WithTimeout(session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Target: dest}}), 6*time.Second)
		reader, input := pipe.New()
		output, writer := pipe.New()
		done := make(chan error, 1)
		go func() { done <- h.Process(ctx, &transport.Link{Reader: reader, Writer: writer}, d); writer.Close() }()
		for i, payload := range []string{"hello", "second payload"} {
			b := buf.FromBytes([]byte(payload))
			target := dest
			target.Port += xnet.Port(i)
			if network == xnet.Network_UDP {
				b.UDP = &target
			}
			if err := input.WriteMultiBuffer(buf.MultiBuffer{b}); err != nil {
				cancel()
				t.Fatal(err)
			}
			result, err := output.ReadMultiBufferTimeout(6 * time.Second)
			if err != nil || len(result) != 1 || string(result[0].Bytes()) != payload {
				cancel()
				input.Close()
				t.Fatalf("%s echo returned %v, %v; handler returned %v", network, result, err, <-done)
			}
			if network == xnet.Network_UDP && (result[0].UDP == nil || result[0].UDP.NetAddr() != target.NetAddr()) {
				cancel()
				input.Close()
				t.Fatalf("incorrect source endpoint %v for %v", result[0].UDP, target)
			}
			buf.ReleaseMulti(result)
		}
		input.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handler did not stop")
		}
	}
	if d.calls.Load() == 0 {
		t.Fatal("the configured server dialer was not used")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
}

func serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	s := httptest.NewTLSServer(http.NotFoundHandler())
	cert := s.TLS.Certificates[0]
	s.Close()
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h3"}}
}

func TestAnyTLSStreamsAndDatagrams(t *testing.T) {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var header [34]byte
				if _, err := io.ReadFull(c, header[:]); err != nil {
					return
				}
				password := sha256.Sum256([]byte("test"))
				if !bytes.Equal(header[:32], password[:]) {
					return
				}
				if _, err = io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(header[32:]))); err != nil {
					return
				}
				s := anysession.NewServerSession(c, func(stream *anysession.Stream) {
					defer stream.Close()
					dst, err := M.SocksaddrSerializer.ReadAddrPort(stream)
					if err != nil {
						return
					}
					if dst != uot.RequestDestination(2) {
						if dst.String() == "[2001:db8::1]:443" {
							io.Copy(stream, stream)
						}
						return
					}
					req, err := uot.ReadRequest(stream)
					if err != nil {
						return
					}
					pc := uot.NewConn(stream, *req)
					var b [2048]byte
					for {
						n, addr, err := pc.ReadFrom(b[:])
						if err != nil {
							return
						}
						if _, err = pc.WriteTo(b[:n], addr); err != nil {
							return
						}
					}
				}, &padding.DefaultPaddingFactory)
				s.Run()
				s.Close()
			}()
		}
	}()
	client, err := anytls.NewClient(context.Background(), &anytls.ClientConfig{Address: "edge.test", Port: 444, Password: "test", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, client, ln.Addr())
}

type testTLS struct {
	stls.Config
	cfg *tls.Config
}

func (s testTLS) Start() error                            { return nil }
func (s testTLS) Close() error                            { return nil }
func (s testTLS) STDConfig() (*tls.Config, error)         { return s.cfg, nil }
func (s testTLS) Server(conn net.Conn) (stls.Conn, error) { return tls.Server(conn, s.cfg), nil }

type echo struct{}

func (echo) NewConnectionEx(ctx context.Context, c net.Conn, src, dst M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer c.Close()
	if dst.String() == "[2001:db8::1]:443" {
		io.Copy(c, c)
	}
}
func (echo) NewPacketConnectionEx(ctx context.Context, c N.PacketConn, src, dst M.Socksaddr, onClose N.CloseHandlerFunc) {
	defer c.Close()
	for {
		b := sbuf.NewPacket()
		target, err := c.ReadPacket(b)
		if err != nil {
			b.Release()
			return
		}
		if err = c.WritePacket(b, target); err != nil {
			return
		}
	}
}
func TestTUICStreamsAndDatagrams(t *testing.T) {
	for _, mode := range []string{"native", "quic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pc, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			service, err := tuicsdk.NewService[int](tuicsdk.ServiceOptions{Context: ctx, Logger: logger.NOP(), TLSConfig: testTLS{cfg: serverTLS(t)}, Handler: echo{}, UDPTimeout: time.Second * 5})
			if err != nil {
				t.Fatal(err)
			}
			service.UpdateUsers([]int{1}, [][16]byte{uuid.MustParse(userID)}, []string{"test"})
			if err = service.Start(pc); err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			client, err := tuic.NewClient(ctx, &tuic.ClientConfig{Address: "edge.test:444", Uuid: userID, Password: "test", UdpRelayMode: mode, AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			exercise(t, client, pc.LocalAddr())
		})
	}
}

func TestJuicityStreamsAndDatagrams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := dquic.ListenAddr("127.0.0.1:0", serverTLS(t), &dquic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		auth, err := c.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		req, err := wire.ReadAuthenticate(bufio.NewReader(auth))
		if err != nil {
			return
		}
		token, err := wire.GenToken(c.ConnectionState(), uuid.MustParse(userID), "test")
		if err != nil || req.VER != juice.Version0 || req.TOKEN != token || req.UUID != uuid.MustParse(userID) {
			c.CloseWithError(1, "bad authentication")
			return
		}
		for {
			stream, err := c.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func() {
				conn := juice.NewConn(stream, nil, nil)
				defer conn.Close()
				// Trigger the server-side Juicity destination decoder.
				if _, err := conn.Read(nil); err != nil {
					return
				}
				if conn.Metadata.Hostname != "2001:db8::1" || conn.Metadata.Port != 443 {
					return
				}
				if conn.Metadata.Network == "tcp" {
					io.Copy(conn, conn)
					return
				}
				packet := juice.PacketConn{Conn: conn}
				var b [2048]byte
				for {
					n, addr, err := packet.ReadFrom(b[:])
					if err != nil {
						return
					}
					if _, err = packet.WriteTo(b[:n], addr.String()); err != nil {
						return
					}
				}
			}()
		}
	}()
	client, err := juicity.NewClient(ctx, &juicity.ClientConfig{Address: "edge.test:444", Uuid: userID, Password: "test", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, client, ln.Addr())
}

type stalledDialer struct {
	internet.Dialer
	ready chan net.Conn
}

func (d stalledDialer) Dial(ctx context.Context, _ xnet.Destination) (stat.Connection, error) {
	client, server := net.Pipe()
	d.ready <- server
	return client, nil
}
func TestAnyTLSCloseDuringHandshake(t *testing.T) {
	c, err := anytls.NewClient(context.Background(), &anytls.ClientConfig{Address: "edge.test", Port: 444})
	if err != nil {
		t.Fatal(err)
	}
	d := stalledDialer{ready: make(chan net.Conn, 1)}
	target := xnet.TCPDestination(xnet.DomainAddress("example.test"), 443)
	ctx, cancel := context.WithTimeout(session.ContextWithOutbounds(context.Background(), []*session.Outbound{{Target: target}}), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Process(ctx, &transport.Link{}, d) }()
	select {
	case conn := <-d.ready:
		defer conn.Close()
	case <-ctx.Done():
		t.Fatal("server dial did not start")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled handshake returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("closing the handler left the handshake blocked")
	}
}
