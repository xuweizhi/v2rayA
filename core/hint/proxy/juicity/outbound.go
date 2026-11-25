package juicity

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/daeuniverse/outbound/netproxy"
	"github.com/daeuniverse/outbound/protocol"
	juice "github.com/daeuniverse/outbound/protocol/juicity"
	"github.com/daeuniverse/outbound/protocol/trojanc"
	"github.com/daeuniverse/outbound/protocol/tuic"
	qcommon "github.com/daeuniverse/outbound/protocol/tuic/common"
	quic "github.com/daeuniverse/quic-go"
	"github.com/google/uuid"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/v2rayA/v2raya-core/hint/proxy/internal/relay"
	"github.com/v2rayA/v2raya-core/hint/tlsutil"
	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
)

// Client owns the QUIC session and authentication stream. The upstream
// packet/stream codecs handle Juicity's destination headers and UDP framing.
type Client struct {
	config   *ClientConfig
	server   M.Socksaddr
	id       uuid.UUID
	tls      *tls.Config
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	conn     quic.Connection
}

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	server, err := relay.ServerAddress(config.Address)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(config.Uuid)
	if err != nil {
		return nil, fmt.Errorf("juicity UUID: %w", err)
	}
	sni := config.Sni
	if sni == "" {
		sni = server.AddrString()
	}
	tlsCfg := &tls.Config{ServerName: sni, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13, InsecureSkipVerify: config.AllowInsecure}
	if err := tlsutil.ConfigurePeerVerification(tlsCfg, "", config.PinnedCertchainSha256, ""); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	return &Client{config: config, server: server, id: id, tls: tlsCfg, lifetime: life, cancel: cancel}, nil
}

func (c *Client) session(ctx context.Context, d internet.Dialer) (quic.Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifetime.Err() != nil {
		return nil, net.ErrClosed
	}
	if c.conn != nil && c.conn.Context().Err() == nil {
		return c.conn, nil
	}

	server := xnet.UDPDestination(xnet.ParseAddress(c.server.AddrString()), xnet.Port(c.server.Port))
	// A chained server connection must outlive the request that opened it.
	socketCtx, cancelSocket := context.WithCancel(relay.SessionContext(c.lifetime, ctx, server))
	stopRequest := context.AfterFunc(ctx, cancelSocket)
	defer stopRequest()
	raw, err := (relay.UDPDialer{Dialer: d}).DialContext(socketCtx, "udp", c.server)
	if err != nil {
		cancelSocket()
		return nil, err
	}
	conn, err := quic.Dial(socketCtx, bufio.NewUnbindPacketConn(raw), raw.RemoteAddr(), c.tls, &quic.Config{
		HandshakeIdleTimeout: 8 * time.Second, KeepAlivePeriod: 5 * time.Second,
		InitialStreamReceiveWindow: qcommon.InitialStreamReceiveWindow, MaxStreamReceiveWindow: qcommon.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: qcommon.InitialConnectionReceiveWindow, MaxConnectionReceiveWindow: qcommon.MaxConnectionReceiveWindow,
	})
	if err != nil {
		cancelSocket()
		raw.Close()
		return nil, err
	}
	context.AfterFunc(conn.Context(), func() { cancelSocket(); raw.Close() })
	qcommon.SetCongestionController(conn, c.config.CongestionControl, 10)
	if err := c.authenticate(conn); err != nil {
		conn.CloseWithError(1, "authentication failed")
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

func (c *Client) authenticate(conn quic.Connection) error {
	token, err := tuic.GenToken(conn.ConnectionState(), c.id, c.config.Password)
	if err != nil {
		return err
	}
	var auth bytes.Buffer
	if err := tuic.NewAuthenticate(c.id, token, juice.Version0).WriteTo(&auth); err != nil {
		return err
	}
	stream, err := conn.OpenUniStream()
	if err != nil {
		return err
	}
	// The Juicity control stream stays open for the life of the connection.
	_, err = stream.Write(auth.Bytes())
	return err
}

func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.CloseWithError(0, "")
	}
	return nil
}

func (c *Client) Process(ctx context.Context, link *transport.Link, d internet.Dialer) error {
	target, err := relay.Target(ctx)
	if err != nil {
		return err
	}
	session, err := c.session(ctx, d)
	if err != nil {
		return err
	}
	metadata, err := protocol.ParseMetadata(target.NetAddr())
	if err != nil {
		return err
	}
	metadata.IsClient = true
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	conn := juice.NewConn(stream, &trojanc.Metadata{Metadata: metadata, Network: target.Network.SystemString()}, nil)
	if _, err := conn.Write(nil); err != nil {
		conn.Close()
		return err
	}
	if target.Network == xnet.Network_UDP {
		pc := netproxy.NewFakeNetPacketConn(&juice.PacketConn{Conn: conn}, session.LocalAddr(), session.RemoteAddr())
		return relay.Packets(ctx, link, pc, target)
	}
	return relay.Stream(ctx, link, conn)
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
