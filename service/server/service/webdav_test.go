package service

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWebdavClientRetriesTransportFailuresOnNextRoute(t *testing.T) {
	requests := 0
	successfulClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "alice" || password != "secret" {
			t.Fatalf("unexpected basic auth: user=%q password=%q ok=%v", user, password, ok)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&emptyReader{}),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	failedClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("first route unavailable")
	})}
	client := webdavClient{
		baseURL:  "https://dav.example.test",
		username: "alice",
		password: "secret",
		clients:  []*http.Client{failedClient, successfulClient},
	}

	resp, err := client.do(http.MethodGet, "backup.db", nil, nil)
	if err != nil {
		t.Fatalf("do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if requests != 1 {
		t.Fatalf("successful fallback requests = %d, want 1", requests)
	}
}

func TestWebdavClientDoesNotRetryHTTPResponses(t *testing.T) {
	firstRequests := 0
	secondRequests := 0
	deniedClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		firstRequests++
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(&emptyReader{}),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	secondClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		secondRequests++
		return nil, errors.New("second route should not be used")
	})}
	client := webdavClient{
		baseURL: "https://dav.example.test",
		clients: []*http.Client{deniedClient, secondClient},
	}

	resp, err := client.do(http.MethodGet, "backup.db", nil, nil)
	if err != nil {
		t.Fatalf("do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if firstRequests != 1 || secondRequests != 0 {
		t.Fatalf("requests = first:%d second:%d, want first:1 second:0", firstRequests, secondRequests)
	}
}

type emptyReader struct{}

func (*emptyReader) Read(_ []byte) (int, error) { return 0, io.EOF }
