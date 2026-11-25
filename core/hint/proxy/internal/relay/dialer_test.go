package relay

import (
	"context"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/pipe"
	"testing"
)

type pipeDialer struct {
	internet.Dialer
	conn stat.Connection
}

func (d pipeDialer) Dial(context.Context, xnet.Destination) (stat.Connection, error) {
	return d.conn, nil
}

func TestChainedDialPreservesPacketBoundaries(t *testing.T) {
	reader, writer := pipe.New()
	pipeConn := cnc.NewConnection(cnc.ConnectionOutputMulti(reader))
	conn, err := (UDPDialer{pipeDialer{conn: &stat.CounterConnection{Connection: pipeConn}}}).DialContext(context.Background(), "udp", M.ParseSocksaddr("edge.test:444"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = writer.WriteMultiBuffer(buf.MultiBuffer{buf.FromBytes([]byte("one")), buf.FromBytes([]byte("two"))}); err != nil {
		t.Fatal(err)
	}
	var data [64]byte
	for _, want := range []string{"one", "two"} {
		n, err := conn.Read(data[:])
		if err != nil || string(data[:n]) != want {
			t.Fatalf("got %q, %v; want %q", data[:n], err, want)
		}
	}
}
