package gopeed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadRangesAndStreams(t *testing.T) {
	for _, content := range []string{"", "a", "ab", strings.Repeat("payload", 1000)} {
		for _, ranges := range []bool{false, true} {
			if ranges && content == "" {
				continue
			}
			t.Run(fmt.Sprintf("size=%d/range=%v", len(content), ranges), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if ranges {
						http.ServeContent(w, r, "data", time.Time{}, strings.NewReader(content))
					} else {
						w.(http.Flusher).Flush()
						fmt.Fprint(w, content)
					}
				}))
				defer server.Close()
				to := filepath.Join(t.TempDir(), "result")
				if err := Down(&Request{URL: server.URL}, to); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(to)
				if err != nil || string(got) != content {
					t.Fatalf("download mismatch: %v", err)
				}
			})
		}
	}
}

func TestFailedChunkPreservesDestination(t *testing.T) {
	for _, failure := range []string{"status", "ignored-range", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					w.Header().Set("Content-Range", "bytes 0-0/8")
					w.WriteHeader(http.StatusPartialContent)
					fmt.Fprint(w, "a")
					return
				}
				switch failure {
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "ignored-range":
					fmt.Fprint(w, "abcdefgh")
				case "truncated":
					w.Header().Set("Content-Range", strings.Replace(r.Header.Get("Range"), "=", " ", 1)+"/8")
					w.WriteHeader(http.StatusPartialContent)
					fmt.Fprint(w, "a")
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			to := filepath.Join(dir, "result")
			if err := os.WriteFile(to, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Down(&Request{URL: server.URL}, to); err == nil {
				t.Fatal("download should fail")
			}
			got, _ := os.ReadFile(to)
			files, _ := os.ReadDir(dir)
			if string(got) != "original" || len(files) != 1 {
				t.Fatal("failed download modified destination or leaked temporary file")
			}
		})
	}
}
