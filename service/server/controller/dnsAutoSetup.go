package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
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

	var result service.DnsAutoSetupResult
	err := service.WithMembershipLock(func() error {
		var err error
		result, err = service.DnsAutoSetup(ctx.Query("outbound"))
		if err != nil || ctx.Query("preview") == "true" {
			return err
		}
		return applyDNSRules(result.Rules)
	})
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"dnsAutoSetup": result,
	})
}
