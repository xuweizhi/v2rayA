package controller

import (
	"bufio"
	"io"
	"mime"
	"net/http"
	"runtime"
	"strconv"

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
	resp, err := service.OpenWebdavBackupStream(name)
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"code": common.FAIL, "message": logError(err).Error()})
		return
	}
	defer resp.Body.Close()
	body := bufio.NewReader(resp.Body)
	if _, err := body.Peek(1); err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"code": common.FAIL, "message": logError(err).Error()})
		return
	}
	if resp.ContentLength >= 0 {
		ctx.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	ctx.Header("Content-Type", "application/octet-stream")
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": name}); cd != "" {
		ctx.Header("Content-Disposition", cd)
	}
	ctx.Status(http.StatusOK)
	if _, err := io.Copy(ctx.Writer, body); err != nil {
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
	if err := service.RestoreFromBackup(body.Source, body.Name); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	// RestoreFromBackup schedules re-exec only after staging has succeeded.
	common.ResponseSuccess(ctx, gin.H{"restartRequired": runtime.GOOS == "windows"})
}
