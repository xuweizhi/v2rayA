package reqCache

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
)

func TestConcurrentRetriesAreScopedAndLocksReclaimed(t *testing.T) {
	r := gin.New()
	r.Use(ReqCache)
	var calls atomic.Int32
	r.GET("/:target", func(c *gin.Context) { calls.Add(1); common.ResponseSuccess(c, c.GetHeader("Authorization")) })
	request := func(id, token, target string) {
		t.Helper()
		req := httptest.NewRequest("GET", "/"+target, nil)
		req.Header.Set(common.RequestIdHeader, id)
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Errorf("status %d", res.Code)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); request("same", "user-a", "one") }()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate retries executed %d times", calls.Load())
	}
	request("same", "user-b", "one")
	request("same", "user-a", "two")
	if calls.Load() != 3 {
		t.Fatal("cache reused across credentials or URLs")
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); request(fmt.Sprint(i), "user-a", "one") }(i)
	}
	wg.Wait()
	glen.reqMu.Lock()
	defer glen.reqMu.Unlock()
	if len(glen.reqIdMu) != 0 {
		t.Fatalf("retained %d request locks", len(glen.reqIdMu))
	}
}
