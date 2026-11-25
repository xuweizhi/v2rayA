package v2ray

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/v2rayA/v2rayA/common/netTools"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

const (
	connectivityCheckInterval = 15 * time.Second
	connectivityCheckTimeout  = 5 * time.Second
	// connectivityBackoffBase is the initial retry delay after the first consecutive
	// probe failure (30 s).  Subsequent failures double the delay up to
	// connectivityBackoffMax to avoid a rapid stop→start oscillation loop.
	connectivityBackoffBase = 30 * time.Second
	// connectivityBackoffMax caps the exponential backoff delay.
	connectivityBackoffMax = 120 * time.Second
)

// connectivityStartupDelay is the initial wait before the first connectivity
// probe.  On Windows it is larger to accommodate wintun driver initialisation.
var connectivityStartupDelay time.Duration

// connectivityProbeForSetting allows tests to override connectivity probing.
var connectivityProbeForSetting = probeConnectivityForSetting

func init() {
	connectivityStartupDelay = 5 * time.Second
	if runtime.GOOS == "windows" {
		connectivityStartupDelay = 15 * time.Second
	}
}

// hasActivePhysicalInterface checks whether at least one non-loopback, non-TUN
// network interface is up and has a routable unicast address. Used in TProxy
// and Redirect modes so we do not dial dokodemo-door on 127.0.0.1:52345 (which
// would trigger Xray isLoopBack rejections and cannot detect physical network loss).
func hasActivePhysicalInterface() bool {
	interfaces, err := net.Interfaces()
	if err != nil {
		return true
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if runtime.GOOS == "linux" && iface.Flags&net.FlagRunning == 0 {
			continue
		}
		ifName := strings.ToLower(iface.Name)
		if strings.HasPrefix(ifName, "tun") || strings.HasPrefix(ifName, "utun") || strings.HasPrefix(ifName, "tap") || strings.HasPrefix(ifName, "veth") || strings.HasPrefix(ifName, "docker") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP == nil {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				continue
			}
			if v4 := ip.To4(); v4 != nil && v4[0] == 169 && v4[1] == 254 {
				continue
			}
			return true
		}
	}
	return false
}

