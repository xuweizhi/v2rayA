package jwt

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/common"
)

func TestJWTAuthSupportsTokenQueryParameter(t *testing.T) {
	key, err := loadSigningSecret(signingTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	secret = key
	once = sync.Once{}
	once.Do(func() {})
	t.Cleanup(func() { secret = nil; once = sync.Once{} })

	duration := time.Hour
	token, err := MakeJWT(map[string]string{"uname": "alice"}, &duration)
	if err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.Use(JWTAuth(false))
	r.GET("/private", func(c *gin.Context) { common.ResponseSuccess(c, gin.H{"name": c.GetString("Name")}) })

	for _, target := range []string{
		"/private?token=" + url.QueryEscape(token),
		"/private?token=" + url.QueryEscape("bearer "+token),
		"/private?Authorization=" + url.QueryEscape("Bearer "+token),
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"alice"`)) {
			t.Fatalf("GET %s failed: status=%d body=%s", target, w.Code, w.Body.String())
		}
	}
}
