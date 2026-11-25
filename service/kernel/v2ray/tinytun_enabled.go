//go:build tinytun

package v2ray

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"github.com/v2rayA/v2rayA/kernel/v2ray/where"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// This file supports TinyTun v0.0.2-alpha.7.

// tinytunLogConf represents the log settings in TinyTun config.
type tinytunLogConf struct {
	Loglevel      string `yaml:"loglevel"`
	HideTimestamp bool   `yaml:"hide_timestamp"`
}

// tinytunTunConf represents the TUN interface settings in TinyTun v0.0.2-alpha.7 config.
type tinytunTunConf struct {
	Name       string `yaml:"name"`
	IP         string `yaml:"ip"`
	Netmask    string `yaml:"netmask"`
	Ipv6Mode   string `yaml:"ipv6_mode,omitempty"`
	Ipv6       string `yaml:"ipv6,omitempty"`
	Ipv6Prefix int    `yaml:"ipv6_prefix,omitempty"`
	AutoRoute  bool   `yaml:"auto_route"`
	MTU        int    `yaml:"mtu,omitempty"`
}

// tinytunSocks5Conf represents the SOCKS5 proxy settings in TinyTun config.
type tinytunSocks5Conf struct {
	Name     string  `yaml:"name,omitempty"`
	Address  string  `yaml:"address"`
	Username *string `yaml:"username,omitempty"`
	Password *string `yaml:"password,omitempty"`
}

// tinytunDnsGroupConf is a named group of upstream DNS servers.
// servers contains address strings:
//   - For udp/tcp/dot/doq: "host:port" (e.g. "8.8.8.8:53" or "dns.google:853")
//   - For doh: HTTPS endpoint URLs (e.g. "https://dns.google/dns-query")
//
// upstream is "direct" or "proxy" (DNS-over-TCP via SOCKS5).
// strategy is "concurrent", "sequential", or "random".
// protocol is "udp" (default), "tcp", "dot", "doh", or "doq" (TinyTun v0.0.1-beta.4+).
// sni overrides the TLS server name for dot/doq; if omitted the hostname is used.
type tinytunDnsGroupConf struct {
	Name     string   `yaml:"name"`
	Servers  []string `yaml:"servers"`
	Strategy string   `yaml:"strategy,omitempty"`
	Upstream string   `yaml:"upstream,omitempty"`
	Protocol string   `yaml:"protocol,omitempty"`
	Sni      *string  `yaml:"sni,omitempty"`
}

// tinytunDnsRoutingConf is the DNS routing configuration.
// Rules use TinyTun's compact YAML syntax: "match(<condition>),<action>"
// Supported conditions: geosite:<tag>, domain:<fqdn>, suffix:<domain>, keyword:<word>, regex:<pattern>, *
// Supported actions: <group-name> (e.g. "direct", "proxy") | "reject"
type tinytunDnsRoutingConf struct {
	Rules         []string `yaml:"rules,omitempty"`
	FallbackGroup string   `yaml:"fallback_group"`
	GeositeFile   string   `yaml:"geosite_file,omitempty"`
	EnableCache   bool     `yaml:"enable_cache"`
	CacheCapacity int      `yaml:"cache_capacity"`
}

// tinytunDnsHijackConf represents Linux-only DNS hijack settings in TinyTun.
// The actual Linux data plane/backend selection, including eBPF internals,
// is handled by TinyTun itself and does not require extra v2rayA YAML fields.
type tinytunDnsHijackConf struct {
	Enabled    bool `yaml:"enabled"`
	Mark       int  `yaml:"mark"`
	TableID    int  `yaml:"table_id"`
	CaptureTCP bool `yaml:"capture_tcp"`
}

// tinytunDnsConf represents the DNS settings in TinyTun v0.0.2-alpha.7 config.
// TinyTun handles DNS routing natively; v2ray is used only for traffic forwarding.
type tinytunDnsConf struct {
	Groups     []tinytunDnsGroupConf `yaml:"groups"`
	ListenPort int                   `yaml:"listen_port"`
	TimeoutMs  int                   `yaml:"timeout_ms"`
	Hijack     tinytunDnsHijackConf  `yaml:"hijack"`
	Routing    tinytunDnsRoutingConf `yaml:"routing"`
}

