package reqCache

import (
	"crypto/sha256"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
	"sync"
)

type reqGlen struct {
	reqIdMu map[string]*requestLock
	reqMu   sync.Mutex
}

type requestLock struct {
	mu    sync.Mutex
	users int
}

func newReqGlen() *reqGlen {
	return &reqGlen{
		reqIdMu: make(map[string]*requestLock),
		reqMu:   sync.Mutex{},
	}
}

var (
	glen = newReqGlen()
)

func ReqCache(ctx *gin.Context) {
	reqId := ctx.GetHeader(common.RequestIdHeader)
	if reqId == "" {
		return
	}
	// Scope retries to the same credential, method and URL. Never let a cached
	// success substitute for another request's authentication failure.
	reqId = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%q", []string{reqId, ctx.GetHeader("Authorization"), ctx.Query("Authorization"), ctx.Request.Method, ctx.Request.URL.RequestURI()}))))
	ctx.Set(common.RequestCacheContextKey, reqId)
	glen.reqMu.Lock()
	if resp := common.RespCache.Get(reqId); resp != nil {
		glen.reqMu.Unlock()
		resp := resp.(common.Resp)
		ctx.AbortWithStatusJSON(resp.Status, resp.Body)
		return
	}
	lock := glen.reqIdMu[reqId]
	if lock == nil {
		lock = new(requestLock)
		glen.reqIdMu[reqId] = lock
	}
	lock.users++
	glen.reqMu.Unlock()
	lock.mu.Lock()
	defer func() {
		lock.mu.Unlock()
		glen.reqMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(glen.reqIdMu, reqId)
		}
		glen.reqMu.Unlock()
	}()
	if resp := common.RespCache.Get(reqId); resp != nil {
		resp := resp.(common.Resp)
		ctx.AbortWithStatusJSON(resp.Status, resp.Body)
		return
	}
	ctx.Next()
}
