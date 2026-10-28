package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRestoreReturnsPreparationFailure(t *testing.T) {
	router := gin.New()
	router.POST("/restore", PostBackupRestore)
	request := httptest.NewRequest("POST", "/restore", strings.NewReader(`{"source":"invalid","name":"backup.db"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"code":"FAIL"`) || !strings.Contains(response.Body.String(), "invalid backup source") {
		t.Fatalf("restore reported success before validation: %s", response.Body)
	}
}
