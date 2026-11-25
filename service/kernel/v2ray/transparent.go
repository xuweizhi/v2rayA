package v2ray

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/iptables"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

const defaultDnsListenPort = "52353"

func dnsListenPort(setting *configure.Setting) string {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}
	if setting != nil && setting.DnsListenAddr != "" {
		if _, port, err := net.SplitHostPort(setting.DnsListenAddr); err == nil && port != "" {
			if p, err := strconv.Atoi(port); err == nil && p > 0 && p <= 65535 {
				return port
			}
		}
	}
	return defaultDnsListenPort
}

func dnsRedirectPortsToClean() []string {
	port := dnsListenPort(nil)
	if port == defaultDnsListenPort {
		return []string{port}
	}
	return []string{port, defaultDnsListenPort}
}

func buildDnsRedirectCleanCommands(ports []string) string {
	var b strings.Builder
	for _, port := range ports {
		fmt.Fprintf(&b, `
iptables -w 2 -t nat -D PREROUTING -p udp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
iptables -w 2 -t nat -D PREROUTING -p tcp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
iptables -w 2 -t nat -D OUTPUT -p udp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
iptables -w 2 -t nat -D OUTPUT -p tcp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
`, port, port, port, port)
	}
	b.WriteString(`
iptables -w 2 -t nat -D OUTPUT -m mark --mark 0x80/0x80 -j RETURN 2>/dev/null || true
iptables -w 2 -t nat -D PREROUTING -m mark --mark 0x80/0x80 -j RETURN 2>/dev/null || true
`)
	for _, port := range ports {
		fmt.Fprintf(&b, `
ip6tables -w 2 -t nat -D PREROUTING -p udp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
ip6tables -w 2 -t nat -D PREROUTING -p tcp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
ip6tables -w 2 -t nat -D OUTPUT -p udp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
ip6tables -w 2 -t nat -D OUTPUT -p tcp --dport 53 -j REDIRECT --to-port %s 2>/dev/null || true
`, port, port, port, port)
	}
	b.WriteString(`
ip6tables -w 2 -t nat -D OUTPUT -m mark --mark 0x80/0x80 -j RETURN 2>/dev/null || true
ip6tables -w 2 -t nat -D PREROUTING -m mark --mark 0x80/0x80 -j RETURN 2>/dev/null || true
`)
	return b.String()
}

