package v2ray

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	jsoniter "github.com/json-iterator/go"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

type Addr struct {
	host string
	port string
	udp  bool
}

func parseDnsAddr(addr string) Addr {
	// 223.5.5.5
	if net.ParseIP(addr) != nil {
		return Addr{
			host: addr,
			port: "53",
			udp:  true,
		}
	}
	// dns.google:53
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if _, err = strconv.Atoi(port); err == nil {
			return Addr{
				host: host,
				port: port,
				udp:  true,
			}
		}
	}
	// tcp://8.8.8.8:53, https://dns.google/dns-query, quic://dns.nextdns.io
	if strings.Contains(addr, "://") {
		if u, err := url.Parse(addr); err == nil {
			udp := false
			if u.Scheme == "quic" {
				udp = true
			}
			return Addr{
				host: u.Hostname(),
				port: u.Port(),
				udp:  udp,
			}
		}
	}
	// dns.google, dns.pub, etc.
	return Addr{
		host: addr,
		port: "53",
		udp:  true,
	}
}

type DnsRouting struct {
	DirectDomains []Addr
	ProxyDomains  []Addr
	DirectIPs     []Addr
	ProxyIPs      []Addr
}

// setDNS 生成新 DNS 模块的配置，嵌入 xray JSON 供 v2raya-core 读取。
// v2raya-core 启动时解析此配置并启动独立 DNS 监听器，v2rayA 不参与 DNS 查询处理。
func (t *Template) setDNS(serverInfos []serverInfo) error {
	return t.generateDnsModuleConfig(serverInfos)
}

// generateDnsModuleConfig 生成新 DNS 模块的 JSON 配置，嵌入 xray JSON 配置文件。
// v2raya-core 启动时解析此配置并启动独立 DNS 监听器，v2rayA 不参与 DNS 查询处理。
//
// 生成的配置结构对应 core/dns/config.go 中的 DnsModuleConfig。
func (t *Template) generateDnsModuleConfig(serverInfos []serverInfo) error {
	return t.generateDnsModuleConfigWithRules(serverInfos, configure.GetDnsRulesNotNil())
}

