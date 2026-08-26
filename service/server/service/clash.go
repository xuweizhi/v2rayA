package service

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// isClashYAML reports whether the raw subscription content looks like a Clash
// (mihomo) YAML config, i.e. it contains a top-level "proxies:" mapping.
func isClashYAML(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "proxies:") || strings.Contains(trimmed, "\nproxies:")
}

// resolveClashYAML parses a Clash subscription YAML and converts each proxy
// under "proxies:" into a serverObj. Unsupported proxy types are skipped.
func resolveClashYAML(raw string) (infos []serverObj.ServerObj, status string, err error) {
	var cfg struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	if err = yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, "", fmt.Errorf("failed to parse clash yaml: %w", err)
	}
	infos = make([]serverObj.ServerObj, 0, len(cfg.Proxies))
	for _, p := range cfg.Proxies {
		obj, err := clashProxyToServerObj(p)
		if err != nil {
			log.Warn("resolveClashYAML: skip proxy %q: %v", getString(p, "name"), err)
			continue
		}
		if obj != nil {
			infos = append(infos, obj)
		}
	}
	return infos, "", nil
}

// ---- yaml helpers ----

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case int:
			return strconv.Itoa(t)
		case int64:
			return strconv.FormatInt(t, 10)
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(t)
		}
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case int64:
			return int(t)
		case uint64:
			return int(t)
		case float64:
			return int(t)
		case string:
			n, _ := strconv.Atoi(t)
			return n
		}
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			return strings.EqualFold(t, "true") || t == "1"
		}
	}
	return false
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
		if mm, ok := v.(map[interface{}]interface{}); ok {
			out := make(map[string]interface{}, len(mm))
			for k, vv := range mm {
				out[fmt.Sprint(k)] = vv
			}
			return out
		}
	}
	return nil
}

func getStringList(m map[string]interface{}, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case []interface{}:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = strings.Split(t, ",")
	}
	return out
}

// ---- proxy converters ----

func clashProxyToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	typ := strings.ToLower(getString(p, "type"))
	switch typ {
	case "mieru":
		return clashMieruToServerObj(p)
	case "vless":
		return clashVlessToServerObj(p)
	case "vmess":
		return clashVmessToServerObj(p)
	case "ss":
		return clashSSToServerObj(p)
	case "trojan":
		return clashTrojanToServerObj(p)
	case "hysteria2", "hy2":
		return clashHysteria2ToServerObj(p)
	default:
		return nil, fmt.Errorf("unsupported clash proxy type %q", typ)
	}
}

func clashMieruToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 {
		return nil, fmt.Errorf("mieru: missing server or port")
	}
	transport := getString(p, "transport")
	if transport == "" {
		transport = "TCP"
	}
	return &serverObj.Mieru{
		Name:      getString(p, "name"),
		Server:    server,
		Port:      port,
		Username:  getString(p, "username"),
		Password:  getString(p, "password"),
		Transport: transport,
		Protocol:  "mieru",
		Link:      mieruLink(p),
	}, nil
}

func mieruLink(p map[string]interface{}) string {
	u := url.URL{
		Scheme: "mieru",
		User:   url.UserPassword(getString(p, "username"), getString(p, "password")),
		Host:   net.JoinHostPort(getString(p, "server"), strconv.Itoa(getInt(p, "port"))),
	}
	if t := getString(p, "transport"); t != "" && !strings.EqualFold(t, "TCP") {
		u.RawQuery = "transport=" + url.QueryEscape(t)
	}
	u.Fragment = getString(p, "name")
	return u.String()
}

func clashVlessToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 || getString(p, "uuid") == "" {
		return nil, fmt.Errorf("vless: missing server, port or uuid")
	}
	obj := &serverObj.V2Ray{
		Ps:       getString(p, "name"),
		Add:      server,
		Port:     strconv.Itoa(port),
		ID:       getString(p, "uuid"),
		Protocol: "vless",
		Net:      getString(p, "network"),
		Type:     "none",
		TLS:      "none",
	}
	if obj.Net == "" {
		obj.Net = "tcp"
	}
	if getBool(p, "tls") {
		if ro := getMap(p, "reality-opts"); ro != nil {
			obj.TLS = "reality"
			obj.PublicKey = getString(ro, "public-key")
			obj.ShortId = getString(ro, "short-id")
		} else {
			obj.TLS = "tls"
		}
	}
	obj.SNI = getString(p, "servername")
	obj.Fingerprint = getString(p, "client-fingerprint")
	obj.Flow = getString(p, "flow")
	obj.Encryption = getString(p, "encryption")
	if smux := getMap(p, "smux"); smux != nil && getBool(smux, "enabled") {
		proto := getString(smux, "protocol")
		if proto == "" {
			proto = "smux"
		}
		obj.Mux = proto
	}
	if alpn := getStringList(p, "alpn"); len(alpn) > 0 {
		obj.Alpn = strings.Join(alpn, ",")
	}
	switch obj.Net {
	case "ws":
		if ws := getMap(p, "ws-opts"); ws != nil {
			obj.Path = getString(ws, "path")
			if headers := getMap(ws, "headers"); headers != nil {
				obj.Host = getString(headers, "Host")
			}
		}
	case "grpc":
		if grpc := getMap(p, "grpc-opts"); grpc != nil {
			obj.Path = getString(grpc, "grpc-service-name")
		}
	case "h2":
		if h2 := getMap(p, "h2-opts"); h2 != nil {
			obj.Path = getString(h2, "path")
			obj.Host = getString(h2, "host")
		}
	case "http":
		obj.Net = "h2"
		if http := getMap(p, "http-opts"); http != nil {
			obj.Path = getString(http, "path")
			obj.Host = getString(http, "host")
		}
	}
	return obj, nil
}

func clashVmessToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 || getString(p, "uuid") == "" {
		return nil, fmt.Errorf("vmess: missing server, port or uuid")
	}
	obj := &serverObj.V2Ray{
		Ps:       getString(p, "name"),
		Add:      server,
		Port:     strconv.Itoa(port),
		ID:       getString(p, "uuid"),
		Aid:      getString(p, "alterId"),
		Security: getString(p, "cipher"),
		Protocol: "vmess",
		Net:      getString(p, "network"),
		Type:     "none",
		TLS:      "none",
		V:        "2",
	}
	if obj.Aid == "" {
		obj.Aid = "0"
	}
	if obj.Security == "" {
		obj.Security = "auto"
	}
	if obj.Net == "" {
		obj.Net = "tcp"
	}
	if getBool(p, "tls") {
		obj.TLS = "tls"
	}
	obj.SNI = getString(p, "servername")
	obj.Fingerprint = getString(p, "client-fingerprint")
	if alpn := getStringList(p, "alpn"); len(alpn) > 0 {
		obj.Alpn = strings.Join(alpn, ",")
	}
	switch obj.Net {
	case "ws":
		if ws := getMap(p, "ws-opts"); ws != nil {
			obj.Path = getString(ws, "path")
			if headers := getMap(ws, "headers"); headers != nil {
				obj.Host = getString(headers, "Host")
			}
		}
	case "grpc":
		if grpc := getMap(p, "grpc-opts"); grpc != nil {
			obj.Path = getString(grpc, "grpc-service-name")
		}
	case "h2":
		if h2 := getMap(p, "h2-opts"); h2 != nil {
			obj.Path = getString(h2, "path")
			obj.Host = getString(h2, "host")
		}
	case "http":
		obj.Net = "h2"
		if http := getMap(p, "http-opts"); http != nil {
			obj.Path = getString(http, "path")
			obj.Host = getString(http, "host")
		}
	}
	return obj, nil
}

func clashSSToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 || getString(p, "password") == "" {
		return nil, fmt.Errorf("ss: missing server, port or password")
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(getString(p, "cipher") + ":" + getString(p, "password")))
	u := url.URL{
		Scheme:   "ss",
		User:     url.User(userinfo),
		Host:     net.JoinHostPort(server, strconv.Itoa(port)),
		Fragment: getString(p, "name"),
	}
	if plugin := getString(p, "plugin"); plugin != "" {
		v := plugin
		if opts := getString(p, "plugin-opts"); opts != "" {
			v += ";" + opts
		}
		u.RawQuery = "plugin=" + url.QueryEscape(v)
	}
	return serverObj.ParseSSURL(u.String())
}

func clashTrojanToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 || getString(p, "password") == "" {
		return nil, fmt.Errorf("trojan: missing server, port or password")
	}
	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(getString(p, "password")),
		Host:     net.JoinHostPort(server, strconv.Itoa(port)),
		Fragment: getString(p, "name"),
	}
	q := url.Values{}
	if sni := getString(p, "sni"); sni != "" {
		q.Set("sni", sni)
	}
	if alpn := getStringList(p, "alpn"); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	network := getString(p, "network")
	switch network {
	case "ws":
		q.Set("type", "ws")
		if ws := getMap(p, "ws-opts"); ws != nil {
			q.Set("path", getString(ws, "path"))
			if headers := getMap(ws, "headers"); headers != nil {
				q.Set("host", getString(headers, "Host"))
			}
		}
	case "grpc":
		q.Set("type", "grpc")
		if grpc := getMap(p, "grpc-opts"); grpc != nil {
			q.Set("serviceName", getString(grpc, "grpc-service-name"))
		}
	}
	if encoded := q.Encode(); encoded != "" {
		u.RawQuery = encoded
	}
	return serverObj.ParseTrojanURL(u.String())
}

func clashHysteria2ToServerObj(p map[string]interface{}) (serverObj.ServerObj, error) {
	server := getString(p, "server")
	port := getInt(p, "port")
	if server == "" || port <= 0 || getString(p, "password") == "" {
		return nil, fmt.Errorf("hysteria2: missing server, port or password")
	}
	u := url.URL{
		Scheme:   "hysteria2",
		User:     url.User(getString(p, "password")),
		Host:     net.JoinHostPort(server, strconv.Itoa(port)),
		Fragment: getString(p, "name"),
	}
	q := url.Values{}
	if sni := getString(p, "sni"); sni != "" {
		q.Set("sni", sni)
	}
	if obfs := getString(p, "obfs"); obfs != "" {
		q.Set("obfs", obfs)
	}
	if obfsPwd := getString(p, "obfs-password"); obfsPwd != "" {
		q.Set("obfs-password", obfsPwd)
	}
	if encoded := q.Encode(); encoded != "" {
		u.RawQuery = encoded
	}
	return serverObj.ParseHysteria2URL(u.String())
}
