package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// defaultRouteFingerprint returns a fingerprint of the current default IPv4
// route (interface + gateway) on Linux, or "" when no default route exists.
func defaultRouteFingerprint() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return "" // header
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 11 {
			continue
		}
		if fields[1] == "00000000" { // Destination == 0.0.0.0
			return fields[0] + "/" + fields[2] // interface/gateway
		}
	}
	return ""
}

// watchNetworkChanges periodically compares the default route fingerprint and
// restarts the core when the network environment changed, so the observatory
// re-probes and the balancer picks the best node for the new network.
func watchNetworkChanges() {
	go func() {
		last := defaultRouteFingerprint()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			cur := defaultRouteFingerprint()
			if cur == "" {
				continue
			}
			if last == "" {
				last = cur
				continue
			}
			if cur != last {
				log.Info("network change detected: default route %v -> %v", last, cur)
				last = cur
				if !v2ray.ProcessManager.Running() {
					continue
				}
				if err := v2ray.UpdateV2RayConfig(); err != nil {
					log.Warn("failed to reload core after network change: %v", err)
				}
			}
		}
	}()
}
