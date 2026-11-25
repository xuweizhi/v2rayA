package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
)

// xrayDispatcherAdapter implements dns.RouteDispatcher using xray-core's
// internal routing dispatcher. This allows the DNS module to route queries
// through xray-core's proxy outbounds (VMESS, VLESS, Trojan, etc.) just like
// xray-core's built-in DNS module does.
//
// It sets session.ContextWithInbound on the dispatch context so that xray's
// routing engine selects the appropriate outbound based on routing rules.
type xrayDispatcherAdapter struct {
	dispatcher routing.Dispatcher
}

// newXrayDispatcherAdapter creates a new adapter from an xray core instance.
func newXrayDispatcherAdapter(srv *xcore.Instance) (*xrayDispatcherAdapter, error) {
	var dispatcher routing.Dispatcher
	if err := srv.RequireFeatures(func(d routing.Dispatcher) {
		dispatcher = d
	}, false); err != nil {
		return nil, fmt.Errorf("xray dispatcher not available: %w", err)
	}
	return &xrayDispatcherAdapter{dispatcher: dispatcher}, nil
}

// Dispatch implements dns.RouteDispatcher.
// It creates a TCP connection to addr through xray-core's routing engine.
// The proxyTag is used to set the inbound tag on the context, which xray's
// routing rules use to determine the outbound (like xray-core's DNS module).
func (a *xrayDispatcherAdapter) Dispatch(ctx context.Context, network, addr, proxyTag string) (net.Conn, error) {
	// Parse the destination address.
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		portStr = "53"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 53
	}

	var dest xnet.Destination
	switch strings.ToLower(network) {
	case "tcp", "tcp4", "tcp6":
		dest = xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(port))
	case "udp", "udp4", "udp6":
		dest = xnet.UDPDestination(xnet.ParseAddress(host), xnet.Port(port))
	default:
		dest = xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(port))
	}

	// Set the inbound tag on the context so xray's routing engine
	// selects the appropriate outbound (like xray-core's DNS module).
	// For "direct", no special tag is needed (uses default routing).
	dnsCtx := ctx
	if proxyTag != "" && proxyTag != "direct" {
		tag := "v2raya-dns-" + base64.RawURLEncoding.EncodeToString([]byte(proxyTag))
		dnsCtx = session.ContextWithInbound(dnsCtx, &session.Inbound{Tag: tag})
	}
	// Mark as DNS traffic to prevent recursive DNS resolution.
	dnsCtx = session.ContextWithContent(dnsCtx, &session.Content{
		Protocol:       "dns",
		SkipDNSResolve: true,
	})

	// Dispatch through xray-core's routing engine.
	link, err := a.dispatcher.Dispatch(dnsCtx, dest)
	if err != nil {
		return nil, fmt.Errorf("xray dispatch: %w", err)
	}

	// Wrap the transport link into a standard net.Conn with real deadline and
	// context-cancellation enforcement (xray's cnc.Connection has no-op SetDeadline).
	rawConn := cnc.NewConnection(
		cnc.ConnectionInputMulti(link.Writer),
		cnc.ConnectionOutputMulti(link.Reader),
	)
	return newDeadlineConn(ctx, rawConn), nil
}

// deadlineConn wraps a net.Conn (such as xray's cnc.Connection whose
// SetDeadline/SetReadDeadline/SetWriteDeadline are no-ops) and actively closes
// the underlying connection when a deadline expires or ctx is cancelled,
// ensuring stalled proxied DNS exchanges abort promptly.
type deadlineConn struct {
	net.Conn
	mu           sync.Mutex
	closed       bool
	timedOut     bool
	readTimer    *time.Timer
	writeTimer   *time.Timer
	stopCtxWatch func() bool
}

func newDeadlineConn(ctx context.Context, inner net.Conn) net.Conn {
	c := &deadlineConn{Conn: inner}
	if ctx != nil {
		c.mu.Lock()
		c.stopCtxWatch = context.AfterFunc(ctx, func() {
			_ = c.closeOnce(ctx.Err() == context.DeadlineExceeded)
		})
		c.mu.Unlock()
	}
	return c
}

func (c *deadlineConn) isTimedOut() bool {
	c.mu.Lock()
	timedOut := c.timedOut
	c.mu.Unlock()
	return timedOut
}

func (c *deadlineConn) closeOnce(timeout bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked(timeout)
}

func (c *deadlineConn) closeLocked(timeout bool) error {
	if c.closed {
		return nil
	}
	c.closed, c.timedOut = true, timeout
	if c.readTimer != nil {
		c.readTimer.Stop()
	}
	if c.writeTimer != nil {
		c.writeTimer.Stop()
	}
	if c.stopCtxWatch != nil {
		c.stopCtxWatch()
	}
	return c.Conn.Close()
}

func (c *deadlineConn) setTimer(slot **time.Timer, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if *slot != nil {
		(*slot).Stop()
		*slot = nil
	}
	if c.closed || t.IsZero() {
		return
	}
	if time.Until(t) <= 0 {
		_ = c.closeLocked(true)
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Until(t), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if *slot == timer {
			_ = c.closeLocked(true)
		}
	})
	*slot = timer
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	if c.isTimedOut() {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.Conn.Read(b)
	if err != nil && c.isTimedOut() {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	if c.isTimedOut() {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.Conn.Write(b)
	if err != nil && c.isTimedOut() {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	c.setTimer(&c.readTimer, t)
	c.setTimer(&c.writeTimer, t)
	return c.Conn.SetDeadline(t)
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	c.setTimer(&c.readTimer, t)
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.setTimer(&c.writeTimer, t)
	return c.Conn.SetWriteDeadline(t)
}

func (c *deadlineConn) Close() error {
	return c.closeOnce(false)
}
