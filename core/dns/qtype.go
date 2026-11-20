package dns

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// QTypeToString converts a QueryType to its string representation.
// For example, TypeA → "A", TypeAAAA → "AAAA".
// If the query type is not in the supported list, it returns the
// numeric string representation (e.g., "TYPE99").
func QTypeToString(qt QueryType) string {
	switch qt {
	case TypeA:
		return "A"
	case TypeAAAA:
		return "AAAA"
	case TypeCNAME:
		return "CNAME"
	case TypeTXT:
		return "TXT"
	case TypeMX:
		return "MX"
	case TypeSRV:
		return "SRV"
	case TypeNS:
		return "NS"
	case TypePTR:
		return "PTR"
	case TypeSOA:
		return "SOA"
	default:
		// Try miekg/dns string representation first.
		s := dns.TypeToString[uint16(qt)]
		if s != "" {
			return s
		}
		return fmt.Sprintf("TYPE%d", uint16(qt))
	}
}

// StringToQType converts a string to a QueryType.
// It supports standard type names ("A", "AAAA", etc.) and
// numeric format ("1", "28", "TYPE1", "TYPE28").
// Returns an error if the string does not represent a valid query type.
func StringToQType(s string) (QueryType, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("dns qtype: empty string")
	}

	// Try uppercase name lookup first.
	upper := strings.ToUpper(s)
	if qt, ok := dns.StringToType[upper]; ok {
		return QueryType(qt), nil
	}
	num, err := strconv.ParseUint(strings.TrimPrefix(upper, "TYPE"), 10, 16)
	if err == nil {
		return QueryType(num), nil
	}
	return 0, fmt.Errorf("dns qtype: unknown query type: %q", s)
}

// SupportsQType returns true if the given QueryType is one of the
// supported types (A, AAAA, CNAME, TXT, MX, SRV, NS, PTR, SOA).
func SupportsQType(qt QueryType) bool {
	switch qt {
	case TypeA, TypeAAAA, TypeCNAME, TypeTXT,
		TypeMX, TypeSRV, TypeNS, TypePTR, TypeSOA:
		return true
	default:
		return false
	}
}

// SupportedQTypes returns a list of all supported query types.
func SupportedQTypes() []QueryType {
	return []QueryType{
		TypeA,
		TypeAAAA,
		TypeCNAME,
		TypeTXT,
		TypeMX,
		TypeSRV,
		TypeNS,
		TypePTR,
		TypeSOA,
	}
}

// IsAddressQuery returns true if the query type is A or AAAA.
func IsAddressQuery(qt QueryType) bool {
	return qt == TypeA || qt == TypeAAAA
}

// NeedsDNS64 returns true if DNS64 synthesis should be considered
// for this query type (i.e., AAAA query).
func NeedsDNS64(qt QueryType) bool {
	return qt == TypeAAAA
}
