package v2ray

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/v2rayA/v2rayA/db/configure"
)

// nodeDNSRules creates exact-host rules. A profile cannot claim a parent domain
// or change the default resolver. Conflicting policies must be resolved by the
// user because the DNS module has one shared namespace for node lookups.
func nodeDNSRules(infos []serverInfo) (upstreams, rules []map[string]interface{}, bootstrap []string, err error) {
	byHost := map[string][]string{}
	for _, info := range infos {
		host := strings.TrimSuffix(strings.ToLower(info.Info.GetHostname()), ".")
		if host == "" || net.ParseIP(host) != nil {
			continue
		}
		servers, e := configure.NormalizeNodeDNS(info.NodeDNS)
		if e != nil {
			return nil, nil, nil, e
		}
		if previous, ok := byHost[host]; ok && strings.Join(previous, "\n") != strings.Join(servers, "\n") {
			return nil, nil, nil, fmt.Errorf("conflicting subscription DNS for node hostname %q", host)
		}
		byHost[host] = servers
	}
	var hosts []string
	for host := range byHost {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for i, host := range hosts {
		if len(byHost[host]) == 0 {
			continue
		}
		var ids []string
		start := len(upstreams)
		for j, server := range byHost[host] {
			u, _ := url.Parse(server)
			id := fmt.Sprintf("subscription-node-%d-%d", i, j)
			ids = append(ids, id)
			protocol, addr := u.Scheme, u.Host
			if protocol == "tls" {
				protocol = "tcp-tls"
			}
			if protocol == "https" {
				addr = server
			}
			if net.ParseIP(u.Hostname()) == nil {
				bootstrap = append(bootstrap, u.Hostname())
			}
			upstreams = append(upstreams, map[string]interface{}{"id": id, "addr": addr, "protocol": protocol, "proxy_tag": "direct", "bootstrap": false})
		}
		if len(ids) == 0 {
			continue
		}
		if len(ids) > 1 {
			upstreams[start]["fallback"] = ids[1:]
		}
		rules = append(rules, map[string]interface{}{"id": fmt.Sprintf("subscription-node-rule-%d", i), "domain": []string{host}, "upstream": ids[0], "action": "route", "policy": "single"})
	}
	return
}
