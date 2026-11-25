package dns

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// GeositeResolver resolves a geosite tag (e.g. "cn", "geolocation-!cn", "google@cn")
// into domain and suffix patterns. Returns (domains, suffixes) where domains may
// contain plain exact domains or prefixed patterns ("full:", "domain:", "keyword:",
// "regexp:") and suffixes are domain suffix patterns.
// Nil means the tag is not recognized/loaded.
type GeositeResolver func(tag string) (domains, suffixes []string)

// normalizeDomain normalizes a DNS domain name to lowercase with any trailing
// root dot stripped (preserving "." for the DNS root zone).
func normalizeDomain(name string) string {
	name = strings.ToLower(name)
	if name == "." {
		return "."
	}
	return strings.TrimSuffix(name, ".")
}

// compileCaseInsensitiveRegex validates pattern first (so unmatched parentheses
// cannot accidentally balance with the wrapper) and compiles it case-insensitively.
func compileCaseInsensitiveRegex(pattern string) (*regexp.Regexp, error) {
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, err
	}
	return regexp.Compile("(?i:" + pattern + ")")
}

// RuleMatcher is the interface for DNS query rule matchers.
type RuleMatcher interface {
	// Match checks whether the given query matches this rule.
	Match(query *DnsQuery) bool
	// Type returns the matcher type description (e.g., "domain", "ip", "querytype", "clientip").
	Type() string
	// Value returns the matcher value description.
	Value() string
}

// NeverMatcher never matches any query. It is inserted when a rule specifies
// domain constraints (such as an unresolved geosite: tag) that yield zero
// matchers, ensuring the rule never degrades into a match-all rule.
type NeverMatcher struct {
	Reason string
}

// Match always returns false.
func (m *NeverMatcher) Match(*DnsQuery) bool {
	return false
}

// Type returns the matcher type.
func (m *NeverMatcher) Type() string {
	return "never"
}

// Value returns the reason this matcher never matches.
func (m *NeverMatcher) Value() string {
	return m.Reason
}

// DomainRule matches DNS queries by domain name.
// It supports exact match, suffix match (wildcard), keyword (substring) match,
// and regex match, all evaluated case-insensitively with trailing dots stripped.
type DomainRule struct {
	Domain    string
	IsSuffix  bool // true: match suffix like .example.com; false: exact match
	IsRegex   bool // true: regex match
	IsKeyword bool // true: case-insensitive substring match
	Re        *regexp.Regexp
}

// Match checks if the query domain matches this domain rule.
func (r *DomainRule) Match(query *DnsQuery) bool {
	if query == nil {
		return false
	}
	name := normalizeDomain(query.Name)

	if r.IsRegex {
		if r.Re == nil {
			return false
		}
		return r.Re.MatchString(name)
	}

	if r.IsKeyword {
		kw := strings.ToLower(r.Domain)
		if kw == "" {
			return false
		}
		return strings.Contains(name, kw)
	}

	if r.IsSuffix {
		// Suffix match: domain ".example.com" matches "sub.example.com" (strict subdomain);
		// "example.com" matches "example.com" itself and "sub.example.com".
		domain := normalizeDomain(r.Domain)
		if domain == "" {
			return false
		}
		if strings.HasPrefix(domain, ".") {
			// ".example.com" matches anything ending with ".example.com"
			return strings.HasSuffix(name, domain)
		}
		// "example.com" matches "example.com" itself and "sub.example.com"
		if name == domain {
			return true
		}
		return strings.HasSuffix(name, "."+domain)
	}

	// Exact match
	domain := normalizeDomain(r.Domain)
	return name == domain
}

// Type returns the matcher type.
func (r *DomainRule) Type() string {
	if r.IsRegex {
		return "domain_regex"
	}
	if r.IsKeyword {
		return "domain_keyword"
	}
	if r.IsSuffix {
		return "domain_suffix"
	}
	return "domain"
}

// Value returns the matcher value.
func (r *DomainRule) Value() string {
	return r.Domain
}

// IPRule matches resolved DNS response IPs (A/AAAA answer records) and IP-based
// queries (IP literals or reverse-DNS PTR names under in-addr.arpa / ip6.arpa)
// against a CIDR range. Unlike ClientIPRule, IPRule never inspects query.ClientIP.
type IPRule struct {
	CIDR *net.IPNet
}

