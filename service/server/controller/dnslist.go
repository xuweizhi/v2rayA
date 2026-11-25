package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/server/service"
)

// DnsConfigResponse 是新 DNS 配置的 API 响应结构。
type DnsConfigResponse struct {
	UseNewModule bool                `json:"useNewModule"`
	Listener     string              `json:"listener"`
	Rules        []configure.DnsRule `json:"rules"`
}

// PutDnsRules 处理 PUT /api/dns 请求，保存 DNS 规则配置。
// 支持新格式和旧格式请求，向后兼容。
func PutDnsRules(ctx *gin.Context) {
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

	var rules []configure.DnsRule
	if err := ctx.ShouldBindJSON(&rules); err != nil {
		common.ResponseError(ctx, logError(fmt.Errorf("bad request: %w", err)))
		return
	}
	if len(rules) == 0 {
		common.ResponseError(ctx, fmt.Errorf("at least one DNS rule is required"))
		return
	}
	for i, rule := range rules {
		// 检查上游地址：优先使用 Upstream 字段，回退到 Server
		upstream := rule.Upstream
		if upstream == "" {
			upstream = rule.Server
		}
		if strings.TrimSpace(upstream) == "" && rule.Action != "reject" && rule.Outbound != "block" {
			common.ResponseError(ctx, logError(fmt.Errorf("rule[%d]: server/upstream cannot be empty", i)))
			return
		}
		// 同步新旧字段：确保 Server 和 Upstream 至少有一个有值
		if rule.Server == "" && rule.Upstream != "" {
			rule.Server = rule.Upstream
		}
		if rule.Upstream == "" && rule.Server != "" {
			rule.Upstream = rule.Server
		}
		// 同步 Domain 和 Domains
		if rule.Domains == "" && rule.Domain != "" {
			rule.Domains = rule.Domain
		}
		if rule.Domain == "" && rule.Domains != "" {
			rule.Domain = rule.Domains
		}
		if rule.Outbound == "" {
			rule.Outbound = "direct"
		}
		rules[i] = rule
	}

	// 执行迁移以确保新字段有默认值
	migrated := configure.MigrateDnsRules(rules)

	if err := service.WithMembershipLock(func() error { return applyDNSRules(migrated) }); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, nil)
}

// The caller holds the membership lock during persistence and core reload.
func applyDNSRules(rules []configure.DnsRule) error {
	old := configure.GetDnsRulesNotNil()
	if err := configure.SetDnsRules(rules); err != nil {
		return err
	}
	if v2ray.ProcessManager.Running() {
		if err := v2ray.UpdateV2RayConfig(); err != nil {
			if restoreErr := configure.SetDnsRules(old); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
			if !v2ray.ProcessManager.Running() {
				return errors.Join(err, v2ray.UpdateV2RayConfig())
			}
			return err
		}
	}
	return nil
}

// GetDnsRules 处理 GET /api/dns 请求，返回 DNS 规则配置和状态。
// 返回新格式配置，同时保持向后兼容。
func GetDnsRules(ctx *gin.Context) {
	rules := configure.GetDnsRulesNotNil()
	migrated := configure.MigrateDnsRules(rules)

	// 获取当前设置以返回监听地址等信息
	setting := service.GetSetting()

	listener := ""
	if setting != nil {
		listener = setting.DNSListenAddress()
	}

	common.ResponseSuccess(ctx, DnsConfigResponse{
		UseNewModule: true,
		Listener:     listener,
		Rules:        migrated,
	})
}
