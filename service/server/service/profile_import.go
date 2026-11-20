package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
)

type ImportResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Reasons  []string `json:"reasons"`
}

const maxImportReasons = 25

func (r *ImportResult) skip(reason string) {
	r.Skipped++
	// Counts cover every entry even when a provider sends a very large list.
	if len(r.Reasons) < maxImportReasons {
		r.Reasons = append(r.Reasons, reason)
	}
}

type parsedProfile struct {
	nodes   []serverObj.ServerObj
	status  string
	nodeDNS []string
	report  ImportResult
}

func decodeProfileText(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "\uFEFF"))
	if decoded, err := common.Base64StdDecode(raw); err == nil {
		return strings.TrimSpace(strings.TrimPrefix(decoded, "\uFEFF"))
	}
	if decoded, err := common.Base64URLDecode(raw); err == nil {
		return strings.TrimSpace(strings.TrimPrefix(decoded, "\uFEFF"))
	}
	return raw
}

func parseProfile(raw string) (p parsedProfile, err error) {
	raw = decodeProfileText(raw)
	p.report.Reasons = []string{}
	switch {
	case isClashYAML(raw):
		p.nodes, err = resolveClashYAML(raw, &p.report)
		if err == nil {
			p.nodeDNS, err = resolveClashNodeDNS(raw)
			if err != nil {
				return p, errors.New("invalid Clash proxy-server-nameserver DNS settings")
			}
		}
	case strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "["):
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &fields) != nil {
			return p, errors.New("unsupported or invalid JSON profile; expected a SIP008 object")
		}
		for _, field := range []string{"outbounds", "endpoints", "inbounds", "route"} {
			if _, ok := fields[field]; ok {
				return p, errors.New("sing-box configuration JSON is not supported; import node links or Clash YAML")
			}
		}
		if _, ok := fields["servers"]; !ok {
			return p, errors.New("unsupported JSON profile; expected SIP008 servers")
		}
		var sip SIP008
		p.nodes, sip, err = resolveSIP008(raw, &p.report)
		p.status = getDataUsageStatus(sip.BytesUsed, sip.BytesRemaining)
	default:
		for i, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if value, ok := strings.CutPrefix(line, "STATUS="); ok {
				p.status = value
				continue
			}
			if !strings.Contains(line, "://") {
				p.report.skip(fmt.Sprintf("line %d: missing node URL scheme", i+1))
				continue
			}
			obj, err := ResolveURL(line)
			if err != nil || obj == nil {
				reason := "invalid node link parameters"
				if errors.Is(err, serverObj.ErrUnsupportedLinkType) {
					reason = "unsupported node link scheme"
				}
				p.report.skip(fmt.Sprintf("line %d: %s", i+1, reason))
				continue
			}
			p.nodes = append(p.nodes, obj)
		}
	}
	if err != nil {
		return p, err
	}
	seen := make(map[string]bool)
	unique := p.nodes[:0]
	for i, node := range p.nodes {
		link := node.ExportToURL()
		if seen[link] {
			p.report.skip(fmt.Sprintf("node %d: duplicate", i+1))
			continue
		}
		seen[link] = true
		unique = append(unique, node)
	}
	p.nodes = unique
	p.report.Imported = len(unique)
	if len(unique) == 0 {
		if len(p.report.Reasons) != 0 {
			return p, fmt.Errorf("no valid nodes (%d skipped): %s", p.report.Skipped, strings.Join(p.report.Reasons, "; "))
		}
		return p, errors.New("no valid nodes in the profile")
	}
	return p, nil
}