// tinytunFilteringConf represents the filtering settings in TinyTun config.
type tinytunFilteringConf struct {
	SkipIPs          []string `yaml:"skip_ips,omitempty"`
	SkipNetworks     []string `yaml:"skip_networks,omitempty"`
	BlockPorts       []int    `yaml:"block_ports,omitempty"`
	AllowPorts       []int    `yaml:"allow_ports,omitempty"`
	ExcludeProcesses []string `yaml:"exclude_processes,omitempty"`
}

// tinytunRouteConf represents the route settings in TinyTun config.
type tinytunRouteConf struct {
	AutoDetectInterface bool    `yaml:"auto_detect_interface"`
	DefaultInterface    *string `yaml:"default_interface,omitempty"`
}

// tinytunConfig is the top-level TinyTun YAML configuration.
type tinytunConfig struct {
	Log       tinytunLogConf       `yaml:"log"`
	Tun       tinytunTunConf       `yaml:"tun"`
	Socks5    tinytunSocks5Conf    `yaml:"socks5"`
	Proxies   []tinytunSocks5Conf  `yaml:"proxies,omitempty"`
	DNS       tinytunDnsConf       `yaml:"dns"`
	Filtering tinytunFilteringConf `yaml:"filtering"`
	Route     tinytunRouteConf     `yaml:"route"`
}

const (
	tinytunBinName        = "tinytun"
	tinytunConfigFileName = "tinytun.yaml"
	tinytunTunIPv4        = "198.18.0.1"
	tinytunTunNetmask     = "255.255.255.255"
	tinytunTunIPv6        = "fd00::1"
	tinytunTunIPv6Prefix  = 128
	tinytunDNSHijackMark  = 0x1
	tinytunDNSHijackTable = 100
	// tinytunSocksPort is the SOCKS5 port in v2ray dedicated for TinyTun traffic.
	// This matches the "transparent" inbound added in setInbound for TransparentTun.
	tinytunSocksPort = 52345
)

var tinytunDefaultSkipNetworks = []string{
	"127.0.0.0/8",
	"169.254.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

// tinyTunState tracks the running TinyTun process.
var tinyTunState struct {
	cancel   context.CancelFunc
	setting  *configure.Setting
	done     chan struct{} // closed by the monitor goroutine when cmd.Wait() returns
	mu       sync.Mutex
	stopping int32 // atomic; 1 while stopTinyTun is in progress
}

// GetTinyTunBinPath returns the path to the TinyTun binary.
// It first checks the --tinytun-bin / V2RAYA_TINYTUN_BIN configuration,
// then searches PATH and the current working directory.
func GetTinyTunBinPath() (string, error) {
	if binPath := conf.GetEnvironmentConfig().TinyTunBin; binPath != "" {
		return binPath, nil
	}
	return getTinyTunBinPathAuto()
}

func getTinyTunBinPathAuto() (string, error) {
	target := tinytunBinName
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(target), ".exe") {
		target += ".exe"
	}
	// Search in the same directory as the executable
	if exe, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(exe), target)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	// Search in PATH
	if path, err := exec.LookPath(target); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("tinytun binary not found: please install tinytun or use --tinytun-bin to specify its path")
}

// resolveHostToIPs resolves a hostname to a list of IP strings.
// If the input is already an IP address it is returned as-is.
func resolveHostToIPs(hostname string) ([]string, error) {
	if ip := net.ParseIP(hostname); ip != nil {
		return []string{ip.String()}, nil
	}
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %v: %w", hostname, err)
	}
	return addrs, nil
}

func isResolvableHost(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.Contains(host, "/") {
		return false
	}
	for _, prefix := range []string{"geoip:", "ext:", "regexp:", "domain:", "keyword:", "full:", "geosite:"} {
		if strings.HasPrefix(host, prefix) {
			return false
		}
	}
	return true
}

