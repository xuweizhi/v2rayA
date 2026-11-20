package httpClient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestFailedSelectedClientDoesNotRetryPublicURL(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	routeErr := errors.New("selected route unavailable")
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, routeErr
	}}}
	resp, err := HttpGetUsingSpecificClientWithUAAndHeaders(client, server.URL, "v2raya-test", nil)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, routeErr) || calls.Load() != 0 {
		t.Fatalf("route error = %v, direct requests = %d", err, calls.Load())
	}
}
