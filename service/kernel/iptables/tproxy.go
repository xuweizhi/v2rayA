package iptables

import (
	"fmt"
	"os"
	"strings"

	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
)

var (
	TproxyNotSkipBr string
)

type tproxy interface {
	AddIPWhitelist(cidr string)
	RemoveIPWhitelist(cidr string)
	GetSetupCommands() Setter
	GetCleanCommands() Setter
}

type legacyTproxy struct{}

type nftTproxy struct{}

var Tproxy tproxy

func init() {
	if IsNftablesSupported() {
		Tproxy = &nftTproxy{}
	} else {
		Tproxy = &legacyTproxy{}
	}
}

func (t *legacyTproxy) AddIPWhitelist(cidr string) {
	// avoid duplication
	t.RemoveIPWhitelist(cidr)
	commands := fmt.Sprintf(`iptables -w 2 -t mangle -I TP_LOCAL -d %s -j RETURN`, cidr)
	if !strings.Contains(cidr, ".") {
		// ipv6
		commands = strings.Replace(commands, "iptables", "ip6tables", 1)
	}
	_ = Setter{Cmds: commands}.Run(false)
}

func (t *legacyTproxy) RemoveIPWhitelist(cidr string) {
	commands := fmt.Sprintf(`iptables -w 2 -t mangle -D TP_LOCAL -d %s -j RETURN`, cidr)
	if !strings.Contains(cidr, ".") {
		// ipv6
		commands = strings.Replace(commands, "iptables", "ip6tables", 1)
	}
	_ = Setter{Cmds: commands}.Run(false)
}