// collectNodeIPs returns the deduplicated list of IP addresses for all proxy
// nodes referenced by tmpl. Domain-based node addresses are resolved to IPs
// concurrently to reduce latency when many nodes need DNS lookups.
func collectNodeIPs(tmpl *Template) []string {
	// First pass: gather all unique, resolvable hostnames.
	seenHost := make(map[string]struct{})
	var hostnames []string
	addHostname := func(h string) {
		if !isResolvableHost(h) {
			return
		}
		if _, ok := seenHost[h]; ok {
			return
		}
		seenHost[h] = struct{}{}
		hostnames = append(hostnames, h)
	}

	// Source 1: read directly from the connected-server database.
	// This is the most reliable source because it does not depend on whether
	// serverInfoMap was populated (e.g. balancer paths skip serverInfoMap).
	if css := configure.GetConnectedServers(); css != nil {
		for _, cs := range css.Get() {
			sr, err := cs.LocateServerRaw()
			if err != nil {
				log.Warn("tinytun: failed to locate server raw for skip_ips: %v", err)
				continue
			}
			addHostname(sr.ServerObj.GetHostname())
		}
	}

	// Source 2: serverInfoMap in the template (covers cases where the template
	// was constructed from a snapshot that may differ from the live DB state).
	for _, info := range tmpl.serverInfoMap {
		addHostname(info.Info.GetHostname())
	}

	if len(hostnames) == 0 {
		return nil
	}

	// Second pass: resolve all hostnames concurrently.
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		seenIP = make(map[string]struct{})
		result []string
	)
	for _, hostname := range hostnames {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			ips, err := resolveHostToIPs(host)
			if err != nil {
				log.Warn("tinytun: failed to resolve node hostname %v: %v", host, err)
				return
			}
			mu.Lock()
			for _, ip := range ips {
				if _, ok := seenIP[ip]; ok {
					continue
				}
				seenIP[ip] = struct{}{}
				result = append(result, ip)
			}
			mu.Unlock()
		}(hostname)
	}
	wg.Wait()
	return result
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// collectBypassInterfaceNetworks returns the IP network CIDRs of all active
// interfaces whose names match any of the comma-separated glob patterns in
// the patterns string.  It is used to populate skip_networks in the TinyTun
// config so that traffic to these subnets bypasses the TUN proxy.
func collectBypassInterfaceNetworks(patterns string) []string {
	if patterns == "" {
		return nil
	}
	var patternList []string
	for _, p := range strings.Split(patterns, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			patternList = append(patternList, p)
		}
	}
	if len(patternList) == 0 {
		return nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		log.Warn("tinytun: failed to enumerate local interfaces: %v", err)
		return nil
	}

	var networks []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		matched := false
		for _, pattern := range patternList {
			if ok, _ := filepath.Match(pattern, iface.Name); ok {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			log.Warn("tinytun: failed to list addresses for interface %v: %v", iface.Name, err)
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP == nil || ipnet.Mask == nil {
				continue
			}
			networkIP := ipnet.IP.Mask(ipnet.Mask)
			if networkIP == nil {
				continue
			}
			networks = append(networks, (&net.IPNet{IP: networkIP, Mask: ipnet.Mask}).String())
		}
	}
	return dedupeStrings(networks)
}

func parseCustomExcludeProcesses(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', ';', '\t':
			return true
		default:
			return false
		}
	})
	custom := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if base := filepath.Base(p); base != "" && base != "." {
			p = base
		}
		custom = append(custom, p)
	}
	return dedupeStrings(custom)
}

// collectExcludeProcesses returns the process basenames that should be excluded
// from TinyTun proxying to prevent traffic loops.  It dynamically resolves
// v2rayA's own executable and the v2ray/xray core binary; names are only added
// when the path can actually be resolved, so no hardcoded strings are written.
func collectExcludeProcesses(customRaw string) []string {
	var processes []string

	// Exclude v2rayA itself so its own outgoing connections are not captured.
	if exe, err := os.Executable(); err == nil {
		if name := filepath.Base(exe); name != "" && name != "." {
			processes = append(processes, name)
		}
	} else {
		log.Warn("tinytun: failed to resolve own executable for process exclusion: %v", err)
	}

	// Exclude the v2ray/xray core so its proxy-server connections bypass TUN.
	if corePath, err := where.GetV2rayBinPath(); err == nil {
		if name := filepath.Base(corePath); name != "" && name != "." {
			processes = append(processes, name)
		}
	} else {
		log.Warn("tinytun: failed to resolve core binary for process exclusion: %v", err)
	}

	processes = append(processes, parseCustomExcludeProcesses(customRaw)...)

	return dedupeStrings(processes)
}

