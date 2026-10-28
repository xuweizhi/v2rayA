package v2ray

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/mohae/deepcopy"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
)

func internalGroupTag(kind, group string) string {
	return "v2raya-" + kind + "-" + base64.RawURLEncoding.EncodeToString([]byte(group))
}

// Each chained group gets its own outbound objects. Nodes shared with another
// balancer must not inherit that group's transport dialer.
func (t *Template) applyPreProxies(sd *ServerData) error {
	if err := configure.ValidatePreProxies(sd.OutboundName2Setting); err != nil {
		return err
	}
	var groups []string
	for group, setting := range sd.OutboundName2Setting {
		if setting.PreProxy != "" {
			groups = append(groups, group)
		}
	}
	sort.Strings(groups)
	for _, group := range groups {
		pre := sd.OutboundName2Setting[group].PreProxy
		bridge := internalGroupTag("pre", group)
		routeTag := internalGroupTag("pre-in", group)
		config, _ := json.Marshal(map[string]string{"inboundTag": routeTag})
		t.Outbounds = append(t.Outbounds, coreObj.OutboundObject{Tag: bridge, Protocol: "loopback", Settings: coreObj.Settings{Inlined: config}})
		t.internalRoutes = append(t.internalRoutes, coreObj.RoutingRule{Type: "field", InboundTag: []string{routeTag}, OutboundTag: pre})
		for i, info := range sd.ServerInfos {
			if info.OutboundName != group {
				continue
			}
			if info.PluginPort != 0 {
				return fmt.Errorf("pre-proxy group %q: external plugin nodes are not supported", group)
			}
			index := -1
			for j := range t.Outbounds {
				if t.Outbounds[j].Tag == t.OutboundTags[i] {
					index = j
					break
				}
			}
			if index < 0 {
				return fmt.Errorf("missing node outbound for group %q", group)
			}
			original := &t.Outbounds[index]
			switch original.Protocol {
			case "vmess", "vless", "trojan", "shadowsocks", "shadowsocks2022", "socks", "http":
			default:
				return fmt.Errorf("pre-proxy group %q: node protocol %q does not support transport chaining", group, original.Protocol)
			}
			if original.ProxySettings != nil {
				return fmt.Errorf("pre-proxy group %q: node already uses a plugin chain", group)
			}
			target := original
			if len(original.Balancers) > 0 {
				clone := deepcopy.Copy(*original).(coreObj.OutboundObject)
				clone.Tag = internalGroupTag("node", group) + "-" + base64.RawURLEncoding.EncodeToString([]byte(original.Tag)) + "."
				clone.Balancers = []string{group}
				remaining := []string{}
				for _, name := range original.Balancers {
					if name != group {
						remaining = append(remaining, name)
					}
				}
				original.Balancers = remaining
				if len(remaining) == 0 {
					*original = clone
					target = original
				} else {
					t.Outbounds = append(t.Outbounds, clone)
					target = &t.Outbounds[len(t.Outbounds)-1]
				}
				t.OutboundTags[i] = clone.Tag
			}
			if target.StreamSettings == nil {
				target.StreamSettings = &coreObj.StreamSettings{}
			}
			if target.StreamSettings.Sockopt == nil {
				target.StreamSettings.Sockopt = &coreObj.Sockopt{}
			}
			target.StreamSettings.Sockopt.DialerProxy = bridge
			copyInfo := info
			t.serverInfoMap[target.Tag] = &copyInfo
		}
	}
	return nil
}

// Local-only SOCKS entry points force probes through the requested group,
// including its balancer and any pre-proxy. They are never shared on the LAN.
func (t *Template) addProbeInbounds(sd *ServerData) error {
	t.ProbePorts = map[string]int{}
	t.ProbePaths = map[string]string{}
	var groups []string
	for group := range sd.OutboundName2ServerObjs {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		t.probeReservations = append(t.probeReservations, listener)
		port := listener.Addr().(*net.TCPAddr).Port
		t.ProbePorts[group] = port
		path := []string{group}
		for pre := sd.OutboundName2Setting[group].PreProxy; pre != ""; pre = sd.OutboundName2Setting[pre].PreProxy {
			path = append(path, pre)
		}
		t.ProbePaths[group] = strings.Join(path, " via ")
		tag := internalGroupTag("probe", group)
		t.Inbounds = append(t.Inbounds, coreObj.Inbound{Tag: tag, Port: port, Listen: "127.0.0.1", Protocol: "socks", Settings: &coreObj.InboundSettings{Auth: "noauth"}})
		t.internalRoutes = append(t.internalRoutes, coreObj.RoutingRule{Type: "field", InboundTag: []string{tag}, OutboundTag: group})
		// The DNS module's internal dispatcher uses a separate tag, unaffected
		// by the IPv4/IPv6 suffixes added to real listening inbounds.
		t.internalRoutes = append(t.internalRoutes, coreObj.RoutingRule{Type: "field", InboundTag: []string{internalGroupTag("dns", group)}, OutboundTag: group})
	}
	return nil
}

func (t *Template) releaseProbeReservations() {
	for _, listener := range t.probeReservations {
		_ = listener.Close()
	}
	t.probeReservations = nil
}