func (t *legacyTproxy) GetSetupCommands() Setter {
	commands := `
ip rule add fwmark 0x40/0xc0 table 100
ip route add local 0.0.0.0/0 dev lo table 100

iptables -w 2 -t mangle -N TP_OUT
iptables -w 2 -t mangle -N TP_PRE
iptables -w 2 -t mangle -N TP_RULE
iptables -w 2 -t mangle -N TP_LOCAL 2>/dev/null || iptables -w 2 -t mangle -F TP_LOCAL
iptables -w 2 -t mangle -N TP_MARK

iptables -w 2 -t mangle -I OUTPUT -j TP_OUT
iptables -w 2 -t mangle -I PREROUTING -j TP_PRE

iptables -w 2 -t mangle -A TP_OUT -p tcp --dport 53 -j RETURN
iptables -w 2 -t mangle -A TP_OUT -p udp --dport 53 -j RETURN
iptables -w 2 -t mangle -A TP_OUT -m mark --mark 0x80/0x80 -j RETURN
iptables -w 2 -t mangle -A TP_OUT -p tcp -m addrtype --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
iptables -w 2 -t mangle -A TP_OUT -p udp -m addrtype --src-type LOCAL ! --dst-type LOCAL -j TP_RULE

iptables -w 2 -t mangle -A TP_PRE -i lo -m mark ! --mark 0x40/0xc0 -j RETURN
iptables -w 2 -t mangle -A TP_PRE -p tcp --dport 53 -j RETURN
iptables -w 2 -t mangle -A TP_PRE -p udp --dport 53 -j RETURN
iptables -w 2 -t mangle -A TP_PRE -p tcp -m addrtype ! --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
iptables -w 2 -t mangle -A TP_PRE -p udp -m addrtype ! --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
# 通用 TPROXY 规则
iptables -w 2 -t mangle -A TP_PRE -p tcp -m mark --mark 0x40/0xc0 -j TPROXY --on-port 52345 --on-ip 127.0.0.1
iptables -w 2 -t mangle -A TP_PRE -p udp -m mark --mark 0x40/0xc0 -j TPROXY --on-port 52345 --on-ip 127.0.0.1
iptables -w 2 -t mangle -A TP_RULE -j CONNMARK --restore-mark
iptables -w 2 -t mangle -A TP_RULE -m mark --mark 0x40/0xc0 -j RETURN
`
	for _, v := range GetExcludedInterfaces() {
		commands += fmt.Sprintf("iptables -w 2 -t mangle -A TP_RULE -i %s -j RETURN\n", shellWord(strings.ReplaceAll(v, "*", "+")))
	}
	// OUTPUT 路径使用 -o 匹配输出网卡（本地流量在 OUTPUT 链中 -i 始终为 lo）
	for _, v := range GetExcludedInterfaces() {
		commands += fmt.Sprintf("iptables -w 2 -t mangle -A TP_RULE -o %s -j RETURN\n", shellWord(strings.ReplaceAll(v, "*", "+")))
	}

	if IsEnabledTproxyWhiteIpGroups() {
		whiteIpv4List, _ := GetWhiteListIPs()
		for _, v := range whiteIpv4List {
			commands += fmt.Sprintf("iptables -w 2 -t mangle -A TP_RULE -d %s -j RETURN\n", v)
		}
	}
	commands += `
iptables -w 2 -t mangle -A TP_RULE -g TP_LOCAL
iptables -w 2 -t mangle -A TP_LOCAL -j TP_MARK

iptables -w 2 -t mangle -A TP_MARK -p tcp -m tcp --syn -j MARK --set-xmark 0x40/0x40
iptables -w 2 -t mangle -A TP_MARK -p udp -m conntrack --ctstate NEW -j MARK --set-xmark 0x40/0x40
iptables -w 2 -t mangle -A TP_MARK -j CONNMARK --save-mark
`
	if IsIPv6Supported() {
		commands += `
ip -6 rule add fwmark 0x40/0xc0 table 100
ip -6 route add local ::/0 dev lo table 100

ip6tables -w 2 -t mangle -N TP_OUT
ip6tables -w 2 -t mangle -N TP_PRE
ip6tables -w 2 -t mangle -N TP_RULE
ip6tables -w 2 -t mangle -N TP_LOCAL 2>/dev/null || ip6tables -w 2 -t mangle -F TP_LOCAL
ip6tables -w 2 -t mangle -N TP_MARK

ip6tables -w 2 -t mangle -I OUTPUT -j TP_OUT
ip6tables -w 2 -t mangle -I PREROUTING -j TP_PRE

ip6tables -w 2 -t mangle -A TP_OUT -p tcp --dport 53 -j RETURN
ip6tables -w 2 -t mangle -A TP_OUT -p udp --dport 53 -j RETURN
ip6tables -w 2 -t mangle -A TP_OUT -m mark --mark 0x80/0x80 -j RETURN
ip6tables -w 2 -t mangle -A TP_OUT -p tcp -m addrtype --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
ip6tables -w 2 -t mangle -A TP_OUT -p udp -m addrtype --src-type LOCAL ! --dst-type LOCAL -j TP_RULE

ip6tables -w 2 -t mangle -A TP_PRE -i lo -m mark ! --mark 0x40/0xc0 -j RETURN
ip6tables -w 2 -t mangle -A TP_PRE -p tcp --dport 53 -j RETURN
ip6tables -w 2 -t mangle -A TP_PRE -p udp --dport 53 -j RETURN
ip6tables -w 2 -t mangle -A TP_PRE -p tcp -m addrtype ! --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
ip6tables -w 2 -t mangle -A TP_PRE -p udp -m addrtype ! --src-type LOCAL ! --dst-type LOCAL -j TP_RULE
# 通用 TPROXY 规则
ip6tables -w 2 -t mangle -A TP_PRE -p tcp -m mark --mark 0x40/0xc0 -j TPROXY --on-port 52345 --on-ip ::1
ip6tables -w 2 -t mangle -A TP_PRE -p udp -m mark --mark 0x40/0xc0 -j TPROXY --on-port 52345 --on-ip ::1
ip6tables -w 2 -t mangle -A TP_RULE -j CONNMARK --restore-mark
ip6tables -w 2 -t mangle -A TP_RULE -m mark --mark 0x40/0xc0 -j RETURN
`
		for _, v := range GetExcludedInterfaces() {
			commands += fmt.Sprintf("ip6tables -w 2 -t mangle -A TP_RULE -i %s -j RETURN\n", shellWord(strings.ReplaceAll(v, "*", "+")))
		}
		// IPv6 OUTPUT 路径使用 -o 匹配输出网卡
		for _, v := range GetExcludedInterfaces() {
			commands += fmt.Sprintf("ip6tables -w 2 -t mangle -A TP_RULE -o %s -j RETURN\n", shellWord(strings.ReplaceAll(v, "*", "+")))
		}
		if IsEnabledTproxyWhiteIpGroups() {
			_, whiteIpv6List := GetWhiteListIPs()
			for _, v := range whiteIpv6List {
				commands += fmt.Sprintf("ip6tables -w 2 -t mangle -A TP_RULE -d %s -j RETURN\n", v)
			}
		}
		commands += `
ip6tables -w 2 -t mangle -A TP_RULE -g TP_LOCAL
ip6tables -w 2 -t mangle -A TP_LOCAL -j TP_MARK

ip6tables -w 2 -t mangle -A TP_MARK -p tcp -m tcp --syn -j MARK --set-xmark 0x40/0x40
ip6tables -w 2 -t mangle -A TP_MARK -p udp -m conntrack --ctstate NEW -j MARK --set-xmark 0x40/0x40
ip6tables -w 2 -t mangle -A TP_MARK -j CONNMARK --save-mark
`
	}
	return Setter{
		Cmds: commands,
	}
}

