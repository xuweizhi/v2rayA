package anytls

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"anytls/proxy/padding"
	"anytls/proxy/session"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"

	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"

	"github.com/v2rayA/v2raya-core/hint/proxy/internal/relay"
	"github.com/v2rayA/v2raya-core/hint/tlsutil"
)

type Client struct {
	pool   *session.Client
	cancel context.CancelFunc
}

type dialerKey struct{}

// A physical session can carry subsequent requests; it belongs to the handler.
type sessionConn struct {
	net.Conn
	cancel context.CancelFunc
	stop   func() bool
}

func (c *sessionConn) Close() error {
	c.stop()
	c.cancel()
	return c.Conn.Close()
}

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	if config.Address == "" || config.Port == 0 || config.Port > 65535 {
		return nil, fmt.Errorf("anytls: invalid server address or port")
	}
	sni := config.Sni
	if sni == "" {
		sni = config.Address
	}
	tlsCfg := &tls.Config{ServerName: sni, InsecureSkipVerify: config.AllowInsecure}
	if err := tlsutil.ConfigurePeerVerification(tlsCfg, config.PinnedPeerCertSha256, config.PinnedPeerCertificateChainSha256, config.VerifyPeerCertByName); err != nil {
		return nil, fmt.Errorf("anytls: %w", err)
	}
	server := xnet.TCPDestination(xnet.ParseAddress(config.Address), xnet.Port(config.Port))
	password := sha256.Sum256([]byte(config.Password))
	lifetime, cancel := context.WithCancel(ctx)
	dialOut := func(ctx context.Context) (net.Conn, error) {
		dialer := ctx.Value(dialerKey{}).(internet.Dialer)
		socketCtx, cancelSocket := context.WithCancel(relay.SessionContext(lifetime, ctx, server))
		stopRequest := context.AfterFunc(ctx, cancelSocket)
		defer stopRequest()
		raw, err := dialer.Dial(socketCtx, server)
		if err != nil {
			cancelSocket()
			return nil, err
		}
		managed := &sessionConn{Conn: raw, cancel: cancelSocket, stop: context.AfterFunc(socketCtx, func() { raw.Close() })}
		conn := tls.Client(managed, tlsCfg)
		if err := conn.HandshakeContext(socketCtx); err != nil {
			managed.Close()
			return nil, err
		}
		var paddingLen int
		if sizes := padding.DefaultPaddingFactory.Load().GenerateRecordPayloadSizes(0); len(sizes) > 0 && sizes[0] > 0 {
			paddingLen = sizes[0]
		}
		if paddingLen > 65535 {
			managed.Close()
			return nil, fmt.Errorf("anytls: invalid handshake padding")
		}
		b := buf.NewSize(len(password) + 2 + paddingLen)
		defer b.Release()
		b.Write(password[:])
		binary.BigEndian.PutUint16(b.Extend(2), uint16(paddingLen))
		b.WriteZeroN(paddingLen)
		if _, err = b.WriteTo(conn); err != nil {
			managed.Close()
			return nil, err
		}
		return conn, nil
	}
	minIdle := int(config.MinIdleSessions)
	if minIdle == 0 {
		minIdle = 5
	}
	return &Client{pool: session.NewClient(lifetime, dialOut, &padding.DefaultPaddingFactory, 30*time.Second, 30*time.Second, minIdle), cancel: cancel}, nil
}

func (c *Client) Close() error {
	c.cancel()
	return c.pool.Close()
}

func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	target, err := relay.Target(ctx)
	if err != nil {
		return err
	}
	stream, err := c.pool.CreateStream(context.WithValue(ctx, dialerKey{}, dialer))
	if err != nil {
		return err
	}
	dst := M.ParseSocksaddr(target.NetAddr())
	header := dst
	if target.Network == xnet.Network_UDP {
		header = uot.RequestDestination(2)
	}
	if err := M.SocksaddrSerializer.WriteAddrPort(stream, header); err != nil {
		stream.Close()
		return err
	}
	if target.Network == xnet.Network_UDP {
		packets := uot.NewLazyConn(stream, uot.Request{Destination: dst})
		return relay.Packets(ctx, link, packets, target)
	}
	return relay.Stream(ctx, link, stream)
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
