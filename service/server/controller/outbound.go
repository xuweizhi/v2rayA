package controller

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"

	"github.com/v2rayA/v2rayA/server/service"
)

func GetOutbounds(ctx *gin.Context) {
	outbounds := configure.GetOutbounds()
	common.ResponseSuccess(ctx, gin.H{
		"outbounds":      outbounds,
		"activeOutbound": service.ActiveOutbound(),
	})
}

func PostOutbound(ctx *gin.Context) {
	var data struct {
		Outbound string `json:"outbound"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil || data.Outbound == "" {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if err := configure.AddOutbound(data.Outbound); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	GetOutbounds(ctx)
}

func GetOutbound(ctx *gin.Context) {
	sources, err := service.SubscriptionSources()
	if err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	setting := configure.GetOutboundSetting(ctx.Query("outbound"))
	common.ResponseSuccess(ctx, gin.H{
		"setting":             setting,
		"subscriptionSources": sources,
	})
}

func PutOutbound(ctx *gin.Context) {
	updatingMu.Lock()
	if updating {
		common.ResponseError(ctx, processingErr)
		updatingMu.Unlock()
		return
	}
	updating = true
	updatingMu.Unlock()
	defer func() { updatingMu.Lock(); updating = false; updatingMu.Unlock() }()

	var data struct {
		Outbound string                    `json:"outbound"`
		Setting  configure.OutboundSetting `json:"setting"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil || data.Outbound == "" {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if err := service.ApplyOutboundSetting(data.Outbound, data.Setting); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	common.ResponseSuccess(ctx, nil)
}

func PutActiveOutbound(ctx *gin.Context) {
	updatingMu.Lock()
	if updating {
		updatingMu.Unlock()
		common.ResponseError(ctx, processingErr)
		return
	}
	updating = true
	updatingMu.Unlock()
	defer func() { updatingMu.Lock(); updating = false; updatingMu.Unlock() }()

	var data struct {
		Outbound string `json:"outbound" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if err := service.SwitchActiveOutbound(data.Outbound); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	GetOutbounds(ctx)
}

func PostOutboundMemberPreview(ctx *gin.Context) {
	var data struct {
		Outbound string                       `json:"outbound"`
		Rule     configure.OutboundMemberRule `json:"memberRule"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil {
		common.ResponseError(ctx, err)
		return
	}
	preview, err := service.PreviewOutboundMembers(data.Outbound, data.Rule)
	if err != nil {
		common.ResponseError(ctx, err)
		return
	}
	common.ResponseSuccess(ctx, preview)
}

func DeleteOutbound(ctx *gin.Context) {
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

	var data struct {
		Outbound string `json:"outbound"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil || data.Outbound == "" {
		common.ResponseError(ctx, logError("bad request"))
		return
	}
	if data.Outbound == "proxy" {
		common.ResponseError(ctx, logError("outbound \"proxy\" cannot be deleted"))
		return
	}

	if err := configure.CheckOutboundDependencies(data.Outbound); err != nil {
		common.ResponseError(ctx, err)
		return
	}
	// Check if any custom inbound is bound to this outbound group
	boundInbounds := configure.GetCustomInboundsByOutbound(data.Outbound)
	if len(boundInbounds) > 0 {
		names := make([]string, len(boundInbounds))
		for i, ci := range boundInbounds {
			names[i] = fmt.Sprintf("%s (port %d, %s)", ci.Tag, ci.Port, ci.Protocol)
		}
		common.ResponseError(ctx, logError(fmt.Errorf(
			"cannot delete outbound group '%s': the following custom inbounds are bound to it:\n%s\nPlease unbind them first",
			data.Outbound,
			strings.Join(names, "\n"),
		)))
		return
	}

	if err := service.DeleteOutbound(data.Outbound); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	GetOutbounds(ctx)
}

func PutOutboundConnections(ctx *gin.Context) {
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

	var data struct {
		Outbound string `json:"outbound"`
		Touches  []struct {
			ID        int    `json:"id"`
			TYPE      string `json:"_type"`
			TYPEAlias string `json:"type"`
			Sub       *int   `json:"sub"`
			Outbound  string `json:"outbound"`
		} `json:"touches"`
	}
	if err := ctx.ShouldBindJSON(&data); err != nil {
		common.ResponseError(ctx, logError("bad request"))
		return
	}

	normalizeTouchType := func(raw string) (configure.TouchType, bool) {
		normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(raw, "_", ""), "-", ""))
		switch normalized {
		case "server":
			return configure.ServerType, true
		case "subscriptionserver":
			return configure.SubscriptionServerType, true
		default:
			return "", false
		}
	}

	whiches := make([]configure.Which, 0, len(data.Touches))
	for i, w := range data.Touches {
		rawType := w.TYPE
		if rawType == "" {
			rawType = w.TYPEAlias
		}
		typ, ok := normalizeTouchType(rawType)
		if !ok {
			common.ResponseError(ctx, logError(fmt.Errorf("bad request: invalid touch type at index %d: %q", i, rawType)))
			return
		}
		if w.ID <= 0 {
			common.ResponseError(ctx, logError(fmt.Errorf("bad request: invalid touch id at index %d: %d", i, w.ID)))
			return
		}
		sub := 0
		if w.Sub != nil {
			sub = *w.Sub
		}
		if typ == configure.SubscriptionServerType && sub < 0 {
			common.ResponseError(ctx, logError(fmt.Errorf("bad request: invalid sub index at index %d: %d", i, sub)))
			return
		}
		if typ == configure.ServerType {
			sub = 0
		}
		whiches = append(whiches, configure.Which{
			TYPE:     typ,
			ID:       w.ID,
			Sub:      sub,
			Outbound: data.Outbound,
		})
	}

	if err := service.ReplaceOutboundConnections(data.Outbound, whiches); err != nil {
		common.ResponseError(ctx, logError(err))
		return
	}
	getTouch(ctx)
}
