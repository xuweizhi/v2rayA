package service

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/v2rayA/v2rayA/kernel/coreObj"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

// RuleHit describes one routing rule evaluated against a query.
type RuleHit struct {
	Index      int      `json:"index"`
	Matched    bool     `json:"matched"`
	Outbound   string   `json:"outbound"`
	Domains    []string `json:"domains,omitempty"`
	IPs        []string `json:"ips,omitempty"`
	Port       string   `json:"port,omitempty"`
	Network    string   `json:"network,omitempty"`
	InboundTag []string `json:"inboundTag,omitempty"`
	// AssetBased is true when the rule references geosite/geoip assets that
	// cannot be evaluated locally; the rule may match at runtime.
	AssetBased bool   `json:"assetBased,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// DetectRuleResult is the response of the rule-hit detector.
type DetectRuleResult struct {
	Domain    string    `json:"domain"`
	Port      string    `json:"port"`
	Network   string    `json:"network"`
	Inbound   string    `json:"inbound,omitempty"`
	Matched   int       `json:"matched"` // index of the first matched rule, -1 if none
	Outbound  string    `json:"outbound,omitempty"`
	Rules     []RuleHit `json:"rules"`
	AssetNote string    `json:"assetNote,omitempty"`
}

// DetectRule evaluates the routing rules of the running template against the
// given query. A nil template (core not running) returns a descriptive error.
func DetectRule(domain, port, network, inbound string) (result DetectRuleResult, err error) {
	tmpl := v2ray.ProcessManager.GetRunningTemplate()
	if tmpl == nil {
		return result, fmt.Errorf("v2ray-core is not running")
	}
	result = DetectRuleOnTemplate(tmpl, domain, port, network, inbound)
	return result, nil
}

// DetectRuleOnTemplate evaluates the routing rules of a template.
func DetectRuleOnTemplate(tmpl *v2ray.Template, domain, port, network, inbound string) DetectRuleResult {
	result := DetectRuleResult{
		Domain:  domain,
		Port:    port,
		Network: network,
		Inbound: inbound,
		Matched: -1,
	}
	// Resolve the domain lazily; only needed for IP rules.
	var ips []net.IP
	ipResolved := false
	resolveIPs := func() {
		if ipResolved {
			return
		}
		ipResolved = true
		if domain != "" {
			addrs, err := net.LookupIP(domain)
			if err == nil {
				ips = addrs
			}
		}
	}
	assetNote := false
	for i, rule := range tmpl.Routing.Rules {
		hit := RuleHit{
			Index:      i,
			Matched:    false,
			Outbound:   firstNonEmpty(rule.OutboundTag, rule.BalancerTag),
			Domains:    rule.Domain,
			IPs:        rule.IP,
			Port:       rule.Port,
			Network:    rule.Network,
			InboundTag: rule.InboundTag,
		}
		if len(rule.Domain) == 0 && len(rule.IP) == 0 && rule.Port == "" && rule.Network == "" && len(rule.InboundTag) == 0 {
			// catch-all rule
			hit.Matched = true
			hit.Reason = "catch-all rule"
		} else {
			matched, asset, reason := matchRuleConditions(rule, domain, port, network, inbound, resolveIPs, ips)
			hit.Matched = matched
			hit.AssetBased = asset
			hit.Reason = reason
			if asset {
				assetNote = true
			}
		}
		result.Rules = append(result.Rules, hit)
		if hit.Matched && result.Matched < 0 {
			result.Matched = i
			result.Outbound = hit.Outbound
		}
	}
	if assetNote {
		result.AssetNote = "部分规则引用了 geosite/geoip 资源，无法在本地完全判定，实际结果以核心为准。"
	}
	return result
}

// matchRuleConditions evaluates a single rule. It returns whether the rule
// matches, whether it relies on geo assets, and a human-readable reason.
func matchRuleConditions(rule coreObj.RoutingRule, domain, port, network, inbound string, resolveIPs func(), ips []net.IP) (matched, asset bool, reason string) {
	// Inbound tag
	if len(rule.InboundTag) > 0 {
		if inbound == "" || !containsStr(rule.InboundTag, inbound) {
			return false, false, fmt.Sprintf("inboundTag [%v] 不匹配 %q", rule.InboundTag, inbound)
		}
	}
	// Network
	if rule.Network != "" {
		if network == "" || !strings.Contains(rule.Network, network) {
			return false, false, fmt.Sprintf("network %q 不匹配 %q", rule.Network, network)
		}
	}
	// Port
	if rule.Port != "" {
		if port == "" || !portInRange(rule.Port, port) {
			return false, false, fmt.Sprintf("port %q 不匹配 %q", rule.Port, port)
		}
	}
	// Domain
	if len(rule.Domain) > 0 {
		if domain == "" {
			return false, false, "规则要求域名，但查询未提供域名"
		}
		dm, dasset := matchDomainEntries(rule.Domain, domain)
		if !dm {
			return false, dasset, fmt.Sprintf("域名 %q 不匹配 %v", domain, rule.Domain)
		}
	}
	// IP
	if len(rule.IP) > 0 {
		if domain == "" {
			return false, false, "规则要求 IP，但查询未提供域名"
		}
		resolveIPs()
		if len(ips) == 0 {
			return false, true, fmt.Sprintf("域名 %q 解析失败，无法判定 IP 规则 %v", domain, rule.IP)
		}
		im, iasset := matchIPEntries(rule.IP, ips)
		if !im {
			return false, iasset, fmt.Sprintf("IP %v 不匹配 %v", ips, rule.IP)
		}
	}
	return true, false, "所有条件匹配"
}

// matchDomainEntries returns whether any domain entry matches, and whether
// geo assets were referenced during evaluation.
func matchDomainEntries(entries []string, domain string) (matched, asset bool) {
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e, "full:"):
			if domain == e[5:] {
				return true, false
			}
		case strings.HasPrefix(e, "domain:"):
			d := e[7:]
			if domain == d || strings.HasSuffix(domain, "."+d) {
				return true, false
			}
		case strings.HasPrefix(e, "regexp:"):
			if re, err := regexp.Compile(e[7:]); err == nil && re.MatchString(domain) {
				return true, false
			}
		case strings.HasPrefix(e, "geosite:") || strings.HasPrefix(e, "ext:"):
			asset = true
		default:
			// bare domain: subdomain match, mirroring xray's default behavior
			if domain == e || strings.HasSuffix(domain, "."+e) {
				return true, false
			}
		}
	}
	return false, asset
}

// matchIPEntries returns whether any IP entry matches one of the resolved
// addresses, and whether geo assets were referenced.
func matchIPEntries(entries []string, ips []net.IP) (matched, asset bool) {
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e, "geoip:") || strings.HasPrefix(e, "ext:"):
			asset = true
		default:
			if _, ipnet, err := net.ParseCIDR(e); err == nil {
				for _, ip := range ips {
					if ipnet.Contains(ip) {
						return true, false
					}
				}
			} else if parsed := net.ParseIP(e); parsed != nil {
				for _, ip := range ips {
					if parsed.Equal(ip) {
						return true, false
					}
				}
			}
		}
	}
	return false, asset
}

// portInRange checks a port spec like "443", "1000-2000" or a comma list.
func portInRange(spec, port string) bool {
	p, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if from, to, ok := strings.Cut(part, "-"); ok {
			f, err1 := strconv.Atoi(from)
			t, err2 := strconv.Atoi(to)
			if err1 == nil && err2 == nil && p >= f && p <= t {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == p {
			return true
		}
	}
	return false
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