// Match checks whether the query name itself encodes an IP (literal or PTR
// reverse-DNS name) within this CIDR range. For matching client source IPs,
// use ClientIPRule; for matching resolved answer IPs, use MatchResponse.
func (r *IPRule) Match(query *DnsQuery) bool {
	if r.CIDR == nil || query == nil {
		return false
	}
	ip := parseQueryIP(query.Name)
	if ip == nil {
		return false
	}
	return r.CIDR.Contains(ip)
}

// MatchIP checks whether ip falls within this CIDR range.
func (r *IPRule) MatchIP(ip net.IP) bool {
	if r.CIDR == nil || ip == nil {
		return false
	}
	return r.CIDR.Contains(ip)
}

// MatchResponse reports whether any A or AAAA record in resp.Answer falls
// within this CIDR range.
func (r *IPRule) MatchResponse(resp *DnsResponse) bool {
	if r.CIDR == nil || resp == nil {
		return false
	}
	for _, rr := range resp.Answer {
		switch v := rr.(type) {
		case *dns.A:
			if r.CIDR.Contains(v.A) {
				return true
			}
		case *dns.AAAA:
			if r.CIDR.Contains(v.AAAA) {
				return true
			}
		}
	}
	return false
}

// parseQueryIP extracts an IP address from an IP literal or reverse-DNS PTR name
// (e.g. "1.0.168.192.in-addr.arpa" or "...ip6.arpa"). Returns nil for normal domains.
func parseQueryIP(name string) net.IP {
	n := normalizeDomain(name)
	if n == "" || n == "." {
		return nil
	}
	if ip := net.ParseIP(n); ip != nil {
		return ip
	}
	if strings.HasSuffix(n, ".in-addr.arpa") {
		prefix := strings.TrimSuffix(n, ".in-addr.arpa")
		parts := strings.Split(prefix, ".")
		if len(parts) != 4 {
			return nil
		}
		var b [4]byte
		for i := 0; i < 4; i++ {
			v, err := strconv.Atoi(parts[3-i])
			if err != nil || v < 0 || v > 255 {
				return nil
			}
			b[i] = byte(v)
		}
		return net.IPv4(b[0], b[1], b[2], b[3])
	}
	if strings.HasSuffix(n, ".ip6.arpa") {
		prefix := strings.TrimSuffix(n, ".ip6.arpa")
		parts := strings.Split(prefix, ".")
		if len(parts) != 32 {
			return nil
		}
		var b [16]byte
		for i := 0; i < 16; i++ {
			hi, err1 := strconv.ParseUint(parts[31-2*i], 16, 4)
			lo, err2 := strconv.ParseUint(parts[30-2*i], 16, 4)
			if err1 != nil || err2 != nil || len(parts[31-2*i]) != 1 || len(parts[30-2*i]) != 1 {
				return nil
			}
			b[i] = byte(hi<<4 | lo)
		}
		return net.IP(b[:])
	}
	return nil
}

// Type returns the matcher type.
func (r *IPRule) Type() string {
	return "ip"
}

// Value returns the matcher value.
func (r *IPRule) Value() string {
	if r.CIDR == nil {
		return ""
	}
	return r.CIDR.String()
}

// QueryTypeRule matches DNS queries by query type (A, AAAA, etc.).
type QueryTypeRule struct {
	QType QueryType
}

// Match checks if the query type matches this rule.
func (r *QueryTypeRule) Match(query *DnsQuery) bool {
	return r.QType == query.QType
}

// Type returns the matcher type.
func (r *QueryTypeRule) Type() string {
	return "query_type"
}

// Value returns the matcher value.
func (r *QueryTypeRule) Value() string {
	return fmt.Sprintf("%d", uint16(r.QType))
}

// ClientIPRule matches DNS queries by client IP CIDR range.
type ClientIPRule struct {
	CIDR *net.IPNet
}

// Match checks if the query's client IP matches this rule.
func (r *ClientIPRule) Match(query *DnsQuery) bool {
	if r.CIDR == nil || query.ClientIP == nil {
		return false
	}
	return r.CIDR.Contains(query.ClientIP)
}

// Type returns the matcher type.
func (r *ClientIPRule) Type() string {
	return "client_ip"
}

// Value returns the matcher value.
func (r *ClientIPRule) Value() string {
	if r.CIDR == nil {
		return ""
	}
	return r.CIDR.String()
}

// OrMatcher matches if ANY of its child matchers match (OR logic).
// It is used to group matchers from the same rule field together,
// so that multiple domain patterns from a geosite expansion are OR-ed.
type OrMatcher struct {
	Matchers []RuleMatcher
}

