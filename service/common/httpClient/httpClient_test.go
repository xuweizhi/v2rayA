package httpClient

import (
	"net/http"
	"testing"
)

func TestNewDirectHTTPClientDisablesEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example.test:8080")
	t.Setenv("HTTPS_PROXY", "http://proxy.example.test:8080")

	client := newDirectHTTPClient(false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("direct client must not use a proxy function")
	}
}