// domainPatternToYamlRule converts a v2rayA domain pattern and routing action into a TinyTun
// YAML compact routing rule string: "match(<condition>),<action>".
//
// For geosite: patterns, geositePath is set at the routing level (geosite_file); this function
// does NOT embed it inline. For ext:file:tag patterns, the explicit file path is embedded inline.
// action should be a group name (e.g., "direct", "proxy") or "reject".
// Returns ("", false) if the pattern is unsupported.
func domainPatternToYamlRule(pattern, geositePath, action string) (string, bool) {
	var condition string
	switch {
	case strings.HasPrefix(pattern, "geosite:"):
		if geositePath == "" {
			return "", false
		}
		condition = pattern // e.g. "geosite:cn" — geosite_file is set at routing level

	case strings.HasPrefix(pattern, "ext:"):
		// ext:filename.dat:tag — resolve to absolute path and embed inline
		parts := strings.SplitN(strings.TrimPrefix(pattern, "ext:"), ":", 2)
		if len(parts) != 2 {
			return "", false
		}
		filePath, err := asset.GetV2rayLocationAsset(parts[0])
		if err != nil || filePath == "" {
			return "", false
		}
		// Normalize to forward slashes so the path is valid YAML on all platforms.
		filePath = strings.ReplaceAll(filePath, "\\", "/")
		condition = "geosite:" + parts[1] + ":" + filePath

	case strings.HasPrefix(pattern, "full:"):
		condition = "domain:" + strings.TrimPrefix(pattern, "full:")

	case strings.HasPrefix(pattern, "domain:"):
		// In v2rayA, "domain:" means suffix match (the domain itself and all sub-domains).
		condition = "suffix:" + strings.TrimPrefix(pattern, "domain:")

	case strings.HasPrefix(pattern, "suffix:"):
		condition = "suffix:" + strings.TrimPrefix(pattern, "suffix:")

	case strings.HasPrefix(pattern, "keyword:"):
		condition = "keyword:" + strings.TrimPrefix(pattern, "keyword:")

	case strings.HasPrefix(pattern, "regexp:"):
		condition = "regex:" + strings.TrimPrefix(pattern, "regexp:")

	default:
		// Plain domain (no prefix) → suffix match, consistent with v2ray behaviour.
		if net.ParseIP(pattern) == nil && pattern != "" && !strings.Contains(pattern, "/") {
			condition = "suffix:" + pattern
		} else {
			return "", false
		}
	}
	return fmt.Sprintf("match(%s),%s", condition, action), true
}

// TinyTun selects upstreams by group. Keep independently routed resolvers in
// separate groups, even when they share the same outbound.
func buildTinyTunDNSConfigFromRules(rawRules []configure.DnsRule) (tinytunDnsConf, error) {
	geositePath, _ := asset.GetV2rayLocationAsset("geosite.dat")
	geositePath = filepath.ToSlash(geositePath)
	var routingRules []string
	var groups []tinytunDnsGroupConf
	fallbackGroup := ""
	groupsByEndpoint := make(map[[3]string]string)
	for i, rule := range configure.MigrateDnsRules(rawRules) {
		if rule.IP != "" || rule.ClientIP != "" || (rule.QueryType != "" && rule.QueryType != "*") {
			return tinytunDnsConf{}, fmt.Errorf("TinyTun DNS rule %q: IP, client IP and query type filters require the core DNS module", rule.RuleID)
		}
		action := "reject"
		if rule.Action != "reject" && rule.Outbound != "block" {
			upstream := rule.Outbound
			if rule.Action == "bypass" {
				upstream = "direct"
			}
			if upstream != "proxy" && upstream != "direct" {
				return tinytunDnsConf{}, fmt.Errorf("TinyTun DNS rule %q: unsupported outbound %q", rule.RuleID, upstream)
			}
			server := rule.Upstream
			if server == "localhost" {
				server = "127.0.0.1"
			}
			protocol, addr, _, err := parseDNSUpstream(server)
			if err != nil {
				return tinytunDnsConf{}, fmt.Errorf("TinyTun DNS rule %q: %w", rule.RuleID, err)
			}
			switch protocol {
			case "tcp-tls":
				protocol = "dot"
			case "https":
				protocol = "doh"
			case "quic":
				protocol = "doq"
			}
			endpoint := [3]string{protocol, upstream, addr}
			action = groupsByEndpoint[endpoint]
			if action == "" {
				action = fmt.Sprintf("dns-%d", i+1)
				groups = append(groups, tinytunDnsGroupConf{Name: action, Servers: []string{addr}, Upstream: upstream, Protocol: protocol, Strategy: "sequential"})
				groupsByEndpoint[endpoint] = action
			}
		}
		domain := strings.TrimSpace(rule.Domain)
		if domain == "" {
			if action == "reject" {
				routingRules = append(routingRules, "match(*),reject")
			} else {
				fallbackGroup = action
			}
			continue
		}
		for _, pattern := range strings.FieldsFunc(domain, func(r rune) bool { return r == '\n' || r == ',' }) {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			if expression, ok := domainPatternToYamlRule(pattern, geositePath, action); ok {
				routingRules = append(routingRules, expression)
			} else {
				return tinytunDnsConf{}, fmt.Errorf("TinyTun DNS rule %q: unavailable domain matcher %q", rule.RuleID, pattern)
			}
		}
	}
	if fallbackGroup == "" {
		if len(groups) == 0 {
			groups = append(groups, tinytunDnsGroupConf{Name: "proxy", Servers: []string{"1.0.0.1:53"}, Upstream: "proxy", Protocol: "udp"})
		}
		fallbackGroup = groups[0].Name
	}

	return tinytunDnsConf{
		Groups:     groups,
		ListenPort: 53,
		TimeoutMs:  5000,
		Hijack: tinytunDnsHijackConf{
			Enabled:    false,
			Mark:       tinytunDNSHijackMark,
			TableID:    tinytunDNSHijackTable,
			CaptureTCP: true,
		},
		Routing: tinytunDnsRoutingConf{
			Rules:         routingRules,
			FallbackGroup: fallbackGroup,
			GeositeFile:   geositePath,
			EnableCache:   true,
			CacheCapacity: 4096,
		},
	}, nil
}