// cleanupResidualTransparentProxyRules cleans up any residual iptables/nftables rules
// that may have been left behind after an abnormal termination (e.g., kill -9, system crash, panic).
// It uses "2>/dev/null || true" to ensure no errors are raised if rules/chains don't exist.
func cleanupResidualTransparentProxyRules() {
	commands := `
# 清理 DNS_MARK 链（TProxy 模式）
iptables -w 2 -t mangle -F DNS_MARK 2>/dev/null || true
iptables -w 2 -t mangle -D PREROUTING -p udp --dport 53 -j DNS_MARK 2>/dev/null || true
iptables -w 2 -t mangle -D PREROUTING -p tcp --dport 53 -j DNS_MARK 2>/dev/null || true
iptables -w 2 -t mangle -D OUTPUT -p udp --dport 53 -j DNS_MARK 2>/dev/null || true
iptables -w 2 -t mangle -D OUTPUT -p tcp --dport 53 -j DNS_MARK 2>/dev/null || true
iptables -w 2 -t mangle -X DNS_MARK 2>/dev/null || true
# 清理 DNS_REDIRECT 链（Redirect 模式）
iptables -w 2 -t nat -F DNS_REDIRECT 2>/dev/null || true
iptables -w 2 -t nat -D PREROUTING -p udp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
iptables -w 2 -t nat -D PREROUTING -p tcp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
iptables -w 2 -t nat -D OUTPUT -p udp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
iptables -w 2 -t nat -D OUTPUT -p tcp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
iptables -w 2 -t nat -X DNS_REDIRECT 2>/dev/null || true
# IPv6 清理
ip6tables -w 2 -t mangle -F DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t mangle -D PREROUTING -p udp --dport 53 -j DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t mangle -D PREROUTING -p tcp --dport 53 -j DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t mangle -D OUTPUT -p udp --dport 53 -j DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t mangle -D OUTPUT -p tcp --dport 53 -j DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t mangle -X DNS_MARK 2>/dev/null || true
ip6tables -w 2 -t nat -F DNS_REDIRECT 2>/dev/null || true
ip6tables -w 2 -t nat -D PREROUTING -p udp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
ip6tables -w 2 -t nat -D PREROUTING -p tcp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
ip6tables -w 2 -t nat -D OUTPUT -p udp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
ip6tables -w 2 -t nat -D OUTPUT -p tcp --dport 53 -j DNS_REDIRECT 2>/dev/null || true
ip6tables -w 2 -t nat -X DNS_REDIRECT 2>/dev/null || true
` + buildDnsRedirectCleanCommands(dnsRedirectPortsToClean()) + `
# 清理 TProxy 链
iptables -w 2 -t mangle -F TP_OUT 2>/dev/null || true
iptables -w 2 -t mangle -D OUTPUT -j TP_OUT 2>/dev/null || true
iptables -w 2 -t mangle -X TP_OUT 2>/dev/null || true
iptables -w 2 -t mangle -F TP_PRE 2>/dev/null || true
iptables -w 2 -t mangle -D PREROUTING -j TP_PRE 2>/dev/null || true
iptables -w 2 -t mangle -X TP_PRE 2>/dev/null || true
iptables -w 2 -t mangle -F TP_RULE 2>/dev/null || true
iptables -w 2 -t mangle -X TP_RULE 2>/dev/null || true
iptables -w 2 -t mangle -F TP_MARK 2>/dev/null || true
iptables -w 2 -t mangle -X TP_MARK 2>/dev/null || true
# 清理 Redirect 链
iptables -w 2 -t nat -F TP_OUT 2>/dev/null || true
iptables -w 2 -t nat -D OUTPUT -j TP_OUT 2>/dev/null || true
iptables -w 2 -t nat -X TP_OUT 2>/dev/null || true
iptables -w 2 -t nat -F TP_PRE 2>/dev/null || true
iptables -w 2 -t nat -D PREROUTING -j TP_PRE 2>/dev/null || true
iptables -w 2 -t nat -X TP_PRE 2>/dev/null || true
iptables -w 2 -t nat -F TP_RULE 2>/dev/null || true
iptables -w 2 -t nat -X TP_RULE 2>/dev/null || true
iptables -w 2 -F DROP_SPOOFING 2>/dev/null || true
iptables -w 2 -D INPUT -j DROP_SPOOFING 2>/dev/null || true
iptables -w 2 -D FORWARD -j DROP_SPOOFING 2>/dev/null || true
iptables -w 2 -X DROP_SPOOFING 2>/dev/null || true
ip rule del fwmark 0x40/0xc0 table 100 2>/dev/null || true
ip route del local 0.0.0.0/0 dev lo table 100 2>/dev/null || true
nft delete table inet v2raya 2>/dev/null || true
`
	_ = (iptables.Setter{Cmds: commands}).Run(false)
}

// cleanDnsRedirectRules removes the direct nat OUTPUT/PREROUTING DNS
// redirect rules and the fwmark 0x80 exemption rules that
// writeTransparentProxyRules adds with -A/-I. Tproxy/Redirect
// GetCleanCommands() only clean their own chains, so without this the
// rules survive a normal proxy stop: local DNS queries keep being hijacked,
// and once the DNS module exits they hit a dead port and DNS fails.
// Idempotent (2>/dev/null || true) and executed through iptables.Setter so
// Docker iptables-legacy and iptables-nft rewriting is honored.
func cleanDnsRedirectRules() {
	_ = iptables.CleanDNSRedirect().Run(false)
	commands := buildDnsRedirectCleanCommands(dnsRedirectPortsToClean())
	_ = (iptables.Setter{Cmds: commands}).Run(false)
}

func deleteTransparentProxyRulesKeepSystemProxy() {
	stopTinyTun()
	iptables.CloseWatcher()
	if !conf.GetEnvironmentConfig().Lite {
		removeResolvHijacker()
		cleanDnsRedirectRules()
		iptables.Tproxy.GetCleanCommands().Run(false)
		iptables.Redirect.GetCleanCommands().Run(false)
		iptables.DropSpoofing.GetCleanCommands().Run(false)
	}
	time.Sleep(30 * time.Millisecond)
}

func deleteTransparentProxyRules() {
	deleteTransparentProxyRulesKeepSystemProxy()
	iptables.SystemProxy.GetCleanCommands().Run(false)
}

func extractDnsListenAddr(tmpl *Template) string {
	if tmpl != nil && len(tmpl.DnsModuleConfig) > 0 {
		var spec dnsModuleListenerSpec
		if err := json.Unmarshal(tmpl.DnsModuleConfig, &spec); err == nil && spec.Listener.ListenAddr != "" {
			return spec.Listener.ListenAddr
		}
	}
	if tmpl != nil && tmpl.Setting != nil && tmpl.Setting.DnsListenAddr != "" {
		return tmpl.Setting.DnsListenAddr
	}
	return "127.0.0.1:" + defaultDnsListenPort
}

