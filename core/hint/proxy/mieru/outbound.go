// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package mieru

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/enfein/mieru/v3/apis/client"
	mierucommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/apis/model"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/internet"
	"google.golang.org/protobuf/proto"
)

type Client struct {
	profile *appctlpb.ClientProfile
	mu      sync.Mutex
	sdk     client.Client
	closed  bool
}

func NewClient(_ context.Context, config *ClientConfig) (*Client, error) {
	if config.Username == "" || config.Password == "" {
		return nil, fmt.Errorf("mieru: username and password are required")
	}
	if config.Transport != "" && !strings.EqualFold(config.Transport, "tcp") {
		return nil, fmt.Errorf("mieru: only TCP transport is supported")
	}
	host, portText, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("mieru: invalid server address")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("mieru: invalid server port")
	}
	endpoint := &appctlpb.ServerEndpoint{
		PortBindings: []*appctlpb.PortBinding{{Port: proto.Int32(int32(port)), Protocol: appctlpb.TransportProtocol_TCP.Enum()}},
	}
	if net.ParseIP(host) == nil {
		endpoint.DomainName = proto.String(host)
	} else {
		endpoint.IpAddress = proto.String(host)
	}
	return &Client{profile: &appctlpb.ClientProfile{
		ProfileName: proto.String("v2raya"),
		User:        &appctlpb.User{Name: proto.String(config.Username), Password: proto.String(config.Password)},
		Servers:     []*appctlpb.ServerEndpoint{endpoint},
	}}, nil
}

// The handler supplies its configured Xray dialer on the first request.
func (c *Client) start(dialer mierucommon.Dialer) (client.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, net.ErrClosed
	}
	if c.sdk == nil {
		sdk := client.NewClient()
		if err := sdk.Store(&client.ClientConfig{
			Profile: c.profile, Dialer: dialer,
			DNSConfig: &mierucommon.ClientDNSConfig{BypassDialerDNS: true},
		}); err != nil {
			return nil, err
		}
		if err := sdk.Start(); err != nil {
			return nil, err
		}
		c.sdk = sdk
	}
	return c.sdk, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.sdk != nil {
		return c.sdk.Stop()
	}
	return nil
}

type serverDialer struct{ internet.Dialer }

func (d serverDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dest, err := xnet.ParseDestination("tcp:" + address)
	if err != nil {
		return nil, err
	}
	return d.Dial(ctx, dest)
}

func (c *Client) Process(ctx context.Context, link *transport.Link, dialer internet.Dialer) error {
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		return fmt.Errorf("mieru: missing target")
	}
	dest := outbounds[len(outbounds)-1].Target
	if !dest.IsValid() || dest.Network != xnet.Network_TCP {
		return fmt.Errorf("mieru: expected TCP target")
	}
	target := model.NetAddrSpec{Net: "tcp"}
	if err := target.AddrSpec.From(dest.NetAddr()); err != nil {
		return err
	}
	sdk, err := c.start(serverDialer{dialer})
	if err != nil {
		return err
	}
	conn, err := sdk.DialContext(ctx, target)
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	send := func() error { return buf.Copy(link.Reader, buf.NewWriter(conn)) }
	receive := func() error { return buf.Copy(buf.NewReader(conn), link.Writer) }
	return task.Run(ctx, send, task.OnSuccess(receive, task.Close(link.Writer)))
}

func init() {
	common.Must(common.RegisterConfig((*ClientConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewClient(ctx, config.(*ClientConfig))
	}))
}
