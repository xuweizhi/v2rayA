package iptables

import (
	"strings"
	"testing"
)

func TestDNSRedirectUsesListenerScope(t *testing.T) {
	for _, tc := range []struct {
		addr                string
		share, ipv6, native bool
		want, absent        []string
	}{
		{"127.0.0.1:5354", true, true, false, []string{"-I OUTPUT", "DNAT --to-destination 127.0.0.1:5354", "--mark 0x80/0x80", "-o 'eth+'"}, []string{"PREROUTING", "ip6tables", "52353"}},
		{"0.0.0.0:5355", true, false, false, []string{"-I PREROUTING", "-i 'eth+'", "REDIRECT --to-ports 5355"}, []string{"ip6tables"}},
		{"192.0.2.3:5355", false, false, true, []string{"hook output", "dnat ip to 192.0.2.3:5355", "meta nfproto ipv4", `oifname "eth*"`}, []string{"prerouting", "ip6tables"}},
		{"[::]:5356", true, true, true, []string{"hook prerouting", "meta nfproto ipv6", "redirect to :5356", `iifname "eth*"`}, []string{"iptables", "52353"}},
		{"[2001:db8::3]:5357", true, true, true, []string{"dnat ip6 to [2001:db8::3]:5357", "meta mark & 0x80 == 0x80 return"}, nil},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			got := DNSRedirect(tc.addr, tc.share, tc.ipv6, tc.native, []string{"eth*"}).Cmds
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %s", want, got)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("unexpected %q in %s", absent, got)
				}
			}
		})
	}
	if DNSRedirect("[::1]:5356", true, false, false, nil).Cmds != "" {
		t.Fatal("rules generated for disabled IPv6")
	}
	if setter := DNSRedirect("127.0.0.1:0", true, false, false, nil); setter.PreFunc == nil || setter.PreFunc() == nil {
		t.Fatal("invalid listener port accepted")
	}
}