// Match returns true if any child matcher matches.
func (m *OrMatcher) Match(query *DnsQuery) bool {
	for _, matcher := range m.Matchers {
		if matcher.Match(query) {
			return true
		}
	}
	return false
}

// Type returns the matcher type.
func (m *OrMatcher) Type() string {
	return "or_group"
}

// Value returns a description of the group.
func (m *OrMatcher) Value() string {
	return fmt.Sprintf("%d matchers", len(m.Matchers))
}

// parseCIDR parses a CIDR string and returns the IPNet.
// Supports both regular CIDR (e.g., "10.0.0.0/8") and single IP (e.g., "10.0.0.1").
func parseCIDR(s string) (*net.IPNet, error) {
	if strings.Contains(s, "/") {
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		return cidr, nil
	}
	// Single IP: treat as /32 for IPv4 or /128 for IPv6
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", s)
	}
	if ip.To4() != nil {
		_, cidr, err := net.ParseCIDR(s + "/32")
		if err != nil {
			return nil, err
		}
		return cidr, nil
	}
	_, cidr, err := net.ParseCIDR(s + "/128")
	if err != nil {
		return nil, err
	}
	return cidr, nil
}

// buildDomainPatternMatcher parses a domain pattern (supporting full:, domain:,
// keyword:, regexp: prefixes) and appends the resulting matcher.
// When skipInvalid is true (used for geosite expansion), invalid patterns are
// logged and skipped rather than failing rule compilation.
func appendDomainPattern(out *[]RuleMatcher, pattern string, defaultSuffix bool, skipInvalid bool) error {
	switch {
	case strings.HasPrefix(pattern, "full:"):
		val := normalizeDomain(strings.TrimPrefix(pattern, "full:"))
		if val == "" {
			if skipInvalid {
				return nil
			}
			return fmt.Errorf("empty full domain pattern %q", pattern)
		}
		*out = append(*out, &DomainRule{Domain: val, IsSuffix: false})
		return nil

	case strings.HasPrefix(pattern, "domain:"):
		val := normalizeDomain(strings.TrimPrefix(pattern, "domain:"))
		if val == "" {
			if skipInvalid {
				return nil
			}
			return fmt.Errorf("empty domain suffix pattern %q", pattern)
		}
		*out = append(*out, &DomainRule{Domain: val, IsSuffix: true})
		return nil

	case strings.HasPrefix(pattern, "keyword:"):
		val := strings.ToLower(strings.TrimPrefix(pattern, "keyword:"))
		if val == "" {
			if skipInvalid {
				return nil
			}
			return fmt.Errorf("empty keyword pattern %q", pattern)
		}
		*out = append(*out, &DomainRule{Domain: val, IsKeyword: true})
		return nil

	case strings.HasPrefix(pattern, "regexp:"):
		expr := strings.TrimPrefix(pattern, "regexp:")
		if expr == "" {
			if skipInvalid {
				return nil
			}
			return fmt.Errorf("empty domain regex pattern %q", pattern)
		}
		re, err := compileCaseInsensitiveRegex(expr)
		if err != nil {
			if skipInvalid {
				log.Printf("[dns rule] skipping invalid geosite regex %q: %v", expr, err)
				return nil
			}
			return fmt.Errorf("invalid domain regex %q: %w", expr, err)
		}
		*out = append(*out, &DomainRule{Domain: expr, IsRegex: true, Re: re})
		return nil

	default:
		val := normalizeDomain(pattern)
		if val == "" {
			return nil
		}
		*out = append(*out, &DomainRule{Domain: val, IsSuffix: defaultSuffix})
		return nil
	}
}

