package controller

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/server/service"
)

// PostDnsAutoSetup measures the built-in DNS servers and persists an
// optimized DNS rule set.
func PostDnsAutoSetup(ctx *gin.Context) {
	updatingMu.Lock()
	if updating {
		common.ResponseError(ctx, processingErr)
		updatingMu.Unlock()
		return
	}
	updating = true
	updatingMu.Unlock()
	defer func() { updatingMu.Lock(); updating = false; updatingMu.Unlock() }()

	old := configure.GetDnsRulesNotNil()
	result, err := service.DnsAutoSetup(ctx.Query("outbound"))
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	if v2ray.ProcessManager.Running() {
		if err := v2ray.UpdateV2RayConfig(); err != nil {
			_ = configure.SetDnsRules(old)
			if !v2ray.ProcessManager.Running() {
				if restoreErr := v2ray.UpdateV2RayConfig(); restoreErr != nil {
					err = fmt.Errorf("%w; restoring previous core failed: %v", err, restoreErr)
				}
			}
			common.ResponseError(ctx, err)
			return
		}
	}
	common.ResponseSuccess(ctx, gin.H{
		"dnsAutoSetup": result,
	})
}
