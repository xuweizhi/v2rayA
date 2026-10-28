package jwt

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/v2rayA/v2rayA/common"
)

func signingTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec("CREATE TABLE system_config (key TEXT PRIMARY KEY,value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSigningKeyIsRandomAndPersistent(t *testing.T) {
	database := signingTestDB(t)
	a, err := loadSigningSecret(database)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadSigningSecret(database)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadSigningSecret(signingTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 || !bytes.Equal(a, b) || bytes.Equal(a, c) {
		t.Fatal("signing key lacks persistence or per-installation randomness")
	}
}

func TestAuthRejectsAddressDerivedTokenAndCachedSuccess(t *testing.T) {
	key, err := loadSigningSecret(signingTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	secret = key
	once = sync.Once{}
	once.Do(func() {})
	t.Cleanup(func() { secret = nil; once = sync.Once{} })
	r := gin.New()
	r.Use(JWTAuth(false))
	r.GET("/private", func(c *gin.Context) { common.ResponseSuccess(c, gin.H{"name": c.GetString("Name")}) })
	known := sha256.Sum256([]byte("node.example"))
	forged, _ := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.MapClaims{"uname": "admin", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(known[:])
	common.RespCache.Insert("reused-id", common.Resp{Status: 200, Body: gin.H{"code": "SUCCESS"}})
	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", forged)
	req.Header.Set(common.RequestIdHeader, "reused-id")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized request got cached success: %d %s", w.Code, w.Body)
	}
	duration := time.Hour
	token, err := MakeJWT(map[string]string{"uname": "admin"}, &duration)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"admin"`)) {
		t.Fatalf("valid token lost identity: %d %s", w.Code, w.Body)
	}
}