func (t *Template) generateDnsModuleConfigWithRules(serverInfos []serverInfo, rules []configure.DnsRule) error {
	setting := t.Setting
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}

	listenAddr := setting.DNSListenAddress()

	// 获取 SOCKS 入站端口（用于 proxy_map）
	socksPort := 20170
	if p := configure.GetPortsNotNil(); p != nil && p.Socks5 > 0 {
		socksPort = p.Socks5
	}

	// 读取当前系统 DNS（保存原始配置，用于 v2raya-core 的 bootstrap 解析）。
	// 重新加载配置时沿用接管前的系统 DNS。
	bootstrapDns := getSystemDnsServers()

	listenerCfg := map[string]interface{}{
		"listen_addr": listenAddr,
		"timeout":     5,
	}
	host, port, _ := net.SplitHostPort(listenAddr)
	if shouldLocalDnsListen(setting) && !(port == "53" && parseListenSocketsConflict(host, "127.2.0.17")) {
		if couldListen, _ := couldLocalDnsListen(); couldListen {
			listenerCfg["extra_listen_addrs"] = []string{"127.2.0.17:53"}
		}
	}

	cfg := map[string]interface{}{
		"listener": listenerCfg,
		"cache": map[string]interface{}{
			"enabled":   setting.DnsCacheEnabled,
			"size":      setting.DnsCacheSize,
			"min_ttl":   setting.DnsCacheMinTTL,
			"max_ttl":   setting.DnsCacheMaxTTL,
			"prefetch":  setting.DnsPrefetch,
			"neg_cache": setting.DnsNegativeCache,
		},
		"proxy_map":     make(map[string]interface{}),
		"bootstrap":     make([]string, 0),
		"bootstrap_dns": bootstrapDns,
		"upstreams":     make([]map[string]interface{}, 0),
		"rules":         make([]map[string]interface{}, 0),
	}

	// 应用默认值
	cache := cfg["cache"].(map[string]interface{})
	if cache["size"].(int) <= 0 {
		cache["size"] = 4096
	}
	if cache["min_ttl"].(int) <= 0 {
		cache["min_ttl"] = 60
	}
	if cache["max_ttl"].(int) <= 0 {
		cache["max_ttl"] = 86400
	}

	// 迁移 DNS 规则
	migrated := configure.MigrateDnsRules(rules)

	// 扫描所有 DNS 规则中引用的出站标签，构建 proxy_map
	// （仅当标签非 direct/block 时创建映射，指向本地 SOCKS 入站端口）
	proxyMap := cfg["proxy_map"].(map[string]interface{})
	bootstrapList := cfg["bootstrap"].([]string)

	// 用于上游去重的 key
	type upstreamKey struct {
		addr  string
		proto string
		tag   string
	}
	seenUpstream := make(map[upstreamKey]int) // key → index in upstreams list
	upstreams := cfg["upstreams"].([]map[string]interface{})
	rulesList := cfg["rules"].([]map[string]interface{})
	defaultUpstreamID := ""

	for _, rule := range migrated {
		outboundTag := rule.Outbound
		if outboundTag == "" {
			outboundTag = "direct"
		}

		action := rule.Action
		if action == "" {
			action = "route"
		}
		if outboundTag == "block" {
			action = "reject"
		}
		isReject := action == "reject"
		if action == "bypass" {
			outboundTag = "direct"
		}

		upstreamAddr := rule.Upstream
		if upstreamAddr == "" {
			upstreamAddr = rule.Server
		}
		if upstreamAddr == "" && !isReject {
			continue
		}

		upstreamID := ""
		if !isReject {
			// "localhost" 表示本地系统 DNS 解析器，映射为 127.0.0.1
			if upstreamAddr == "localhost" {
				upstreamAddr = "127.0.0.1"
			}

			proto, addr, hostname, err := parseDNSUpstream(upstreamAddr)
			if err != nil {
				return fmt.Errorf("DNS rule %q: %w", rule.RuleID, err)
			}
			if proto == "quic" && outboundTag != "direct" && action != "bypass" {
				return fmt.Errorf("DNS rule %q: DNS-over-QUIC requires a direct upstream", rule.RuleID)
			}
			if net.ParseIP(hostname) == nil && (outboundTag == "direct" || action == "bypass") {
				bootstrapList = append(bootstrapList, hostname)
			}

			// 构建 proxy_map：为每个非直连的出站标签创建 SOCKS5 映射
			if outboundTag != "direct" && outboundTag != "block" {
				if _, exists := proxyMap[outboundTag]; !exists {
					switch p := t.ProbePorts[outboundTag]; {
					case p > 0:
						proxyMap[outboundTag] = fmt.Sprintf("127.0.0.1:%d", p)
					case t.ProbePorts == nil:
						proxyMap[outboundTag] = fmt.Sprintf("127.0.0.1:%d", socksPort)
					default:
						// No connected member for this DNS rule. Block the internal
						// dispatch and leave no SOCKS fallback to another group.
						proxyMap[outboundTag] = ""
						t.internalRoutes = append(t.internalRoutes, coreObj.RoutingRule{
							Type: "field", InboundTag: []string{internalGroupTag("dns", outboundTag)}, OutboundTag: "block",
						})
					}
				}
			}

			// 上游去重（相同地址+协议+代理标签的上游只创建一个实例）
			key := upstreamKey{addr: addr, proto: proto, tag: outboundTag}
			upstreamID = fmt.Sprintf("upstream-%d", len(upstreams))
			if idx, ok := seenUpstream[key]; ok {
				upstreamID = fmt.Sprintf("upstream-%d", idx)
			} else {
				seenUpstream[key] = len(upstreams)
				upstreams = append(upstreams, map[string]interface{}{
					"id":        upstreamID,
					"addr":      addr,
					"protocol":  proto,
					"proxy_tag": outboundTag,
					"bootstrap": false,
				})
			}
		}

		// 解析域名匹配规则
		var domains, suffixes, regexes []string
		domainStr := rule.Domain
		if domainStr == "" {
			domainStr = rule.Domains
		}
		if domainStr != "" {
			for _, d := range strings.Split(strings.TrimSpace(domainStr), "\n") {
				d = strings.TrimSpace(d)
				if d == "" {
					continue
				}
				switch {
				case strings.HasPrefix(d, "full:"):
					val := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "full:"))), ".")
					if val != "" {
						domains = append(domains, val)
					}
				case strings.HasPrefix(d, "domain:"):
					val := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "domain:"))), ".")
					if val != "" {
						suffixes = append(suffixes, val)
					}
				case strings.HasPrefix(d, "suffix:"):
					val := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "suffix:"))), ".")
					if val != "" {
						suffixes = append(suffixes, val)
					}
				case strings.HasPrefix(d, "regexp:"):
					val := strings.TrimSpace(strings.TrimPrefix(d, "regexp:"))
					if val != "" {
						regexes = append(regexes, val)
					}
				case strings.HasPrefix(d, "keyword:"):
					val := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(d, "keyword:")))
					if val != "" {
						domains = append(domains, "keyword:"+val)
					}
				case strings.HasPrefix(d, "geosite:"):
					domains = append(domains, d)
				default:
					val := strings.TrimSuffix(strings.ToLower(d), ".")
					if val != "" {
						domains = append(domains, val)
					}
				}
			}
		}

		// 解析 IP 匹配
		var ips []string
		if rule.IP != "" {
			for _, ip := range strings.Split(rule.IP, ",") {
				ip = strings.TrimSpace(ip)
				if ip != "" {
					ips = append(ips, ip)
				}
			}
		}

		// 解析客户端 IP
		var clientIPs []string
		if rule.ClientIP != "" {
			for _, cidr := range strings.Split(rule.ClientIP, ",") {
				cidr = strings.TrimSpace(cidr)
				if cidr != "" {
					clientIPs = append(clientIPs, cidr)
				}
			}
		}

		// 解析查询类型
		var qtypes []string
		if rule.QueryType != "" && rule.QueryType != "*" {
			for _, qt := range strings.Split(rule.QueryType, ",") {
				qt = strings.TrimSpace(qt)
				if qt != "" && qt != "*" {
					qtypes = append(qtypes, strings.ToUpper(qt))
				}
			}
		}

		hasConditions := len(domains) > 0 || len(suffixes) > 0 || len(regexes) > 0 || len(clientIPs) > 0 || len(ips) > 0 || len(qtypes) > 0

		// 构建规则配置（有匹配条件时，或无条件的 reject 兜底规则）
		if hasConditions || isReject {
			ruleID := rule.RuleID
			if ruleID == "" {
				ruleID = fmt.Sprintf("rule-%d", len(rulesList))
			}

			policy := rule.Policy
			if policy == "" {
				policy = "single"
			}

			rc := map[string]interface{}{
				"id":            ruleID,
				"upstream":      upstreamID,
				"action":        action,
				"policy":        policy,
				"domain":        domains,
				"domain_suffix": suffixes,
				"domain_regex":  regexes,
				"ip":            ips,
				"client_ip":     clientIPs,
			}
			if len(qtypes) > 0 {
				rc["query_type"] = qtypes
			}

			rulesList = append(rulesList, rc)
		} else if upstreamID != "" {
			// 无匹配条件的普通规则作为兜底默认上游
			defaultUpstreamID = upstreamID
		}
	}

	// 节点服务器域名必须直连解析：若命中默认（代理）上游，查询经 dispatcher
	// 路由到代理出站，而代理出站连接节点又需要解析节点域名，形成死锁，
	// 导致节点域名永远解析失败、代理通道完全不可用。
	var nodeDomains []string
	for _, info := range serverInfos {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(info.Info.GetHostname())), ".")
		if host != "" && net.ParseIP(host) == nil {
			nodeDomains = append(nodeDomains, host)
		}
	}
	nodeDomains = common.Deduplicate(nodeDomains)
	if len(nodeDomains) > 0 {
		nodeUpstreamAddr := "223.5.5.5:53"
		for _, s := range bootstrapDns {
			if host, _, err := net.SplitHostPort(s); err == nil {
				if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
					nodeUpstreamAddr = s
					break
				}
			}
		}
		// 不能 append 到末尾：模块以最后一个非 bootstrap 上游作为默认上游
		upstreams = append([]map[string]interface{}{{
			"id":        "upstream-node",
			"addr":      nodeUpstreamAddr,
			"protocol":  "udp",
			"proxy_tag": "direct",
			"bootstrap": false,
		}}, upstreams...)
		rulesList = append([]map[string]interface{}{{
			"id":        "rule-node",
			"upstream":  "upstream-node",
			"action":    "route",
			"policy":    "single",
			"domain":    nodeDomains,
			"ip":        nil,
			"client_ip": nil,
		}}, rulesList...)
	}

	privateUpstreams, privateRules, privateBootstrap, err := nodeDNSRules(serverInfos)
	if err != nil {
		return err
	}
	upstreams = append(privateUpstreams, upstreams...)
	rulesList = append(privateRules, rulesList...)
	bootstrapList = append(bootstrapList, privateBootstrap...)

	// 显式写入 default_upstream，并将对应的兜底上游移到 upstreams 末尾，
	// 确保即使去重命中先前的上游条目，默认上游依然无歧义。
	if defaultUpstreamID != "" {
		cfg["default_upstream"] = defaultUpstreamID
		for i, u := range upstreams {
			if id, ok := u["id"].(string); ok && id == defaultUpstreamID {
				if i != len(upstreams)-1 {
					target := upstreams[i]
					upstreams = append(append(upstreams[:i], upstreams[i+1:]...), target)
				}
				break
			}
		}
	}

	cfg["upstreams"] = upstreams
	cfg["rules"] = rulesList

	// bootstrap 列表去重
	bootstrapList = common.Deduplicate(bootstrapList)
	cfg["bootstrap"] = bootstrapList

	// 序列化为 JSON
	raw, err := jsoniter.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal dns module config: %w", err)
	}
	t.DnsModuleConfig = json.RawMessage(raw)

	return nil
}

