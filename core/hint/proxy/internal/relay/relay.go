// Package relay adapts Xray links to stream and packet-oriented protocol clients.
package relay

import (
	"context"
	"fmt"
	"io"
	"net"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/task"
	"github.com/xtls/xray-core/transport"
)

func Target(ctx context.Context) (xnet.Destination, error) {
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) != 0 {
		target := outbounds[len(outbounds)-1].Target
		if target.Address != nil && (target.Network == xnet.Network_TCP || target.Network == xnet.Network_UDP) {
			return target, nil
		}
	}
	return xnet.Destination{}, fmt.Errorf("missing TCP or UDP destination")
}

func Stream(ctx context.Context, link *transport.Link, conn io.ReadWriteCloser) error {
	defer conn.Close()
	send := func() error {
		if err := buf.Copy(link.Reader, buf.NewWriter(conn)); err != nil {
			return err
		}
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			return half.CloseWrite()
		}
		return nil
	}
	receive := func() error { return buf.Copy(buf.NewReader(conn), link.Writer) }
	return task.Run(ctx, send, task.OnSuccess(receive, task.Close(link.Writer)))
}

func Packets(ctx context.Context, link *transport.Link, conn net.PacketConn, target xnet.Destination) error {
	defer conn.Close()
	send := func() error {
		for {
			packets, readErr := link.Reader.ReadMultiBuffer()
			for _, packet := range packets {
				dest := target
				if packet.UDP != nil {
					dest = *packet.UDP
				}
				if _, err := conn.WriteTo(packet.Bytes(), M.ParseSocksaddr(dest.NetAddr())); err != nil {
					buf.ReleaseMulti(packets)
					return err
				}
			}
			buf.ReleaseMulti(packets)
			if readErr != nil {
				return readErr
			}
		}
	}
	receive := func() error {
		for {
			packet := buf.NewWithSize(65535)
			n, source, err := conn.ReadFrom(packet.Extend(65535))
			if err != nil {
				packet.Release()
				return err
			}
			packet.Resize(0, int32(n))
			addr := M.SocksaddrFromNet(source)
			if addr.IsValid() {
				dest := xnet.UDPDestination(xnet.ParseAddress(addr.AddrString()), xnet.Port(addr.Port))
				packet.UDP = &dest
			}
			if err := link.Writer.WriteMultiBuffer(buf.MultiBuffer{packet}); err != nil {
				return err
			}
		}
	}
	return task.Run(ctx, send, task.OnSuccess(receive, task.Close(link.Writer)))
}
