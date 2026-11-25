package iptables

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// DNSRedirect intercepts port 53 only for the configured listener's address
// family. A private loopback listener can receive locally generated traffic.
func DNSRedirect(addr string, share, ipv6, native bool, excluded []string) Setter {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return NewErrorSetter(err)
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return NewErrorSetter(fmt.Errorf("invalid DNS redirect port %q", port))
	}
	if host == "" {
		host = "::"
	}
	resolved, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		return NewErrorSetter(err)
	}
	ip := resolved.IP
	isV6 := ip.To4() == nil
	if isV6 && !ipv6 {
		return Setter{}
	}
	family, bin := "ipv4", "iptables"
	if isV6 {
		family, bin = "ipv6", "ip6tables"
	}
	target := net.JoinHostPort(ip.String(), port)
	var commands strings.Builder
	if native {
		commands.WriteString("nft add table inet v2raya_dns\n")
	}
	hooks := []string{"OUTPUT"}
	if share && !ip.IsLoopback() {
		hooks = append(hooks, "PREROUTING")
	}
	for _, hook := range hooks {
		if native {
			name := strings.ToLower(hook)
			add := func(expr string) {
				fmt.Fprintf(&commands, "nft %s\n", shellWord("add rule inet v2raya_dns "+name+" "+expr))
			}
			fmt.Fprintf(&commands, "nft %s\n", shellWord("add chain inet v2raya_dns "+name+" { type nat hook "+name+" priority -110; policy accept; }"))
			add("meta mark & 0x80 == 0x80 return")
			matchIf := "oifname"
			if hook == "PREROUTING" {
				matchIf = "iifname"
			}
			for _, iface := range excluded {
				add(fmt.Sprintf("%s %q return", matchIf, iface))
			}
			action := "redirect to :" + port
			if !ip.IsUnspecified() {
				typ := "ip"
				if isV6 {
					typ = "ip6"
				}
				action = "dnat " + typ + " to " + target
			}
			add("meta nfproto " + family + " meta l4proto { udp, tcp } th dport 53 " + action)
			continue
		}
		chain, matchIf := "V2RAYA_DNS_OUT", "-o"
		if hook == "PREROUTING" {
			chain, matchIf = "V2RAYA_DNS_PRE", "-i"
		}
		prefix := bin + " -w 2 -t nat "
		fmt.Fprintf(&commands, "%s-N %s\n%s-A %s -m mark --mark 0x80/0x80 -j RETURN\n", prefix, chain, prefix, chain)
		for _, iface := range excluded {
			fmt.Fprintf(&commands, "%s-A %s %s %s -j RETURN\n", prefix, chain, matchIf, shellWord(strings.ReplaceAll(iface, "*", "+")))
		}
		action := "REDIRECT --to-ports " + port
		if !ip.IsUnspecified() {
			action = "DNAT --to-destination " + target
		}
		for _, proto := range []string{"tcp", "udp"} {
			fmt.Fprintf(&commands, "%s-A %s -p %s --dport 53 -j %s\n", prefix, chain, proto, action)
			fmt.Fprintf(&commands, "%s-I %s -p %s --dport 53 -j %s\n", prefix, hook, proto, chain)
		}
	}
	return Setter{Cmds: commands.String()}
}

func CleanDNSRedirect() Setter {
	var b strings.Builder
	b.WriteString("nft delete table inet v2raya_dns 2>/dev/null || true\n")
	for _, bin := range []string{"iptables", "ip6tables"} {
		for _, item := range []struct{ hook, chain string }{{"OUTPUT", "V2RAYA_DNS_OUT"}, {"PREROUTING", "V2RAYA_DNS_PRE"}} {
			for _, proto := range []string{"tcp", "udp"} {
				fmt.Fprintf(&b, "%s -w 2 -t nat -D %s -p %s --dport 53 -j %s 2>/dev/null || true\n", bin, item.hook, proto, item.chain)
			}
			fmt.Fprintf(&b, "%s -w 2 -t nat -F %s 2>/dev/null || true\n%s -w 2 -t nat -X %s 2>/dev/null || true\n", bin, item.chain, bin, item.chain)
		}
	}
	return Setter{Cmds: b.String()}
}