// getSystemDnsServers 读取当前系统的 DNS 服务器列表（从 /etc/resolv.conf）。
// 在劫持发生前调用，保存原始 DNS 供 v2raya-core bootstrap 使用。
func getSystemDnsServers() []string {
	data, err := readSystemResolvConf()
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				ip := parts[1]
				if net.ParseIP(ip) != nil {
					servers = append(servers, net.JoinHostPort(ip, "53"))
				}
			}
		}
	}
	return servers
}

// parseDNSUpstream retains the hostname for TLS verification and bootstrap.
func parseDNSUpstream(raw string) (protocol, addr, host string, err error) {
	protocol = "udp"
	addr = strings.TrimSpace(raw)
	defaultPort := "53"
	if scheme, rest, ok := strings.Cut(addr, "://"); ok {
		switch strings.ToLower(scheme) {
		case "https":
			u, err := url.Parse(addr)
			if err != nil || u.Hostname() == "" {
				return "", "", "", fmt.Errorf("invalid DoH URL %q", addr)
			}
			return "https", u.String(), u.Hostname(), nil
		case "tls", "dot", "tcp-tls":
			protocol, defaultPort = "tcp-tls", "853"
		case "quic", "doq":
			protocol, defaultPort = "quic", "853"
		case "udp", "tcp":
			protocol = strings.ToLower(scheme)
		default:
			return "", "", "", fmt.Errorf("unsupported DNS upstream protocol %q", scheme)
		}
		addr = rest
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = strings.Trim(addr, "[]"), defaultPort
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return "", "", "", fmt.Errorf("invalid DNS upstream %q", raw)
		}
	}
	portNumber, err := strconv.Atoi(port)
	if host == "" || strings.ContainsAny(host, "/?#@ ") || err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", "", fmt.Errorf("invalid DNS upstream %q", raw)
	}
	return protocol, net.JoinHostPort(host, port), host, nil
}
