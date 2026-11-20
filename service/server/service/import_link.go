package service

import (
	"errors"
	"net/url"
	"strings"

	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
)

type ImportLink struct {
	URL     string   `json:"url"`
	Remarks string   `json:"remarks"`
	NodeDNS []string `json:"nodeDNS"`
}

func isInstallScheme(scheme string) bool {
	return scheme == "karing" || scheme == "clash" || scheme == "sing-box"
}

// ParseImportLink only inspects link metadata. Decode the embedded URL once
// so signing parameters and escaped delimiters survive the confirmation step.
func ParseImportLink(input string) (ImportLink, error) {
	var result ImportLink
	raw := strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(raw), "sub://") {
		payload := raw[len("sub://"):]
		var err error
		if raw, err = common.Base64StdDecode(payload); err != nil {
			if raw, err = common.Base64URLDecode(payload); err != nil {
				return result, errors.New("invalid base64 subscription link")
			}
		}
	} else if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return result, errors.New("invalid subscription URL")
	}
	var dns string
	if isInstallScheme(u.Scheme) {
		action := "install-config"
		if u.Scheme == "sing-box" {
			action = "import-remote-profile"
		}
		if !strings.EqualFold(u.Host, action) || (u.Path != "" && u.Path != "/") || u.User != nil {
			return result, errors.New("unsupported installation link action")
		}
		params, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(params["url"]) != 1 {
			return result, errors.New("installation link must contain one valid url parameter")
		}
		result.Remarks = params.Get("name")
		if result.Remarks == "" {
			result.Remarks = u.Fragment
		}
		dns = params.Get("outbound-dns")
		raw = params.Get("url")
		u, err = url.Parse(raw)
		if err != nil {
			return result, errors.New("invalid embedded subscription URL")
		}
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return result, errors.New("subscription source must be an HTTP or HTTPS URL")
	}
	result.URL = raw
	query := u.Query()
	if result.Remarks == "" {
		result.Remarks = u.Fragment
	}
	if result.Remarks == "" {
		result.Remarks = query.Get("remarks")
	}
	if result.Remarks == "" {
		result.Remarks = query.Get("name")
	}
	if dns == "" && u.Scheme == "https" {
		dns = query.Get("outbound-dns")
	}
	result.NodeDNS, err = configure.NormalizeNodeDNS(strings.Split(dns, ","))
	if err != nil {
		return ImportLink{}, errors.New("invalid outbound-dns in subscription link")
	}
	if result.NodeDNS == nil {
		result.NodeDNS = []string{}
	}
	return result, nil
}