// generateTinyTunConfig generates a TinyTun YAML config file and returns its path.
func generateTinyTunConfig(tmpl *Template) (string, error) {
	setting := tmpl.Setting
	dns, err := buildTinyTunDNSConfigFromRules(configure.GetDnsRulesNotNil())
	if err != nil {
		return "", err
	}
	skipIPs := dedupeStrings(append([]string{"127.0.0.1", "::1", tinytunTunIPv4}, collectNodeIPs(tmpl)...))
	skipNetworks := dedupeStrings(append(append([]string{}, tinytunDefaultSkipNetworks...), collectBypassInterfaceNetworks(setting.TunBypassInterfaces)...))

	cfg := tinytunConfig{
		Log: tinytunLogConf{
			Loglevel:      setting.LogLevel,
			HideTimestamp: false,
		},
		Tun: tinytunTunConf{
			Name:       "tun0",
			IP:         tinytunTunIPv4,
			Netmask:    tinytunTunNetmask,
			Ipv6Mode:   "auto",
			Ipv6:       tinytunTunIPv6,
			Ipv6Prefix: tinytunTunIPv6Prefix,
			AutoRoute:  setting.TunAutoRoute,
			MTU:        1500,
		},
		Socks5: tinytunSocks5Conf{
			Name:    "proxy",
			Address: fmt.Sprintf("127.0.0.1:%d", tinytunSocksPort),
		},
		Proxies: nil,
		DNS:     dns,
		Filtering: tinytunFilteringConf{
			SkipIPs:          skipIPs,
			SkipNetworks:     skipNetworks,
			BlockPorts:       []int{22, 23, 25, 110, 143},
			AllowPorts:       []int{80, 443, 53},
			ExcludeProcesses: collectExcludeProcesses(setting.TunExcludeProcesses),
		},
		Route: tinytunRouteConf{
			AutoDetectInterface: true,
		},
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tinytun config: %w", err)
	}

	configPath := filepath.Join(conf.GetEnvironmentConfig().Config, tinytunConfigFileName)
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write tinytun config to %v: %w", configPath, err)
	}
	return configPath, nil
}

// shellInfo holds the binary path and argument format for a given shell type.
type shellInfo struct {
	// bin is the path/name of the shell binary.
	bin string
	// scriptFlag is the flag used to pass a script file to the shell.
	// For shells that accept a file path directly (bash, zsh, sh, fish), this is empty and the file is appended.
	// For PowerShell, this is "-File".
	// For cmd, this is "/C".
	scriptFlag string
	// ext is the file extension for the temporary script file.
	ext string
}

