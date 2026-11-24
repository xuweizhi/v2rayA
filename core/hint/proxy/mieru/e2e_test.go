package mieru

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/enfein/mieru/v3/apis/model"
)

// TestE2E dials a real mieru server and fetches a page through it.
// It only runs when V2RAYA_MIERU_E2E=1 is set, since it depends on a
// third-party server being online.
func TestE2E(t *testing.T) {
	if os.Getenv("V2RAYA_MIERU_E2E") != "1" {
		t.Skip("set V2RAYA_MIERU_E2E=1 to run the mieru e2e test")
	}
	server := os.Getenv("V2RAYA_MIERU_SERVER")
	username := os.Getenv("V2RAYA_MIERU_USERNAME")
	password := os.Getenv("V2RAYA_MIERU_PASSWORD")
	if server == "" || username == "" || password == "" {
		t.Fatal("set V2RAYA_MIERU_SERVER, V2RAYA_MIERU_USERNAME and V2RAYA_MIERU_PASSWORD for the live test")
	}

	c, err := NewClient(t.Context(), &ClientConfig{Address: server, Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sdk, err := c.start(&net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target := model.NetAddrSpec{Net: "tcp"}
		if err := target.AddrSpec.From(address); err != nil {
			return nil, err
		}
		return sdk.DialContext(ctx, target)
	}}
	defer tr.CloseIdleConnections()
	httpClient := http.Client{Transport: tr, Timeout: 15 * time.Second}
	resp, err := httpClient.Get("https://www.gstatic.com/generate_204")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
