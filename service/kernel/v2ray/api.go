package v2ray

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/devfeel/mapper"
	"github.com/gin-gonic/gin"
	"github.com/v2fly/v2ray-core/v5/app/observatory"
	pb "github.com/v2fly/v2ray-core/v5/app/observatory/command"
	statspb "github.com/v2fly/v2ray-core/v5/app/stats/command"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ApiProducts = []string{
		"observatory",
		"running_state",
		"traffic",
	}
	ApiFeed *Feed
)

const (
	ApiFeedBoxSize  = 10
	ApiFeedInterval = 1 * time.Second
)

type OutboundStatus struct {
	Alive bool  `json:"alive"`
	Delay int64 `json:"delay"`
	//LastErrorReason string           `json:"last_error_reason"`
	OutboundTag  string           `json:"outbound_tag"`
	Which        *configure.Which `json:"which"`
	LastSeenTime int64            `json:"last_seen_time"`
	LastTryTime  int64            `json:"last_try_time"`
}

func init() {
	mapper.Register(&observatory.OutboundStatus{})

	ApiFeed = NewSubscriptions(ApiFeedBoxSize)
	for _, product := range ApiProducts {
		ApiFeed.RegisterProduct(product)
	}
}

type ObservatoryResp struct {
	OutboundName string
	Resp         *pb.GetOutboundStatusResponse
}

type TrafficTotals struct {
	Uplink   int64 `json:"upTotal"`
	Downlink int64 `json:"downTotal"`
}

type TrafficMessage struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpTotal   int64 `json:"upTotal"`
	DownTotal int64 `json:"downTotal"`
}

func sumInboundTraffic(stats []*statspb.Stat) TrafficTotals {
	var totals TrafficTotals
	for _, stat := range stats {
		if stat == nil || stat.GetValue() < 0 {
			continue
		}
		parts := strings.Split(stat.GetName(), ">>>")
		if len(parts) != 4 || parts[0] != "inbound" || strings.HasPrefix(parts[1], "api-in") || parts[2] != "traffic" {
			continue
		}
		switch parts[3] {
		case "uplink":
			totals.Uplink += stat.GetValue()
		case "downlink":
			totals.Downlink += stat.GetValue()
		}
	}
	return totals
}

func trafficMessage(previous, current TrafficTotals, elapsed time.Duration) TrafficMessage {
	msg := TrafficMessage{UpTotal: current.Uplink, DownTotal: current.Downlink}
	if elapsed <= 0 || current.Uplink < previous.Uplink || current.Downlink < previous.Downlink {
		return msg
	}
	seconds := elapsed.Seconds()
	msg.Up = int64(float64(current.Uplink-previous.Uplink) / seconds)
	msg.Down = int64(float64(current.Downlink-previous.Downlink) / seconds)
	return msg
}

func getTrafficTotals(conn *grpc.ClientConn) (TrafficTotals, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ApiFeedInterval)
	defer cancel()
	resp, err := statspb.NewStatsServiceClient(conn).QueryStats(ctx, &statspb.QueryStatsRequest{
		Pattern: "inbound>>>",
	})
	if err != nil {
		return TrafficTotals{}, err
	}
	return sumInboundTraffic(resp.GetStat()), nil
}