func (t *legacyTproxy) GetCleanCommands() Setter {
	commands := `
ip rule del fwmark 0x40/0xc0 table 100 2>/dev/null || true
ip route del local 0.0.0.0/0 dev lo table 100 2>/dev/null || true

iptables -w 2 -t mangle -F TP_OUT
iptables -w 2 -t mangle -D OUTPUT -j TP_OUT
iptables -w 2 -t mangle -X TP_OUT
iptables -w 2 -t mangle -F TP_PRE
iptables -w 2 -t mangle -D PREROUTING -j TP_PRE
iptables -w 2 -t mangle -X TP_PRE
iptables -w 2 -t mangle -F TP_RULE
iptables -w 2 -t mangle -X TP_RULE
iptables -w 2 -t mangle -F TP_LOCAL 2>/dev/null || true
iptables -w 2 -t mangle -X TP_LOCAL 2>/dev/null || true
iptables -w 2 -t mangle -F TP_MARK
iptables -w 2 -t mangle -X TP_MARK
iptables -w 2 -t mangle -F DNS_MARK
iptables -w 2 -t mangle -D PREROUTING -p udp --dport 53 -j DNS_MARK
iptables -w 2 -t mangle -D PREROUTING -p tcp --dport 53 -j DNS_MARK
iptables -w 2 -t mangle -D OUTPUT -p udp --dport 53 -j DNS_MARK
iptables -w 2 -t mangle -D OUTPUT -p tcp --dport 53 -j DNS_MARK
iptables -w 2 -t mangle -X DNS_MARK
`
	if IsIPv6Supported() {
		commands += `
ip -6 rule del fwmark 0x40/0xc0 table 100 2>/dev/null || true
ip -6 route del local ::/0 dev lo table 100 2>/dev/null || true

ip6tables -w 2 -t mangle -F TP_OUT
ip6tables -w 2 -t mangle -D OUTPUT -j TP_OUT
ip6tables -w 2 -t mangle -X TP_OUT
ip6tables -w 2 -t mangle -F TP_PRE
ip6tables -w 2 -t mangle -D PREROUTING -j TP_PRE
ip6tables -w 2 -t mangle -X TP_PRE
ip6tables -w 2 -t mangle -F TP_RULE
ip6tables -w 2 -t mangle -X TP_RULE
ip6tables -w 2 -t mangle -F TP_LOCAL 2>/dev/null || true
ip6tables -w 2 -t mangle -X TP_LOCAL 2>/dev/null || true
ip6tables -w 2 -t mangle -F TP_MARK
ip6tables -w 2 -t mangle -X TP_MARK
ip6tables -w 2 -t mangle -F DNS_MARK
ip6tables -w 2 -t mangle -D PREROUTING -p udp --dport 53 -j DNS_MARK
ip6tables -w 2 -t mangle -D PREROUTING -p tcp --dport 53 -j DNS_MARK
ip6tables -w 2 -t mangle -D OUTPUT -p udp --dport 53 -j DNS_MARK
ip6tables -w 2 -t mangle -D OUTPUT -p tcp --dport 53 -j DNS_MARK
ip6tables -w 2 -t mangle -X DNS_MARK
`
	}
	commands += "conntrack -D --mark 0x40 2>/dev/null || true\n"
	return Setter{
		Cmds: commands,
	}
}

func (t *nftTproxy) AddIPWhitelist(cidr string) {
	command := fmt.Sprintf("nft add element inet v2raya local_ips { %s }", cidr)
	if !strings.Contains(cidr, ".") {
		command = strings.Replace(command, "local_ips", "local_ips6", 1)
	}
	_ = Setter{Cmds: command}.Run(false)
}

func (t *nftTproxy) RemoveIPWhitelist(cidr string) {
	command := fmt.Sprintf("nft delete element inet v2raya local_ips { %s }", cidr)
	if !strings.Contains(cidr, ".") {
		command = strings.Replace(command, "local_ips", "local_ips6", 1)
	}
	_ = Setter{Cmds: command}.Run(false)
}

