package serverObj

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

func init() {
	FromLinkRegister("mieru", NewMieru)
	EmptyRegister("mieru", func() (ServerObj, error) {
		return new(Mieru), nil
	})
}

type Mieru struct {
	Name      string `json:"name"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Transport string `json:"transport"`
	Protocol  string `json:"protocol"`
	Link      string `json:"link"`
}

func NewMieru(link string) (ServerObj, error) {
	return ParseMieruURL(link)
}

// ParseMieruURL parses a link in the form
//
//	mieru://username:password@host:port?transport=TCP#name
func ParseMieruURL(link string) (data *Mieru, err error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("mieru: missing server address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return nil, fmt.Errorf("mieru: bad port: %w", err)
	}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	transport := u.Query().Get("transport")
	if transport == "" {
		transport = "TCP"
	}
	return &Mieru{
		Name:      u.Fragment,
		Server:    u.Hostname(),
		Port:      port,
		Username:  username,
		Password:  password,
		Transport: transport,
		Protocol:  "mieru",
		Link:      link,
	}, nil
}

// mieruSettings holds the settings serialized into the v2raya-core xray config.
type mieruSettings struct {
	// Address is "host:port".
	Address   string `json:"address"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Transport string `json:"transport,omitempty"`
}

func (s *Mieru) Configuration(info PriorInfo) (c Configuration, err error) {
	if s.Username == "" || s.Password == "" {
		return c, fmt.Errorf("mieru: username and password are required")
	}
	transport := s.Transport
	if transport == "" {
		transport = "TCP"
	}
	settingsJSON, err := json.Marshal(mieruSettings{
		Address:   net.JoinHostPort(s.Server, strconv.Itoa(s.Port)),
		Username:  s.Username,
		Password:  s.Password,
		Transport: transport,
	})
	if err != nil {
		return c, fmt.Errorf("mieru: marshal settings: %w", err)
	}
	return Configuration{
		CoreOutbound: coreObj.OutboundObject{
			Tag:      info.Tag,
			Protocol: "mieru",
			Settings: coreObj.Settings{Inlined: settingsJSON},
		},
		UDPSupport: false,
	}, nil
}

func (s *Mieru) ExportToURL() string {
	if s.Link != "" {
		return s.Link
	}
	u := url.URL{
		Scheme:   "mieru",
		User:     url.UserPassword(s.Username, s.Password),
		Host:     net.JoinHostPort(s.Server, strconv.Itoa(s.Port)),
		Fragment: s.Name,
	}
	q := url.Values{}
	if s.Transport != "" && !strings.EqualFold(s.Transport, "TCP") {
		q.Set("transport", s.Transport)
	}
	if encoded := q.Encode(); encoded != "" {
		u.RawQuery = encoded
	}
	return u.String()
}

func (s *Mieru) NeedPluginPort() bool {
	return false
}

func (s *Mieru) ProtoToShow() string {
	return fmt.Sprintf("Mieru(%v)", s.Transport)
}

func (s *Mieru) GetProtocol() string {
	return s.Protocol
}

func (s *Mieru) GetHostname() string {
	return s.Server
}

func (s *Mieru) GetPort() int {
	return s.Port
}

func (s *Mieru) GetName() string {
	return s.Name
}

func (s *Mieru) SetName(name string) {
	s.Name = name
}
