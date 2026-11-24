package service

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

// Run inside an isolated network namespace because the core also binds local DNS.
func TestFailedLatencyStartupRestoresSOCKS(t *testing.T) {
	binary := os.Getenv("V2RAYA_NETNS_TEST_CORE")
	if binary == "" {
		t.Skip("set V2RAYA_NETNS_TEST_CORE inside a disposable network namespace")
	}
	resetReviewDB(t)
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	env := conf.GetEnvironmentConfig()
	old := *env
	t.Cleanup(func() { *env = old })
	env.V2rayBin = binary
	env.CoreStartupTimeout = 5
	env.Lite = true
	env.V2rayAssetsDirectory = os.Getenv("XRAY_LOCATION_ASSET")
	settings := configure.NewSetting()
	settings.DnsListenAddr = "127.0.0.1:52353"
	require(configure.SetSetting(settings))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(err)
	port := listener.Addr().(*net.TCPAddr).Port
	require(listener.Close())
	require(configure.SetPorts(&configure.Ports{Socks5: port}))
	node, err := serverObj.NewFromLink("vless", "vless://00000000-0000-0000-0000-000000000001@127.0.0.1:12345#loopback")
	require(err)
	require(configure.AppendServers([]*configure.ServerRaw{{ServerObj: node}}))
	which := configure.Which{TYPE: configure.ServerType, ID: 1, Outbound: "proxy"}
	require(configure.AddConnect(which))
	require(v2ray.UpdateV2RayConfig())
	defer v2ray.ProcessManager.Stop(true)
	checkSOCKS := func() {
		t.Helper()
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
		require(err)
		defer conn.Close()
		require(conn.SetDeadline(time.Now().Add(2 * time.Second)))
		_, err = conn.Write([]byte{5, 1, 0})
		require(err)
		reply := make([]byte, 2)
		_, err = io.ReadFull(conn, reply)
		require(err)
		if !bytes.Equal(reply, []byte{5, 0}) {
			t.Fatalf("SOCKS greeting: %v", reply)
		}
	}
	checkSOCKS()
	// Fail the probe's pre-start hook exactly once, after the old core stopped.
	env.CoreHook = filepath.Join(t.TempDir(), "hook")
	marker := filepath.Join(t.TempDir(), "fail-once")
	t.Setenv("V2RAYA_TEST_FAIL_ONCE", marker)
	require(os.WriteFile(marker, nil, 0600))
	require(os.WriteFile(env.CoreHook, []byte("#!/bin/sh\nif [ \"$1\" = --stage=pre-start ] && [ -f \"$V2RAYA_TEST_FAIL_ONCE\" ]; then\n rm \"$V2RAYA_TEST_FAIL_ONCE\"\n exit 42\nfi\n"), 0700))
	_, err = TestHttpLatency([]*configure.Which{&which}, time.Second, 1, false, "")
	if err == nil || !strings.Contains(err.Error(), "exit status 42") || strings.Contains(err.Error(), "restoring proxy") {
		t.Fatalf("expected only the injected failure: %v", err)
	}
	if !v2ray.ProcessManager.ServiceRunning() {
		t.Fatal("core did not resume normal service")
	}
	checkSOCKS()
}
