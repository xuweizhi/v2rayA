package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// GetDiagnose runs the network self-check and returns a copyable report.
func GetDiagnose(ctx *gin.Context) {
	report := service.Diagnose(ctx.Query("domain"))
	common.ResponseSuccess(ctx, gin.H{
		"diagnose": report,
	})
}