// TrafficProducer publishes the aggregate inbound upload/download rate once a
// second. v2raya_core exposes a v2ray-compatible StatsService endpoint, so the
// existing v2ray protobuf client can be used without coupling the service to
// the embedded Xray module.
func TrafficProducer(apiPort int) (closeFunc func()) {
	closed := make(chan struct{})
	go func() {
		const product = "traffic"
		var conn *grpc.ClientConn
		var previous TrafficTotals
		var previousAt time.Time
		hasPrevious := false

		for {
			select {
			case <-closed:
				if conn != nil {
					_ = conn.Close()
				}
				return
			case <-time.After(ApiFeedInterval):
			}

			if ProcessManager.Process() == nil {
				hasPrevious = false
				if conn != nil {
					_ = conn.Close()
					conn = nil
				}
				continue
			}

			if conn == nil {
				ctx, cancel := context.WithTimeout(context.Background(), ApiFeedInterval)
				newConn, err := grpc.DialContext(
					ctx,
					net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
					grpc.WithInsecure(),
					grpc.WithBlock(),
				)
				cancel()
				if err != nil {
					continue
				}
				conn = newConn
			}

			current, err := getTrafficTotals(conn)
			if err != nil {
				if status.Code(err) == codes.Unavailable {
					_ = conn.Close()
					conn = nil
				}
				hasPrevious = false
				continue
			}

			now := time.Now()
			if hasPrevious {
				ApiFeed.ProductMessage(product, trafficMessage(previous, current, now.Sub(previousAt)))
			} else {
				ApiFeed.ProductMessage(product, TrafficMessage{UpTotal: current.Uplink, DownTotal: current.Downlink})
			}
			previous = current
			previousAt = now
			hasPrevious = true
		}
	}()
	return func() {
		close(closed)
	}
}

func getObservatoryResponses(conn *grpc.ClientConn, observatoryTags []string) (r []ObservatoryResp, err error) {
	c := pb.NewObservatoryServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if len(observatoryTags) == 0 {
		observatoryTags = append(observatoryTags, "")
	}
	for _, tag := range observatoryTags {
		resp, err := c.GetOutboundStatus(ctx, &pb.GetOutboundStatusRequest{
			Tag: tag,
		})
		if err != nil {
			return nil, err
		}
		r = append(r, ObservatoryResp{OutboundName: tag, Resp: resp})
	}
	return r, nil
}

// ObservatoryProducer monitors outbound status via gRPC API and publishes to ApiFeed.
// This function is only compatible with v2ray-core v5+ API structure.
// For xray-core, this function should not be called as it uses different gRPC services.
func ObservatoryProducer(apiPort int, observatoryTags []string) (closeFunc func()) {
	closed := make(chan struct{})
	go func() {
		const product = "observatory"
		var conn *grpc.ClientConn
	nextLoop:
		for {
			select {
			case <-closed:
				return
			default:
			}
			p := ProcessManager.Process()
			if p == nil {
				time.Sleep(ApiFeedInterval)
				continue
			}
			// Set up a connection to the server.
			if conn == nil {
				ctx, cancel := context.WithTimeout(context.Background(), ApiFeedInterval)
				defer cancel()
				c, err := grpc.DialContext(
					ctx,
					net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort)),
					grpc.WithInsecure(),
					grpc.WithBlock(),
				)
				if err != nil {
					log.Warn("ObservatoryProducer: did not connect: %v", err)
					continue nextLoop
				}
				defer c.Close()
				conn = c
			}
			resps, err := getObservatoryResponses(conn, observatoryTags)
			if err != nil {
				if status.Code(err) == codes.Unavailable {
					// the connection is reliable, and reconnect
					conn = nil
					continue nextLoop
				}
				log.Warn("ObservatoryProducer: %v", err)
			} else {
				css := configure.GetConnectedServers()
				for _, r := range resps {
					outboundStatus := r.Resp.GetStatus().GetStatus()
					os := make([]OutboundStatus, len(outboundStatus))
					for i := range outboundStatus {
						_ = mapper.AutoMapper(outboundStatus[i], &os[i])
						index := p.tag2WhichIndex[os[i].OutboundTag]
						if index >= css.Len() {
							continue nextLoop
						}
						os[i].Which = css.Get()[index]
						var w []configure.Which
						for _, v := range css.Get() {
							w = append(w, *v)
						}
					}
					msg := gin.H{
						"outboundName":   r.OutboundName,
						"outboundStatus": os,
					}
					ApiFeed.ProductMessage(product, msg)
				}
			}
			time.Sleep(ApiFeedInterval)
		}
	}()
	return func() {
		close(closed)
	}
}
