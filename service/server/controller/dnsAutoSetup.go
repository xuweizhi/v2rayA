package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// PostDnsAutoSetup measures the built-in DNS servers and persists an
// optimized DNS rule set.
func PostDnsAutoSetup(ctx *gin.Context) {
	result, err := service.DnsAutoSetup()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"dnsAutoSetup": result,
	})
}
