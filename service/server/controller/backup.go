package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// GetBackups lists the available backups.
func GetBackups(ctx *gin.Context) {
	items, err := service.ListBackups()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"backups": items,
	})
}

// PostBackup creates a backup snapshot now.
func PostBackup(ctx *gin.Context) {
	item, err := service.CreateBackup()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"backup": item,
	})
}

// GetBackupDownload serves a backup file by name.
func GetBackupDownload(ctx *gin.Context) {
	path, err := service.BackupPath(ctx.Query("filename"))
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	ctx.FileAttachment(path, ctx.Query("filename"))
}
