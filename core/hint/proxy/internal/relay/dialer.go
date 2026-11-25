package relay

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// UDPDialer opens the QUIC server socket through this outbound's Xray handler.
type UDPDialer struct{ internet.Dialer }

func (d UDPDialer) DialContext(ctx context.Context, network string, addr M.Socksaddr) (net.Conn, error) {
	if network != "udp" {
		return nil, fmt.Errorf("expected UDP transport, got %s", network)
	}
	raw, err := d.Dial(ctx, xnet.UDPDestination(xnet.ParseAddress(addr.AddrString()), xnet.Port(addr.Port)))
	if err != nil {
		return nil, err
	}
	// A proxied Xray link can batch several UDP buffers into one read.
	// Keep one packet per QUIC read and leave the traffic counters in place.
	if pipe, ok := stat.TryUnwrapStatsConn(raw).(*cnc.Connection); ok {
		pc := &packetPipe{Conn: pipe, reader: buf.BufferedReader{Reader: pipe, Splitter: buf.SplitFirstBytes}, peer: &net.UDPAddr{IP: net.IPv4zero, Port: int(addr.Port)}}
		if counter, ok := raw.(*stat.CounterConnection); ok {
			return &stat.CounterConnection{Connection: pc, ReadCounter: counter.ReadCounter, WriteCounter: counter.WriteCounter}, nil
		}
		return pc, nil
	}
	return raw, nil
}

type packetPipe struct {
	net.Conn
	mu     sync.Mutex
	reader buf.BufferedReader
	peer   *net.UDPAddr
}

func (c *packetPipe) RemoteAddr() net.Addr { return c.peer }
func (c *packetPipe) LocalAddr() net.Addr  { return &net.UDPAddr{IP: net.IPv4zero} }
func (c *packetPipe) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reader.Read(p)
}
func (c *packetPipe) Close() error {
	err := c.Conn.Close()
	c.mu.Lock()
	c.reader.Buffer = buf.ReleaseMulti(c.reader.Buffer)
	c.mu.Unlock()
	return err
}

func ServerAddress(address string) (M.Socksaddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return M.Socksaddr{}, fmt.Errorf("invalid server address %q", address)
	}
	number, err := xnet.PortFromString(port)
	if err != nil || number == 0 {
		return M.Socksaddr{}, fmt.Errorf("invalid server port %q", port)
	}
	return M.ParseSocksaddrHostPort(host, uint16(number)), nil
}

func (d UDPDialer) ListenPacket(ctx context.Context, addr M.Socksaddr) (net.PacketConn, error) {
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	return bufio.NewUnbindPacketConn(c), nil
}

// SessionContext carries the routing metadata needed by a dialer into a
// shared session. The originating request may finish while the session is in use.
func SessionContext(lifetime, request context.Context, server xnet.Destination) context.Context {
	if in := session.InboundFromContext(request); in != nil {
		lifetime = session.ContextWithInbound(lifetime, &session.Inbound{Source: in.Source, Local: in.Local, Tag: in.Tag})
	}
	ob := &session.Outbound{Target: server}
	if out := session.OutboundsFromContext(request); len(out) > 0 {
		ob.Tag = out[len(out)-1].Tag
		ob.Gateway = out[len(out)-1].Gateway
	}
	return session.ContextWithOutbounds(lifetime, []*session.Outbound{ob})
}
