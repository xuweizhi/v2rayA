package v2ray

import (
	"testing"
	"time"

	statspb "github.com/v2fly/v2ray-core/v5/app/stats/command"
)

func TestSumInboundTraffic(t *testing.T) {
	stats := []*statspb.Stat{
		{Name: "inbound>>>socks>>>traffic>>>uplink", Value: 1200},
		{Name: "inbound>>>socks>>>traffic>>>downlink", Value: 3400},
		{Name: "inbound>>>http>>>traffic>>>uplink", Value: 500},
		{Name: "inbound>>>api-in>>>traffic>>>downlink", Value: 9999},
		{Name: "inbound>>>api-in_ipv4>>>traffic>>>uplink", Value: 9999},
		{Name: "inbound>>>api-in_ipv6>>>traffic>>>downlink", Value: 9999},
		{Name: "outbound>>>proxy>>>traffic>>>uplink", Value: 8888},
		{Name: "inbound>>>http>>>traffic>>>downlink", Value: -1},
	}

	got := sumInboundTraffic(stats)
	if got.Uplink != 1700 || got.Downlink != 3400 {
		t.Fatalf("unexpected totals: %+v", got)
	}
}

func TestTrafficMessage(t *testing.T) {
	got := trafficMessage(
		TrafficTotals{Uplink: 1000, Downlink: 2000},
		TrafficTotals{Uplink: 2500, Downlink: 5000},
		1500*time.Millisecond,
	)
	if got.Up != 1000 || got.Down != 2000 || got.UpTotal != 2500 || got.DownTotal != 5000 {
		t.Fatalf("unexpected traffic message: %+v", got)
	}
}

func TestTrafficMessageHandlesCounterReset(t *testing.T) {
	got := trafficMessage(
		TrafficTotals{Uplink: 5000, Downlink: 6000},
		TrafficTotals{Uplink: 100, Downlink: 200},
		time.Second,
	)
	if got.Up != 0 || got.Down != 0 || got.UpTotal != 100 || got.DownTotal != 200 {
		t.Fatalf("unexpected reset message: %+v", got)
	}
}
