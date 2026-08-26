package service

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

// DiagnoseReport is the structured result of the network self-check.
type DiagnoseReport struct {
	Text          string            `json:"text"`
	Version       string            `json:"version"`
	OS            string            `json:"os"`
	CoreRunning   bool              `json:"coreRunning"`
	DefaultRoute  string            `json:"defaultRoute"`
	Connectivity  []DnsLatencyItem  `json:"connectivity"`
	ConnectedTo   []string          `json:"connectedTo"`
	DnsRules      []string          `json:"dnsRules"`
	DnsLatency    []DnsLatencyItem  `json:"dnsLatency"`
	RuleDetection *DetectRuleResult `json:"ruleDetection,omitempty"`
}

// Diagnose runs the network self-check and builds a copyable report.
func Diagnose(domain string) (report DiagnoseReport) {
	report.Version = conf.Version
	report.OS = runtime.GOOS + "/" + runtime.GOARCH
	report.CoreRunning = v2ray.ProcessManager.Running()

	var lines []string
	lines = append(lines,
		"v2rayA 诊断报告",
		"生成时间: "+time.Now().Format("2006-01-02 15:04:05"),
		"版本: "+report.Version,
		"系统: "+report.OS,
		fmt.Sprintf("核心状态: %v", map[bool]string{true: "运行中", false: "已停止"}[report.CoreRunning]),
	)

	// Default route
	report.DefaultRoute = defaultRouteInfo()
	if report.DefaultRoute != "" {
		lines = append(lines, "默认路由: "+report.DefaultRoute)
	}

	// Connectivity probes (direct dial to well-known resolvers)
	probeTargets := []string{"223.5.5.5:53", "1.1.1.1:53"}
	lines = append(lines, "连通性:")
	for _, target := range probeTargets {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, 4*time.Second)
		elapsed := time.Since(start).Milliseconds()
		item := DnsLatencyItem{Server: target, LatencyMs: elapsed}
		if err != nil {
			item.Error = err.Error()
			lines = append(lines, fmt.Sprintf("  %v: 失败 (%v)", target, err))
		} else {
			item.Ok = true
			conn.Close()
			lines = append(lines, fmt.Sprintf("  %v: 正常 (%dms)", target, elapsed))
		}
		report.Connectivity = append(report.Connectivity, item)
	}

	// Connected servers
	for _, cs := range configure.GetConnectedServers().Get() {
		if sRaw, err := cs.LocateServerRaw(); err == nil && sRaw.ServerObj != nil {
			name := fmt.Sprintf("%v (%v)", sRaw.ServerObj.GetName(), sRaw.ServerObj.GetProtocol())
			report.ConnectedTo = append(report.ConnectedTo, name)
		}
	}
	if len(report.ConnectedTo) > 0 {
		lines = append(lines, "已连接节点: "+strings.Join(report.ConnectedTo, ", "))
	} else {
		lines = append(lines, "已连接节点: 无")
	}

	// DNS rules and their direct latency
	rules := configure.GetDnsRulesNotNil()
	lines = append(lines, "DNS 规则:")
	var dnsServers []string
	for _, r := range rules {
		entry := fmt.Sprintf("%v [%v] -> %v", r.Server, strings.ReplaceAll(strings.TrimSpace(r.Domains), "\n", ","), r.Outbound)
		report.DnsRules = append(report.DnsRules, entry)
		lines = append(lines, "  "+entry)
		if r.Server != "" && !strings.Contains(r.Server, "://") && !strings.EqualFold(r.Server, "localhost") {
			dnsServers = append(dnsServers, r.Server)
		}
	}
	if len(dnsServers) > 0 {
		lines = append(lines, "DNS 直连查询:")
		report.DnsLatency = measureDNSServers(dnsServers)
		for _, item := range report.DnsLatency {
			if item.Ok {
				lines = append(lines, fmt.Sprintf("  %v: %dms", item.Server, item.LatencyMs))
			} else {
				lines = append(lines, fmt.Sprintf("  %v: 失败 (%v)", item.Server, item.Error))
			}
		}
	}

	// Rule detection for the target domain
	if domain != "" {
		if tmpl := v2ray.ProcessManager.GetRunningTemplate(); tmpl != nil {
			detect := DetectRuleOnTemplate(tmpl, domain, "443", "tcp", "")
			report.RuleDetection = &detect
			lines = append(lines, fmt.Sprintf("域名 %v 路由: 规则 %d -> %v", domain, detect.Matched, detect.Outbound))
			if detect.AssetNote != "" {
				lines = append(lines, "  "+detect.AssetNote)
			}
		} else {
			lines = append(lines, fmt.Sprintf("域名 %v 路由: 核心未运行，无法检测", domain))
		}
	}

	report.Text = strings.Join(lines, "\n")
	return report
}

// defaultRouteInfo reads the default route from /proc/net/route.
func defaultRouteInfo() string {
	f, err := openProcRoute()
	if err != nil {
		return ""
	}
	defer f.Close()
	lines := readAllLines(f)
	for i, line := range lines {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == "00000000" {
			return fmt.Sprintf("iface=%v gw=%v", fields[0], hexToIP(fields[2]))
		}
	}
	return ""
}

func openProcRoute() (*os.File, error) {
	return os.Open("/proc/net/route")
}

func readAllLines(f *os.File) []string {
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// hexToIP converts a little-endian hex representation used by
// /proc/net/route into an IP address string.
func hexToIP(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 4 {
		return h
	}
	return fmt.Sprintf("%d.%d.%d.%d", raw[3], raw[2], raw[1], raw[0])
}

// measureDNSServers measures a DNS A query against each server.
func measureDNSServers(servers []string) []DnsLatencyItem {
	items := make([]DnsLatencyItem, len(servers))
	measureDirectDNSList(servers, items)
	return items
}
