package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/server/service"
)

// GetNotices returns the stored notices with the read state.
func GetNotices(ctx *gin.Context) {
	notices := service.GetNotices()
	read := make(map[string]bool)
	for _, id := range service.GetReadNotices() {
		read[id] = true
	}
	unread := 0
	for _, n := range notices {
		if !read[n.ID] {
			unread++
		}
	}
	common.ResponseSuccess(ctx, gin.H{
		"notices": notices,
		"unread":  unread,
	})
}

// PostNoticesRead marks notices as read.
func PostNoticesRead(ctx *gin.Context) {
	var body struct {
		Ids []string `json:"ids"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if err := service.MarkNoticesRead(body.Ids); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, nil)
}