// resolveShellInfo maps a shell type string and optional custom path to a shellInfo.
func resolveShellInfo(shellType, shellPath string) (shellInfo, error) {
	// Custom shell: use user-provided path directly; assume POSIX-style (-c)
	if shellType == "custom" || (shellType == "" && shellPath != "") {
		if shellPath == "" {
			return shellInfo{}, fmt.Errorf("custom shell path is empty")
		}
		return shellInfo{bin: shellPath, ext: ".sh"}, nil
	}

	switch shellType {
	case "bash", "":
		bin := "/bin/bash"
		if runtime.GOOS == "windows" {
			bin = "bash.exe"
		}
		return shellInfo{bin: bin, ext: ".sh"}, nil
	case "zsh":
		return shellInfo{bin: "/bin/zsh", ext: ".sh"}, nil
	case "sh":
		return shellInfo{bin: "/bin/sh", ext: ".sh"}, nil
	case "fish":
		return shellInfo{bin: "fish", ext: ".fish"}, nil
	case "windows_powershell":
		return shellInfo{bin: "powershell.exe", scriptFlag: "-File", ext: ".ps1"}, nil
	case "pwsh":
		return shellInfo{bin: "pwsh.exe", scriptFlag: "-File", ext: ".ps1"}, nil
	case "cmd":
		return shellInfo{bin: "cmd.exe", scriptFlag: "/C", ext: ".bat"}, nil
	case "git_bash":
		return shellInfo{bin: "bash.exe", ext: ".sh"}, nil
	default:
		return shellInfo{}, fmt.Errorf("unknown shell type: %v", shellType)
	}
}

// runTinyTunScript writes the given script to a temp file and executes it
// with the configured shell.  stage is only used for logging.
func runTinyTunScript(setting *configure.Setting, stage, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	si, err := resolveShellInfo(setting.TunRouteShellType, setting.TunRouteShellPath)
	if err != nil {
		return fmt.Errorf("tinytun route script (%s): %w", stage, err)
	}

	// Resolve the shell binary via PATH if it is not an absolute path.
	binPath := si.bin
	if !filepath.IsAbs(binPath) {
		if resolved, err := exec.LookPath(binPath); err == nil {
			binPath = resolved
		}
	}

	// Write script to a temp file with restricted permissions.
	tmpFile, err := os.CreateTemp("", "tinytun_*"+si.ext)
	if err != nil {
		return fmt.Errorf("tinytun route script (%s): failed to create temp file: %w", stage, err)
	}
	defer os.Remove(tmpFile.Name())

	// Restrict permissions immediately after creation (before writing content).
	if runtime.GOOS != "windows" {
		if err = os.Chmod(tmpFile.Name(), 0600); err != nil {
			tmpFile.Close()
			return fmt.Errorf("tinytun route script (%s): failed to chmod temp file: %w", stage, err)
		}
	}

	if _, err = tmpFile.WriteString(script); err != nil {
		tmpFile.Close()
		return fmt.Errorf("tinytun route script (%s): failed to write temp file: %w", stage, err)
	}
	tmpFile.Close()

	// Make the file executable on non-Windows systems.
	if runtime.GOOS != "windows" {
		if err = os.Chmod(tmpFile.Name(), 0700); err != nil {
			return fmt.Errorf("tinytun route script (%s): failed to chmod temp file executable: %w", stage, err)
		}
	}

	// Build command arguments.
	var args []string
	if si.scriptFlag != "" {
		args = []string{binPath, si.scriptFlag, tmpFile.Name()}
	} else {
		args = []string{binPath, tmpFile.Name()}
	}

	log.Info("tinytun: running %s script via %s", stage, binPath)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("tinytun route script (%s) exited with error: %w", stage, err)
	}
	return nil
}

// tinytunLineWriter is an io.Writer that prefixes each output line from the TinyTun
// process with "[tinytun] " and forwards it to the v2rayA log system.
// This makes TinyTun log lines distinguishable from core (v2ray/xray) and v2rayA
// log lines so the frontend can filter by source.
type tinytunLineWriter struct{}

func (w tinytunLineWriter) Write(p []byte) (n int, err error) {
	s := string(p)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			line = strings.ReplaceAll(line, "io.go:", "tinytun.go:")
			log.Info("[tinytun] %v", line)
		}
	}
	return len(p), nil
}