func (t *nftTproxy) buildNftTable(whiteIpv4List, whiteIpv6List, excludedIfs []string, ipv6Supported bool) string {
	hasWhitelist := len(whiteIpv4List) > 0 || len(whiteIpv6List) > 0
	table := `
	table inet v2raya {
`
	if hasWhitelist {
		table += `
    set whitelist {
        type ipv4_addr
        flags interval
        auto-merge
`
		if len(whiteIpv4List) > 0 {
			table += "        elements = {\n            " + strings.Join(whiteIpv4List, ",\n            ") + "\n        }\n"
		}
		table += `    }

    set whitelist6 {
        type ipv6_addr
        flags interval
        auto-merge
`
		if len(whiteIpv6List) > 0 {
			table += "        elements = {\n            " + strings.Join(whiteIpv6List, ",\n            ") + "\n        }\n"
		}
		table += `    }
`
	}

	// 198.18.0.0/15 and fc00::/7 are reserved for private use but used by fakedns

	table += `
    # 用于记录本机网卡 IP 地址（由 watcher 动态维护），
    # 避免发往本机地址的流量被重定向造成环路
    set local_ips {
        type ipv4_addr
        flags interval
        auto-merge
    }

    set local_ips6 {
        type ipv6_addr
        flags interval
        auto-merge
    }

    chain tp_out {
        meta mark & 0x80 == 0x80 return
        meta l4proto { tcp, udp } th dport 53 return
        meta l4proto { tcp, udp } fib saddr type local fib daddr type != local jump tp_rule
    }

    chain tp_pre {
        iifname "lo" mark & 0xc0 != 0x40 return
        meta l4proto { tcp, udp } th dport 53 return
        meta l4proto { tcp, udp } fib saddr type != local fib daddr type != local jump tp_rule
        # 通用 TPROXY 规则
        meta l4proto { tcp, udp } mark & 0xc0 == 0x40 tproxy ip to 127.0.0.1:52345
        meta l4proto { tcp, udp } mark & 0xc0 == 0x40 tproxy ip6 to [::1]:52345
    }

    chain output {
        type route hook output priority mangle - 5; policy accept;
        meta nfproto { ipv4, ipv6 } jump tp_out
    }

    chain prerouting {
        type filter hook prerouting priority mangle - 5; policy accept;
        meta nfproto { ipv4, ipv6 } jump tp_pre
    }

    chain tp_rule {
        meta mark set ct mark
        meta mark & 0xc0 == 0x40 return
`
	for _, v := range excludedIfs {
		table += fmt.Sprintf("        iifname %q return\n", v)
	}
	for _, v := range excludedIfs {
		table += fmt.Sprintf("        oifname %q return\n", v)
	}
	table += `
        ip daddr @local_ips return
`
	if hasWhitelist {
		table += `
        ip daddr @whitelist return
        ip6 daddr @whitelist6 return
`
	}
	table += `
        ip6 daddr @local_ips6 return
        jump tp_mark
    }

    chain tp_mark {
        tcp flags & (fin | syn | rst | ack) == syn meta mark set mark | 0x40
        meta l4proto udp ct state new meta mark set mark | 0x40
        ct mark set mark
    }
}
`
	if !ipv6Supported {
		// drop ipv6 packets hooks
		table = strings.ReplaceAll(table, "meta nfproto { ipv4, ipv6 }", "meta nfproto ipv4")
	}
	return table
}

func (t *nftTproxy) GetSetupCommands() Setter {
	whiteIpv4List, whiteIpv6List := GetWhiteListIPs()
	table := t.buildNftTable(whiteIpv4List, whiteIpv6List, GetExcludedInterfaces(), IsIPv6Supported())

	nftablesConf := asset.GetNftablesConfigPath()
	if err := os.WriteFile(nftablesConf, []byte(table), 0600); err != nil {
		return NewErrorSetter(err)
	}

	command := `
ip rule add fwmark 0x40/0xc0 table 100
ip route add local 0.0.0.0/0 dev lo table 100
`
	if IsIPv6Supported() {
		command += `
ip -6 rule add fwmark 0x40/0xc0 table 100
ip -6 route add local ::/0 dev lo table 100
`
	}

	command += `nft -f ` + shellWord(nftablesConf)
	return Setter{Cmds: command}
}

func (t *nftTproxy) GetCleanCommands() Setter {
	command := `
ip rule del fwmark 0x40/0xc0 table 100 2>/dev/null || true
ip route del local 0.0.0.0/0 dev lo table 100 2>/dev/null || true
`
	if IsIPv6Supported() {
		command += `
ip -6 rule del fwmark 0x40/0xc0 table 100 2>/dev/null || true
ip -6 route del local ::/0 dev lo table 100 2>/dev/null || true
		`
	}

	command += `nft delete table inet v2raya`
	command += "\nconntrack -D --mark 0x40 2>/dev/null || true"
	return Setter{Cmds: command}
}
