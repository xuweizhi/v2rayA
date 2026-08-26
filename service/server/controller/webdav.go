package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// GetWebdavBackups lists the remote backup files.
func GetWebdavBackups(ctx *gin.Context) {
	items, err := service.ListWebdavBackups()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"webdavBackups": items,
	})
}

// PostWebdavBackup creates a local backup and uploads it.
func PostWebdavBackup(ctx *gin.Context) {
	item, err := service.UploadLatestBackup()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, gin.H{
		"backup": item,
	})
}

// GetWebdavBackupDownload streams a remote backup file.
func GetWebdavBackupDownload(ctx *gin.Context) {
	name := ctx.Query("filename")
	ctx.Header("Content-Disposition", "attachment; filename="+name)
	if err := service.OpenWebdavBackup(name, ctx.Writer); err != nil {
		// Headers are already sent at this point; log and stop.
		logError(err)
	}
}

// PostBackupRestore replaces the database with a backup and restarts the
// service.
func PostBackupRestore(ctx *gin.Context) {
	var body struct {
		Source string `json:"source"` // "local" or "webdav"
		Name   string `json:"name"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	common.ResponseSuccess(ctx, nil)
	// Restart happens asynchronously so the response reaches the client.
	go func() {
		if err := service.RestoreFromBackup(body.Source, body.Name); err != nil {
			_ = err
		}
	}()
}
