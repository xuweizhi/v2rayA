package controller

import (
	"mime"
	"net/http"

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
	name := ctx.Query("filename")
	file, err := service.OpenBackup(name)
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(ctx.Writer, ctx.Request, name, info.ModTime(), file)
}
