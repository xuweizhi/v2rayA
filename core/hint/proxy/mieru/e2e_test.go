package mieru

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	xray_net "github.com/xtls/xray-core/common/net"
)

// TestE2E dials a real mieru server and fetches a page through it.
// It only runs when V2RAYA_MIERU_E2E=1 is set, since it depends on a
// third-party server being online.
func TestE2E(t *testing.T) {
	if os.Getenv("V2RAYA_MIERU_E2E") != "1" {
		t.Skip("set V2RAYA_MIERU_E2E=1 to run the mieru e2e test")
	}
	server := "91.243.81.186:2401"
	username := "4107543b-64de-1ee5-5081-2020537a1359"
	password := username

	d := net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.Dial("tcp", server)
	if err != nil {
		t.Skipf("server unreachable: %v", err)
	}
	defer raw.Close()

	mc, err := newMieruConn(raw, username, password)
	if err != nil {
		t.Fatalf("newMieruConn: %v", err)
	}
	target := xray_net.Destination{
		Network: xray_net.Network_TCP,
		Address: xray_net.ParseAddress("www.gstatic.com"),
		Port:    80,
	}
	if err := mc.openSession(t.Context(), target); err != nil {
		t.Fatalf("openSession: %v", err)
	}
	t.Log("session opened, fetching http://www.gstatic.com/generate_204")

	req := "GET /generate_204 HTTP/1.1\r\nHost: www.gstatic.com\r\nConnection: close\r\n\r\n"
	if _, err := mc.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = raw.SetReadDeadline(time.Now().Add(15 * time.Second))
	resp, err := io.ReadAll(mc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	t.Logf("response (%d bytes): %.200s", len(resp), resp)
	if len(resp) == 0 {
		t.Fatalf("empty response")
	}
	mc.closeSession()
}