// startTinyTun generates the TinyTun config and starts the TinyTun process.
func startTinyTun(tmpl *Template) error {
	// Ensure any previous TinyTun instance is stopped and has exited before
	// starting a new one (prevents tun device conflicts and orphaned processes).
	stopTinyTun()

	binPath, err := GetTinyTunBinPath()
	if err != nil {
		return err
	}

	configPath, err := generateTinyTunConfig(tmpl)
	if err != nil {
		return err
	}

	log.Info("Starting TinyTun from %v with config %v", binPath, configPath)

	ctx, cancel := context.WithCancel(context.Background())
	setting := tmpl.Setting

	cmdArgs := []string{"run", "--config", configPath}
	cmd := exec.CommandContext(ctx, binPath, cmdArgs...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdout = tinytunLineWriter{}
	cmd.Stderr = tinytunLineWriter{}

	// On Linux, when the user selects the eBPF process-exclusion backend,
	// set TINYTUN_EBPF_OBJECT so TinyTun loads the eBPF programs from the
	// standard installation path (/usr/lib/tinytun/tinytun-ebpf.o).
	if runtime.GOOS == "linux" && setting.TunProcessBackend == "ebpf" {
		if os.Getenv("TINYTUN_EBPF_OBJECT") == "" {
			cmd.Env = append(os.Environ(), "TINYTUN_EBPF_OBJECT=/usr/lib/tinytun/tinytun-ebpf.o")
		}
	}

	if err = cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start tinytun: %w", err)
	}

	doneCh := make(chan struct{})

	tinyTunState.mu.Lock()
	tinyTunState.cancel = cancel
	tinyTunState.done = doneCh
	tinyTunState.setting = setting
	atomic.StoreInt32(&tinyTunState.stopping, 0)
	tinyTunState.mu.Unlock()

	// Monitor for unexpected exits: if TinyTun dies without its context being cancelled,
	// the proxy is in an inconsistent state and must be stopped.
	go func() {
		_ = cmd.Wait()
		close(doneCh) // signal stopTinyTun that the process has exited
		select {
		case <-ctx.Done():
			// Context was cancelled by stopTinyTun — intentional stop, nothing to do.
			return
		default:
		}
		// Ignore if a graceful shutdown is already in progress (stopTinyTun was
		// called but the context cancellation has not propagated yet).
		if atomic.LoadInt32(&tinyTunState.stopping) != 0 {
			return
		}
		ProcessManager.lifecycle.Lock()
		defer ProcessManager.lifecycle.Unlock()
		tinyTunState.mu.Lock()
		current := tinyTunState.done == doneCh
		tinyTunState.mu.Unlock()
		if current && ProcessManager.GetRunningTemplate() == tmpl {
			log.Warn("tinytun: process exited unexpectedly, stopping proxy")
			ProcessManager.stop(true)
			_ = configure.SetLastKernelExitStatus(configure.LastKernelExitCrashed)
		}
	}()

	// Run user-defined setup script when auto_route is disabled.
	if !setting.TunAutoRoute {
		if err = runTinyTunScript(setting, "setup", setting.TunSetupScript); err != nil {
			log.Warn("tinytun setup script error: %v", err)
		}
	}

	return nil
}

// stopTinyTun stops the running TinyTun process if one is active.
func stopTinyTun() {
	tinyTunState.mu.Lock()
	wasRunning := tinyTunState.cancel != nil
	if wasRunning {
		// Signal the unexpected-exit monitor that this is an intentional stop
		// before cancelling the context, to close the race window between the
		// process exiting and ctx.Done() being observable.
		atomic.StoreInt32(&tinyTunState.stopping, 1)
	}
	cancel := tinyTunState.cancel
	doneCh := tinyTunState.done
	setting := tinyTunState.setting
	tinyTunState.setting = nil
	tinyTunState.cancel = nil
	tinyTunState.done = nil
	tinyTunState.mu.Unlock()

	if !wasRunning {
		return
	}

	log.Info("Stopping TinyTun")
	cancel()
	// CommandContext kills the child, and WaitDelay bounds inherited pipe waits.
	<-doneCh

	if !setting.TunAutoRoute {
		if err := runTinyTunScript(setting, "teardown", setting.TunTeardownScript); err != nil {
			log.Warn("tinytun teardown script error: %v", err)
		}
	}
}

// IsTinyTunEnabled reports whether TinyTun support was compiled into this binary.
func IsTinyTunEnabled() bool { return true }
