package configure

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// NormalizeNodeDNS accepts the transports supported by the DNS module. Keeping
// a canonical URL also makes conflicting policies for a shared hostname visible.
func NormalizeNodeDNS(servers []string) ([]string, error) {
	var result []string
	seen := map[string]bool{}
	for _, server := range servers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		if !strings.Contains(server, "://") {
			if ip := net.ParseIP(server); ip != nil {
				server = net.JoinHostPort(ip.String(), "53")
			}
			server = "udp://" + server
		}
		u, err := url.Parse(server)
		if err != nil {
			return nil, fmt.Errorf("invalid node DNS address %q", server)
		}
		if u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, fmt.Errorf("invalid node DNS address %q", server)
		}
		port := "53"
		switch u.Scheme {
		case "udp", "tcp":
		case "tls":
			port = "853"
		case "https":
			port = "443"
		default:
			return nil, fmt.Errorf("unsupported node DNS transport %q (use udp, tcp, tls or https)", u.Scheme)
		}
		if u.Scheme != "https" && (u.Path != "" || u.RawQuery != "") {
			return nil, fmt.Errorf("invalid node DNS path %q", server)
		}
		if u.Port() != "" {
			port = u.Port()
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid node DNS port %q", port)
		}
		u.Host = net.JoinHostPort(strings.ToLower(u.Hostname()), port)
		if u.Scheme == "https" && u.Path == "" {
			u.Path = "/dns-query"
		}
		server = u.String()
		if !seen[server] {
			result = append(result, server)
			seen[server] = true
		}
	}
	if len(result) > 8 {
		return nil, fmt.Errorf("at most 8 node DNS servers are supported")
	}
	return result, nil
}

func (e *SubscriptionExtra) Validate() error {
	if e.FilterField != "" && e.FilterField != "name" && e.FilterField != "host" {
		return fmt.Errorf("invalid filter field %q", e.FilterField)
	}
	for _, expression := range []string{e.IncludeRegex, e.ExcludeRegex} {
		if _, err := regexp.Compile(expression); err != nil {
			return fmt.Errorf("invalid node filter: %w", err)
		}
	}
	for _, protocol := range e.IncludeProtocols {
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(protocol) {
			return fmt.Errorf("invalid protocol filter %q", protocol)
		}
	}
	var err error
	e.NodeDNS, err = NormalizeNodeDNS(e.NodeDNS)
	return err
}

func (e SubscriptionExtra) EffectiveNodeDNS() []string {
	if !e.PreferNodeDNS {
		return nil
	}
	if len(e.NodeDNS) > 0 {
		return e.NodeDNS
	}
	return e.ProviderNodeDNS
}