func writeTransparentProxyRules(tmpl *Template) (err error) {
	var setting *configure.Setting
	if tmpl != nil {
		setting = tmpl.Setting
	}
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}

	defer func() {
		if err != nil {
			log.Warn("writeTransparentProxyRules: %v", err)
			if setting.TransparentType == configure.TransparentSystemProxy {
				_ = iptables.SystemProxy.GetCleanCommands().Run(false)
			} else {
				deleteTransparentProxyRulesKeepSystemProxy()
			}
		}
	}()

	// v2raya-core 进程内启动 DNS 模块，
	// v2rayA 负责在透明代理时应用 iptables/nftables 规则将 53 端口流量重定向到 DNS 模块端口。
	// 等待 DNS 模块就绪后应用防火墙规则。
	if tmpl != nil && tmpl.DnsModuleConfig != nil && setting.TransparentType != configure.TransparentSystemProxy {
		dnsAddr := extractDnsListenAddr(tmpl)
		if err := waitForDnsPort(dnsAddr, 5*time.Second); err != nil {
			return fmt.Errorf("dns module not ready: %w", err)
		}
		log.Trace("DNS module is ready on %s, setting up transparent proxy rules", dnsAddr)
	}

	if setting.TransparentType == configure.TransparentSystemProxy {
		if err = iptables.SystemProxy.GetSetupCommands().Run(true); err != nil {
			return fmt.Errorf("not support \"system proxy\" mode of transparent proxy: %w", err)
		}
		return nil
	}

	if !conf.GetEnvironmentConfig().Lite {
		cleanupResidualTransparentProxyRules()
	}

	switch setting.TransparentType {
	case configure.TransparentTun:
		return startTinyTun(tmpl)
	case configure.TransparentTproxy:
		if err = iptables.Tproxy.GetSetupCommands().Run(true); err != nil {
			if strings.Contains(err.Error(), "TPROXY") && strings.Contains(err.Error(), "No chain") {
				err = fmt.Errorf("you does not compile xt_TPROXY in kernel")
			}
			return fmt.Errorf("not support \"tproxy\" mode of transparent proxy: %w", err)
		}
		iptables.SetWatcher(iptables.Tproxy)
	case configure.TransparentRedirect:
		if err = iptables.Redirect.GetSetupCommands().Run(true); err != nil {
			return fmt.Errorf("not support \"redirect\" mode of transparent proxy: %w", err)
		}
		iptables.SetWatcher(iptables.Redirect)
	default:
		return fmt.Errorf("undefined \"%v\" mode of transparent proxy", setting.TransparentType)
	}

	if shouldLocalDnsListen(setting) && tmpl != nil && len(tmpl.DnsModuleConfig) != 0 {
		var spec dnsModuleListenerSpec
		if err := json.Unmarshal(tmpl.DnsModuleConfig, &spec); err != nil {
			return err
		}
		rules := iptables.DNSRedirect(extractDnsListenAddr(tmpl), setting.PortSharing, isIPv6Supported(), iptables.IsNft(), iptables.GetExcludedInterfaces())
		if err := rules.Run(true); err != nil {
			return fmt.Errorf("configure DNS interception: %w", err)
		}
		for _, addr := range append([]string{spec.Listener.ListenAddr}, spec.Listener.ExtraListenAddrs...) {
			if addr == "127.2.0.17:53" {
				if err := resetResolvHijacker(); err != nil {
					log.Warn("could not take over system resolver: %v", err)
				}
				break
			}
		}
	}
	return nil
}

func IsTransparentOn(setting *configure.Setting) bool {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}
	if setting.Transparent == configure.TransparentClose {
		return false
	}
	if conf.GetEnvironmentConfig().Lite &&
		(setting.TransparentType == configure.TransparentTproxy ||
			setting.TransparentType == configure.TransparentRedirect) {
		return false
	}
	return true
}

func normalizeDnsDialAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}

// waitForDnsPort polls the DNS module's TCP listening port until it's ready or a timeout expires.
// core/dns listens on both TCP and UDP, and TCP dial (unlike connectionless UDP dial) reliably
// verifies that the listener has actually bound and is accepting connections.
func waitForDnsPort(addr string, timeout time.Duration) error {
	dialAddr := normalizeDnsDialAddr(addr)
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		dialTimeout := 500 * time.Millisecond
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		conn, err := net.DialTimeout("tcp", dialAddr, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		sleepDur := 100 * time.Millisecond
		if rem := time.Until(deadline); rem < sleepDur {
			if rem <= 0 {
				break
			}
			sleepDur = rem
		}
		time.Sleep(sleepDur)
	}
	return fmt.Errorf("dns port %s not reachable within %v", addr, timeout)
}
