package v2ray

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/v2rayA/v2rayA/common/netTools/netstat"
	"github.com/v2rayA/v2rayA/common/netTools/ports"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

func (t *Template) checkDuplicatedTags() error {
	inboundTagsSet := make(map[string]interface{})
	for _, in := range t.Inbounds {
		tag := in.Tag
		if _, exists := inboundTagsSet[tag]; exists {
			return fmt.Errorf("duplicated inbound tag: %v", tag)
		} else {
			inboundTagsSet[tag] = nil
		}
	}
	outboundTagsSet := make(map[string]interface{})
	for _, out := range t.Outbounds {
		tag := out.Tag
		if _, exists := outboundTagsSet[tag]; exists {
			return fmt.Errorf("duplicated outbound tag: %v", tag)
		} else {
			outboundTagsSet[tag] = nil
		}
	}
	return nil
}

type dnsModuleListenerSpec struct {
	Listener struct {
		ListenAddr       string   `json:"listen_addr"`
		ExtraListenAddrs []string `json:"extra_listen_addrs"`
	} `json:"listener"`
}

func parseListenSocketsConflict(hostA, hostB string) bool {
	if hostA == "" {
		hostA = "0.0.0.0"
	}
	if hostB == "" {
		hostB = "0.0.0.0"
	}
	ipA := net.ParseIP(hostA)
	ipB := net.ParseIP(hostB)
	if ipA == nil || ipB == nil {
		return strings.EqualFold(hostA, hostB)
	}
	if ipA.Equal(ipB) {
		return true
	}
	isV4A := ipA.To4() != nil
	isV4B := ipB.To4() != nil
	isWildV4A := isV4A && ipA.IsUnspecified()
	isWildV4B := isV4B && ipB.IsUnspecified()
	isWildV6A := !isV4A && ipA.IsUnspecified()
	isWildV6B := !isV4B && ipB.IsUnspecified()

	// Dual-stack wildcard "::" conflicts with any IPv4 or IPv6 listener on the same port.
	if isWildV6A || isWildV6B {
		return true
	}
	// IPv4 wildcard "0.0.0.0" conflicts with any IPv4 listener on the same port.
	if (isWildV4A && isV4B) || (isWildV4B && isV4A) {
		return true
	}
	return false
}

func (t *Template) checkDuplicatedInboundSockets() error {
	byPort := make(map[int][]string)
	addSocket := func(host string, port int) error {
		if port <= 0 {
			return nil
		}
		if host == "" {
			// https://www.v2fly.org/config/inbounds.html#inboundobject
			host = "0.0.0.0"
		}
		for _, existing := range byPort[port] {
			if parseListenSocketsConflict(existing, host) {
				return fmt.Errorf("duplicated inbound listening address: %v (conflicts with %v)",
					net.JoinHostPort(host, strconv.Itoa(port)),
					net.JoinHostPort(existing, strconv.Itoa(port)))
			}
		}
		byPort[port] = append(byPort[port], host)
		return nil
	}

	for _, in := range t.Inbounds {
		if err := addSocket(in.Listen, in.Port); err != nil {
			return err
		}
	}

	if len(t.DnsModuleConfig) > 0 {
		var spec dnsModuleListenerSpec
		if err := json.Unmarshal(t.DnsModuleConfig, &spec); err == nil {
			var dnsAddrs []string
			if spec.Listener.ListenAddr != "" {
				dnsAddrs = append(dnsAddrs, spec.Listener.ListenAddr)
			}
			dnsAddrs = append(dnsAddrs, spec.Listener.ExtraListenAddrs...)
			for _, addr := range dnsAddrs {
				host, portStr, err := net.SplitHostPort(addr)
				if err != nil {
					continue
				}
				port, err := strconv.Atoi(portStr)
				if err != nil || port <= 0 {
					continue
				}
				if err := addSocket(host, port); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var OccupiedErr = fmt.Errorf("port is occupied")

func PortOccupied(syntax []string) (err error) {
	occupied, sockets, err := ports.IsPortOccupied(syntax)
	if err != nil {
		if errors.Is(err, netstat.ErrorNotSupportOSErr) {
			log.Trace("PortOccupied: %v", err)
			return nil
		}
		return
	}
	if occupied {
		if err = netstat.FillProcesses(sockets); err != nil {
			if errors.Is(err, netstat.ErrorNotSupportOSErr) {
				log.Warn("cannot judge port occupation: %v", err)
				return nil
			}
			return fmt.Errorf("failed to check if port is occupied: %w", err)
		}
		for _, s := range sockets {
			p := s.Proc
			if p == nil {
				continue
			}
			if ownPID := strconv.Itoa(os.Getpid()); p.PPID == ownPID ||
				p.PID == ownPID {
				continue
			}
			occupiedErr := fmt.Errorf("%w by %v(%v): %v", OccupiedErr, p.Name, p.PID, s.LocalAddress.Port)
			if configure.GetSettingNotNil().PortSharing {
				// want to listen 0.0.0.0, which conflicts with all IPs
				return occupiedErr
			}
			if s.LocalAddress.IP.IsUnspecified() {
				return occupiedErr
			}
			if s.LocalAddress.IP.IsLoopback() {
				return occupiedErr
			}
		}
	}
	return nil
}

func (t *Template) CheckInboundPortsOccupied() (err error) {
	var st []string
	for _, in := range t.Inbounds {
		switch strings.ToLower(in.Protocol) {
		case "http", "vmess", "vless", "trojan":
			st = append(st, strconv.Itoa(in.Port)+":tcp")
		case "dokodemo-door":
			if strings.HasPrefix(in.Tag, "dns-in") {
				// checked before
				continue
			} else if in.Settings != nil && in.Settings.Network != "" {
				st = append(st, strconv.Itoa(in.Port)+":"+in.Settings.Network)
			} else {
				st = append(st, strconv.Itoa(in.Port)+":tcp,udp")
			}
		default:
			st = append(st, strconv.Itoa(in.Port)+":tcp,udp")
		}
	}
	if len(t.DnsModuleConfig) > 0 {
		var spec dnsModuleListenerSpec
		if err := json.Unmarshal(t.DnsModuleConfig, &spec); err == nil && spec.Listener.ListenAddr != "" {
			if _, portStr, splitErr := net.SplitHostPort(spec.Listener.ListenAddr); splitErr == nil {
				if port, convErr := strconv.Atoi(portStr); convErr == nil && port > 0 {
					st = append(st, portStr+":tcp,udp")
				}
			}
		}
	}
	return PortOccupied(st)
}
