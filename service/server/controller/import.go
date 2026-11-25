package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

func PostImportLink(ctx *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.ResponseError(ctx, logError("invalid import link request"))
		return
	}
	link, err := service.ParseImportLink(req.URL)
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, link)
}

func PostImport(ctx *gin.Context) {
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

	var req service.ImportRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.ResponseError(ctx, logError("invalid import request"))
		return
	}
	result, err := service.ImportProfile(req)
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	data := touchData()
	data["importResult"] = result
	common.ResponseSuccess(ctx, data)
}
