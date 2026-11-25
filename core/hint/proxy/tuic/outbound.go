package tuic

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"github.com/google/uuid"
	sdk "github.com/sagernet/sing-quic/tuic"
	M "github.com/sagernet/sing/common/metadata"
	stls "github.com/sagernet/sing/common/tls"
	"github.com/xtls/xray-core/common"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"

	"github.com/v2rayA/v2raya-core/hint/proxy/internal/relay"
	"github.com/v2rayA/v2raya-core/hint/tlsutil"
)

type Client struct {
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	options  sdk.ClientOptions
	client   *sdk.Client
}

// stdTLS exposes the standard library config to sing's transport API.
type stdTLS struct{ config *tls.Config }

func (s stdTLS) ServerName() string                   { return s.config.ServerName }
func (s stdTLS) SetServerName(name string)            { s.config.ServerName = name }
func (s stdTLS) NextProtos() []string                 { return s.config.NextProtos }
func (s stdTLS) SetNextProtos(p []string)             { s.config.NextProtos = p }
func (s stdTLS) STDConfig() (*tls.Config, error)      { return s.config, nil }
func (s stdTLS) Client(c net.Conn) (stls.Conn, error) { return tls.Client(c, s.config), nil }
func (s stdTLS) Clone() stls.Config                   { return stdTLS{s.config.Clone()} }

func NewClient(ctx context.Context, config *ClientConfig) (*Client, error) {
	server, err := relay.ServerAddress(config.Address)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(config.Uuid)
	if err != nil {
		return nil, fmt.Errorf("tuic UUID: %w", err)
	}
	sni := config.Sni
	if sni == "" {
		sni = server.AddrString()
	}
	alpn := config.Alpn
	if len(alpn) == 0 {
		alpn = []string{"h3"}
	}
	tlsCfg := &tls.Config{ServerName: sni, NextProtos: alpn, InsecureSkipVerify: config.AllowInsecure, MinVersion: tls.VersionTLS13}
	names := config.VerifyPeerCertByName
	if config.DisableSni && !config.AllowInsecure && names == "" && config.PinnedPeerCertSha256 == "" && config.PinnedPeerCertificateChainSha256 == "" {
		names = sni
	}
	if err := tlsutil.ConfigurePeerVerification(tlsCfg, config.PinnedPeerCertSha256, config.PinnedPeerCertificateChainSha256, names); err != nil {
		return nil, err
	}
	if config.DisableSni {
		// Go omits SNI for IP literals. An empty ServerName would let the
		// QUIC library fill it from the transport's remote address.
		tlsCfg.ServerName = "127.0.0.1"
	}
	cc := config.CongestionControl
	if cc == "" {
		cc = "bbr"
	}
	switch config.UdpRelayMode {
	case "", "native", "quic":
	default:
		return nil, fmt.Errorf("tuic: unsupported UDP relay mode %q", config.UdpRelayMode)
	}
	options := sdk.ClientOptions{ServerAddress: server, UUID: id, Password: config.Password, TLSConfig: stdTLS{tlsCfg}, CongestionControl: cc, UDPStream: config.UdpRelayMode == "quic"}
	lifetime, cancel := context.WithCancel(ctx)
	return &Client{lifetime: lifetime, cancel: cancel, options: options}, nil
}

func (c *Client) start(ctx context.Context, d internet.Dialer) (*sdk.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifetime.Err() != nil {
		return nil, net.ErrClosed
	}
	if c.client == nil {
		options := c.options
		server := options.ServerAddress
		options.Context = relay.SessionContext(c.lifetime, ctx, xnet.UDPDestination(xnet.ParseAddress(server.AddrString()), xnet.Port(server.Port)))
		options.Dialer = relay.UDPDialer{Dialer: d}
		client, err := sdk.NewClient(options)
		if err != nil {
			return nil, err
		}
		c.client = client
	}
	return c.client, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel()
	if c.client != nil {
		return c.client.CloseWithError(net.ErrClosed)
	}
	return nil
}

func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	target, err := relay.Target(ctx)
	if err != nil {
		return err
	}
	client, err := c.start(ctx, dialer)
	if err != nil {
		return err
	}
	if target.Network == xnet.Network_UDP {
		pc, err := client.ListenPacket(ctx)
		if err != nil {
			return err
		}
		return relay.Packets(ctx, link, pc, target)
	}
	conn, err := client.DialConn(ctx, M.ParseSocksaddr(target.NetAddr()))
	if err != nil {
		return err
	}
	return relay.Stream(ctx, link, conn)
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
