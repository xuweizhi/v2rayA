package controller

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/RoutingA"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/server/service"
)

func isReservedCustomInboundTag(tag string) bool {
	lower := strings.ToLower(strings.TrimSpace(tag))
	if strings.HasPrefix(lower, "v2raya-") {
		return true
	}
	switch lower {
	case "socks", "http", "rule-socks", "rule-http", "vmess",
		"transparent", "transparent-socks", "api-in", "dns-in":
		return true
	}
	return false
}

func checkCustomInboundPortCollision(port int) error {
	if port == 52345 || port == 52306 {
		return fmt.Errorf("port %d is reserved for transparent proxy", port)
	}
	if _, dnsPort, err := net.SplitHostPort(configure.GetSettingNotNil().DnsListenAddr); err == nil && dnsPort == strconv.Itoa(port) {
		return fmt.Errorf("port %d is used by the DNS listener", port)
	}
	ports := configure.GetPortsNotNil()
	reserved := []struct {
		name string
		port int
	}{
		{"socks5", ports.Socks5},
		{"http", ports.Http},
		{"socks5WithPac", ports.Socks5WithPac},
		{"httpWithPac", ports.HttpWithPac},
		{"vmess", ports.Vmess},
		{"api", ports.Api.Port},
	}
	for _, r := range reserved {
		if r.port > 0 && r.port == port {
			return fmt.Errorf("port %d is already used by %s inbound", port, r.name)
		}
	}
	return nil
}

func GetCustomInbound(ctx *gin.Context) {
	inbounds := configure.GetCustomInbounds()
	common.ResponseSuccess(ctx, gin.H{"inbounds": inbounds})
}

func PostCustomInbound(ctx *gin.Context) {
	updatingMu.Lock()
	if updating {
		common.ResponseError(ctx, processingErr)
		updatingMu.Unlock()
		return
	}
	updating = true
	updatingMu.Unlock()
	defer func() {
		updatingMu.Lock()
		updating = false
		updatingMu.Unlock()
	}()

	var ci configure.CustomInbound
	if err := ctx.ShouldBindJSON(&ci); err != nil {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if ci.Protocol != "socks" && ci.Protocol != "http" {
		common.ResponseError(ctx, logError(fmt.Errorf("protocol must be socks or http")))
		return
	}
	if ci.Port <= 0 || ci.Port > 65535 {
		common.ResponseError(ctx, logError(fmt.Errorf("invalid port")))
		return
	}
	ci.Tag = strings.TrimSpace(ci.Tag)
	if ci.Tag == "" {
		common.ResponseError(ctx, logError(fmt.Errorf("tag is required")))
		return
	}
	if isReservedCustomInboundTag(ci.Tag) {
		common.ResponseError(ctx, logError(fmt.Errorf("tag '%s' is reserved", ci.Tag)))
		return
	}

	// Validate outbound binding
	if ci.Outbound == "" {
		common.ResponseError(ctx, logError(fmt.Errorf("outbound group is required")))
		return
	}
	if ci.OutboundType != "direct" && ci.OutboundType != "routingA" {
		common.ResponseError(ctx, logError(fmt.Errorf("outboundType must be 'direct' or 'routingA'")))
		return
	}

	// If outboundType is "routingA", validate the RoutingA rules
	if ci.OutboundType == "routingA" {
		if ci.RoutingARules == "" {
			common.ResponseError(ctx, logError(fmt.Errorf("routingA rules are required when outboundType is 'routingA'")))
			return
		}
		// Parse and validate RoutingA rules
		lines := strings.Split(ci.RoutingARules, "\n")
		hardcodeReplacement := regexp.MustCompile(`\$\$.+?\$\$`)
		for i := range lines {
			hardcodes := hardcodeReplacement.FindAllString(lines[i], -1)
			for _, hardcode := range hardcodes {
				lines[i] = strings.Replace(lines[i], hardcode, "", 1)
			}
		}
		_, err := RoutingA.Parse(strings.Join(lines, "\n"))
		if err != nil {
			common.ResponseError(ctx, logError(fmt.Errorf("invalid RoutingA rules: %w", err)))
			return
		}
	}

	var updated []configure.CustomInbound
	if err := service.WithMembershipLock(func() error {
		// Verify the outbound group exists
		outboundExists := false
		for _, ob := range configure.GetOutbounds() {
			if ob == ci.Outbound {
				outboundExists = true
				break
			}
		}
		if !outboundExists {
			return fmt.Errorf("outbound group '%s' does not exist", ci.Outbound)
		}
		if err := checkCustomInboundPortCollision(ci.Port); err != nil {
			return err
		}
		previous := configure.GetCustomInbounds()
		for _, existing := range previous {
			if existing.Tag == ci.Tag {
				return fmt.Errorf("tag '%s' already exists", ci.Tag)
			}
			if existing.Port == ci.Port {
				return fmt.Errorf("port %d is already in use by '%s'", ci.Port, existing.Tag)
			}
		}
		updated = append(append([]configure.CustomInbound(nil), previous...), ci)
		if err := configure.SetCustomInbounds(updated); err != nil {
			return err
		}
		if v2ray.ProcessManager.Running() {
			if err := v2ray.UpdateV2RayConfig(); err != nil {
				_ = configure.SetCustomInbounds(previous)
				if !v2ray.ProcessManager.Running() {
					if restoreErr := v2ray.UpdateV2RayConfig(); restoreErr != nil {
						return fmt.Errorf("%w; restoring previous core failed: %v", err, restoreErr)
					}
				}
				return err
			}
		}
		return nil
	}); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{"inbounds": updated})
}

func DeleteCustomInbound(ctx *gin.Context) {
	updatingMu.Lock()
	if updating {
		common.ResponseError(ctx, processingErr)
		updatingMu.Unlock()
		return
	}
	updating = true
	updatingMu.Unlock()
	defer func() {
		updatingMu.Lock()
		updating = false
		updatingMu.Unlock()
	}()

	var req struct {
		Tag string `json:"tag"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil || req.Tag == "" {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	var newList []configure.CustomInbound
	if err := service.WithMembershipLock(func() error {
		previous := configure.GetCustomInbounds()
		newList = make([]configure.CustomInbound, 0, len(previous))
		found := false
		for _, ci := range previous {
			if ci.Tag == req.Tag {
				found = true
				continue
			}
			newList = append(newList, ci)
		}
		if !found {
			return fmt.Errorf("tag '%s' not found", req.Tag)
		}
		if err := configure.SetCustomInbounds(newList); err != nil {
			return err
		}
		if v2ray.ProcessManager.Running() {
			if err := v2ray.UpdateV2RayConfig(); err != nil {
				_ = configure.SetCustomInbounds(previous)
				if !v2ray.ProcessManager.Running() {
					if restoreErr := v2ray.UpdateV2RayConfig(); restoreErr != nil {
						return fmt.Errorf("%w; restoring previous core failed: %v", err, restoreErr)
					}
				}
				return err
			}
		}
		return nil
	}); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{"inbounds": newList})
}