// BuildMatchers constructs a list of RuleMatcher from a DnsRule configuration.
//
// Matching semantics: within each field group, matchers are OR-ed (any one match
// is sufficient); across different field groups, matchers are AND-ed (all groups
// must match). For example, a rule with domain=["geosite:cn"] and query_type=[A]
// matches if the domain is in geosite:cn AND the query type is A.
//
// If a resolver is provided, geosite tags (e.g. "geosite:cn", "geosite:google@cn")
// are expanded into individual domain matchers, all grouped into a single OR group.
// If a rule specifies domain constraints but none resolve to a concrete matcher
// (e.g. missing geosite.dat or unknown tag), a NeverMatcher is inserted so the
// rule never degrades into a match-all rule.
func BuildMatchers(rule *DnsRule, resolver GeositeResolver) ([]RuleMatcher, error) {
	// Collect matchers by field group. Each group uses OR logic internally.
	var domainMatchers []RuleMatcher
	var ipMatchers []RuleMatcher
	var qtypeMatchers []RuleMatcher
	var clientIPMatchers []RuleMatcher

	hasDomainConstraint := len(rule.Domain) > 0 || len(rule.DomainSuffix) > 0 || len(rule.DomainRegex) > 0

	// ── Domain group (OR): exact + suffix + keyword + regex + geosite expansion ──

	expandGeosite := func(tag string) {
		if resolver == nil {
			return
		}
		doms, suffs := resolver(tag)
		for _, dom := range doms {
			_ = appendDomainPattern(&domainMatchers, dom, false, true)
		}
		for _, suf := range suffs {
			_ = appendDomainPattern(&domainMatchers, suf, true, true)
		}
	}

	for _, d := range rule.Domain {
		if strings.HasPrefix(d, "geosite:") {
			expandGeosite(strings.TrimPrefix(d, "geosite:"))
			continue
		}
		if err := appendDomainPattern(&domainMatchers, d, false, false); err != nil {
			return nil, err
		}
	}

	for _, d := range rule.DomainSuffix {
		if strings.HasPrefix(d, "geosite:") {
			expandGeosite(strings.TrimPrefix(d, "geosite:"))
			continue
		}
		if err := appendDomainPattern(&domainMatchers, d, true, false); err != nil {
			return nil, err
		}
	}

	for _, d := range rule.DomainRegex {
		if d == "" {
			return nil, fmt.Errorf("empty domain regex")
		}
		re, err := compileCaseInsensitiveRegex(d)
		if err != nil {
			return nil, fmt.Errorf("invalid domain regex %q: %w", d, err)
		}
		domainMatchers = append(domainMatchers, &DomainRule{Domain: d, IsRegex: true, Re: re})
	}

	// Never let a rule with unresolved/empty domain constraints silently become match-all.
	if hasDomainConstraint && len(domainMatchers) == 0 {
		domainMatchers = append(domainMatchers, &NeverMatcher{Reason: "unresolved domain constraint"})
	}

	// ── IP group (OR) ──
	for _, ipStr := range rule.IP {
		cidr, err := parseCIDR(ipStr)
		if err != nil {
			return nil, fmt.Errorf("invalid IP CIDR %q: %w", ipStr, err)
		}
		ipMatchers = append(ipMatchers, &IPRule{CIDR: cidr})
	}

	// ── QueryType group (OR) ──
	for _, qt := range rule.QueryType {
		qtypeMatchers = append(qtypeMatchers, &QueryTypeRule{QType: qt})
	}

	// ── ClientIP group (OR) ──
	for _, cipStr := range rule.ClientIP {
		cidr, err := parseCIDR(cipStr)
		if err != nil {
			return nil, fmt.Errorf("invalid client IP CIDR %q: %w", cipStr, err)
		}
		clientIPMatchers = append(clientIPMatchers, &ClientIPRule{CIDR: cidr})
	}

	// ── Build final list: each group is OR internally, AND across groups ──
	var matchers []RuleMatcher

	if len(domainMatchers) > 0 {
		if len(domainMatchers) == 1 {
			matchers = append(matchers, domainMatchers[0])
		} else {
			matchers = append(matchers, &OrMatcher{Matchers: domainMatchers})
		}
	}
	if len(ipMatchers) > 0 {
		if len(ipMatchers) == 1 {
			matchers = append(matchers, ipMatchers[0])
		} else {
			matchers = append(matchers, &OrMatcher{Matchers: ipMatchers})
		}
	}
	if len(qtypeMatchers) > 0 {
		if len(qtypeMatchers) == 1 {
			matchers = append(matchers, qtypeMatchers[0])
		} else {
			matchers = append(matchers, &OrMatcher{Matchers: qtypeMatchers})
		}
	}
	if len(clientIPMatchers) > 0 {
		if len(clientIPMatchers) == 1 {
			matchers = append(matchers, clientIPMatchers[0])
		} else {
			matchers = append(matchers, &OrMatcher{Matchers: clientIPMatchers})
		}
	}

	return matchers, nil
}

// MatchAll checks if all matcher groups match the query (AND across groups).
// Within each group (e.g. OrMatcher), the semantics is OR (any one matches).
// Empty matchers list means no constraints — matches everything.
func MatchAll(matchers []RuleMatcher, query *DnsQuery) bool {
	if len(matchers) == 0 {
		// Empty matchers means no constraints — matches everything.
		return true
	}
	for _, m := range matchers {
		if !m.Match(query) {
			return false
		}
	}
	return true
}
