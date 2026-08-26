package serverObj

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

func init() {
	FromLinkRegister("shadowtls", NewShadowTLS)
	EmptyRegister("shadowtls", func() (ServerObj, error) {
		return new(ShadowTLS), nil
	})
}

type ShadowTLS struct {
	Name     string `json:"name"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Version  int    `json:"version"`
	Sni      string `json:"sni,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
	Protocol string `json:"protocol"`
	Link     string `json:"link"`
}

func NewShadowTLS(link string) (ServerObj, error) {
	return ParseShadowTLSURL(link)
}

// ParseShadowTLSURL parses a link in the form
//
//	shadowtls://password@host:port?sni=example.com&version=3&insecure=1#name
func ParseShadowTLSURL(link string) (data *ShadowTLS, err error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("shadowtls: missing server address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return nil, fmt.Errorf("shadowtls: bad port: %w", err)
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			password += ":" + pw
		}
	}
	version := 3
	if v := u.Query().Get("version"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			version = n
		}
	}
	return &ShadowTLS{
		Name:     u.Fragment,
		Server:   u.Hostname(),
		Port:     port,
		Password: password,
		Version:  version,
		Sni:      u.Query().Get("sni"),
		Insecure: u.Query().Get("insecure") == "1" || u.Query().Get("allowInsecure") == "1",
		Protocol: "shadowtls",
		Link:     link,
	}, nil
}

// shadowtlsSettings holds the settings serialized into the v2raya-core xray config.
type shadowtlsSettings struct {
	Address  string `json:"address"`
	Password string `json:"password"`
	Version  int32  `json:"version"`
	Sni      string `json:"sni,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

func (s *ShadowTLS) Configuration(info PriorInfo) (c Configuration, err error) {
	if s.Password == "" {
		return c, fmt.Errorf("shadowtls: password is required")
	}
	version := s.Version
	if version == 0 {
		version = 3
	}
	settingsJSON, err := json.Marshal(shadowtlsSettings{
		Address:  net.JoinHostPort(s.Server, strconv.Itoa(s.Port)),
		Password: s.Password,
		Version:  int32(version),
		Sni:      s.Sni,
		Insecure: s.Insecure,
	})
	if err != nil {
		return c, fmt.Errorf("shadowtls: marshal settings: %w", err)
	}
	return Configuration{
		CoreOutbound: coreObj.OutboundObject{
			Tag:      info.Tag,
			Protocol: "shadowtls",
			Settings: coreObj.Settings{Inlined: settingsJSON},
		},
		UDPSupport: false,
	}, nil
}

func (s *ShadowTLS) ExportToURL() string {
	if s.Link != "" {
		return s.Link
	}
	u := url.URL{
		Scheme:   "shadowtls",
		User:     url.User(s.Password),
		Host:     net.JoinHostPort(s.Server, strconv.Itoa(s.Port)),
		Fragment: s.Name,
	}
	q := url.Values{}
	if s.Version != 0 && s.Version != 3 {
		q.Set("version", strconv.Itoa(s.Version))
	}
	if s.Sni != "" {
		q.Set("sni", s.Sni)
	}
	if s.Insecure {
		q.Set("insecure", "1")
	}
	if encoded := q.Encode(); encoded != "" {
		u.RawQuery = encoded
	}
	return u.String()
}

func (s *ShadowTLS) NeedPluginPort() bool {
	return false
}

func (s *ShadowTLS) ProtoToShow() string {
	return fmt.Sprintf("ShadowTLS(v%v)", s.Version)
}

func (s *ShadowTLS) GetProtocol() string {
	return s.Protocol
}

func (s *ShadowTLS) GetHostname() string {
	return s.Server
}

func (s *ShadowTLS) GetPort() int {
	return s.Port
}

func (s *ShadowTLS) GetName() string {
	return s.Name
}

func (s *ShadowTLS) SetName(name string) {
	s.Name = name
}