// Probe the actual remote node endpoints. TinyTun excludes these addresses;
// on Linux the bypass mark also keeps traffic out of TProxy and Redirect.
func probeConnectivityForSetting(t *Template) bool {
	if !hasActivePhysicalInterface() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectivityCheckTimeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: time.Second}
	if runtime.GOOS == "linux" && t.Setting.TransparentType != configure.TransparentTun {
		dialer.Control = netTools.DirectSocketControl
	}
	dialer.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Control: dialer.Control}).DialContext(ctx, network, addr)
	}}
	tried := false
	udpRoute := false
	seen := map[string]bool{}
	for _, tag := range t.OutboundTags {
		info := t.serverInfoMap[tag]
		if info == nil || info.Info == nil {
			continue
		}
		host := info.Info.GetHostname()
		ip := net.ParseIP(host)
		if host == "" || strings.EqualFold(host, "localhost") || ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
			continue
		}
		address := net.JoinHostPort(host, strconv.Itoa(info.Info.GetPort()))
		if seen[address] {
			continue
		}
		seen[address], tried = true, true
		c, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			c.Close()
			return true
		}
		// Even a UDP-only node can return a TCP reset. That proves a
		// responsive remote host without requiring a TCP service.
		if errors.Is(err, syscall.ECONNREFUSED) {
			return true
		}
		// A filtered TCP port on a UDP-only node is inconclusive. Check that
		// its actual UDP endpoint still has an outbound route.
		if usesUDPTransport(info.Info) {
			if conn, err := dialer.DialContext(ctx, "udp", address); err == nil {
				udpRoute = true
				conn.Close()
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	// Local-only tunnels have no external endpoint to measure.
	return udpRoute || !tried
}

func usesUDPTransport(node serverObj.ServerObj) bool {
	switch n := node.(type) {
	case *serverObj.Hysteria2, *serverObj.Juicity, *serverObj.Tuic, *serverObj.WireGuard:
		return true
	case *serverObj.V2Ray:
		return n.Net == "quic" || n.Net == "kcp" || n.Net == "mkcp"
	case *serverObj.Mieru:
		return strings.EqualFold(n.Transport, "udp")
	}
	return false
}

// connectivityBackoffDelay returns the delay to wait after consecutiveFailures
// consecutive probe failures, using exponential backoff capped at
// connectivityBackoffMax.
func connectivityBackoffDelay(consecutiveFailures int) time.Duration {
	delay := connectivityBackoffBase
	for i := 1; i < consecutiveFailures; i++ {
		delay *= 2
		if delay >= connectivityBackoffMax {
			return connectivityBackoffMax
		}
	}
	return delay
}

func (m *CoreProcessManager) startConnectivityMonitor(t *Template, expectedGen uint64, expectedProc *Process) {
	if t == nil || t.Setting == nil || !IsTransparentOn(t.Setting) || t.Setting.TransparentType == configure.TransparentSystemProxy {
		return
	}

	m.mu.Lock()
	if m.testing || m.p == nil || m.startGen != expectedGen || (expectedProc != nil && m.p != expectedProc) {
		m.mu.Unlock()
		return
	}
	m.stopConnectivityMonitorLocked()
	stopCh := make(chan struct{})
	m.connectivityStop = stopCh
	proc := m.p
	m.mu.Unlock()

	go m.connectivityLoop(stopCh, t, expectedGen, proc)
}

func (m *CoreProcessManager) connectivityLoop(stopCh chan struct{}, t *Template, expectedGen uint64, expectedProc *Process) {
	// Wait for the transparent proxy (e.g. TinyTun) to fully initialize before
	// the first connectivity check.  Probing too early on Windows can yield a
	// false network-unavailable result while the wintun driver is still setting
	// up routes, which would stop TinyTun prematurely and leave the frontend
	// stuck on "检测中" (Checking).
	select {
	case <-stopCh:
		return
	case <-time.After(connectivityStartupDelay):
	}

	failureCount := 0
	for {
		stop, healthy := m.syncConnectivityStateForGen(t, expectedGen, expectedProc)
		if stop {
			return
		}

		var delay time.Duration
		if !healthy {
			failureCount++
			delay = connectivityBackoffDelay(failureCount)
		} else {
			failureCount = 0
			delay = connectivityCheckInterval
		}

		select {
		case <-stopCh:
			return
		case <-time.After(delay):
		}
	}
}

// syncConnectivityState checks and updates the transparent proxy connectivity
// state for the manager's current generation.
func (m *CoreProcessManager) syncConnectivityState(t *Template) (stop bool, healthy bool) {
	m.mu.Lock()
	gen := m.startGen
	proc := m.p
	m.mu.Unlock()
	return m.syncConnectivityStateForGen(t, gen, proc)
}

// syncConnectivityStateForGen checks and updates the transparent proxy
// connectivity state for a specific start generation. It returns (stop, healthy):
// stop=true means the monitor goroutine should exit; healthy=true means
// connectivity is healthy and rules are active.
func (m *CoreProcessManager) syncConnectivityStateForGen(t *Template, expectedGen uint64, expectedProc *Process) (stop bool, healthy bool) {
	if t == nil || t.Setting == nil {
		return true, false
	}

	m.mu.Lock()
	if m.p == nil || m.testing || m.startGen != expectedGen || (expectedProc != nil && m.p != expectedProc) {
		m.mu.Unlock()
		return true, false
	}
	setting := t.Setting
	m.mu.Unlock()
	if setting.TransparentType == configure.TransparentSystemProxy || !IsTransparentOn(setting) {
		return false, true
	}
	online := connectivityProbeForSetting(t)
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if m.testing || m.startGen != expectedGen || m.p != expectedProc {
		m.mu.Unlock()
		return true, false
	}
	paused := m.networkPaused
	m.mu.Unlock()
	if paused == !online {
		return false, online
	}
	if online {
		if err := m.setupTransparentProxy(setting, t); err != nil {
			log.Warn("failed to resume transparent proxy after network recovery: %v", err)
			return false, false
		}
	} else {
		deleteTransparentProxyRulesKeepSystemProxyFn()
	}
	m.mu.Lock()
	m.networkPaused = !online
	m.mu.Unlock()
	ApiFeed.ProductMessage("running_state", map[string]interface{}{"running": online, "networkPaused": !online})
	return false, online
}
