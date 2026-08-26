package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// GetDetectRule answers "which routing rule and outbound would a given
// domain/port/network hit under the currently running configuration".
func GetDetectRule(ctx *gin.Context) {
	result, err := service.DetectRule(
		ctx.Query("domain"),
		ctx.Query("port"),
		ctx.Query("network"),
		ctx.Query("inbound"),
	)
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"detectRule": result,
	})
}
