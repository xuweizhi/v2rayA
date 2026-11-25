package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/coreObj"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/kernel/touch"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
)

func TestHttpLatencyHandlesInvalidURLWithoutPanic(t *testing.T) {
	w := &configure.Which{}
	httpLatency(w, "1080", time.Second, "://bad-url")
	if w.Latency != "INVALID URL" {
		t.Fatalf("Latency = %q, want INVALID URL", w.Latency)
	}
}

func TestClashSSPluginOptsAndH2HttpSequences(t *testing.T) {
	raw := `proxies:
  - name: ss-map
    type: ss
    server: ss.example
    port: 8388
    cipher: aes-256-gcm
    password: secret
    plugin: v2ray-plugin
    plugin-opts:
      mode: websocket
      tls: true
      host: cdn.example
      path: /ws
  - name: ss-notls
    type: ss
    server: ss2.example
    port: 8388
    cipher: aes-256-gcm
    password: secret
    plugin: v2ray-plugin
    plugin-opts:
      mode: websocket
      tls: false
      host: plain.example
  - name: ss-str
    type: ss
    server: ss3.example
    port: 8388
    cipher: aes-256-gcm
    password: secret
    plugin: simple-obfs
    plugin-opts: obfs=http;obfs-host=obfs.example
  - name: vless-h2
    type: vless
    server: h2.example
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    network: h2
    h2-opts:
      host:
        - a.example
        - b.example
      path: /h2path
  - name: vmess-http
    type: vmess
    server: http.example
    port: 80
    uuid: 00000000-0000-0000-0000-000000000002
    network: http
    http-opts:
      path:
        - /first
        - /second
      headers:
        Host:
          - h1.example
          - h2.example
`
	var report ImportResult
	nodes, err := resolveClashYAML(raw, &report)
	if err != nil {
		t.Fatalf("resolveClashYAML error = %v", err)
	}
	if len(nodes) != 5 {
		t.Fatalf("len(nodes) = %d, want 5 (reasons: %v)", len(nodes), report.Reasons)
	}

	ssMap := nodes[0].(*serverObj.Shadowsocks)
	if ssMap.Plugin.Name != "v2ray-plugin" || ssMap.Plugin.Opts.Tls != "tls" || ssMap.Plugin.Opts.Obfs != "websocket" || ssMap.Plugin.Opts.Host != "cdn.example" || ssMap.Plugin.Opts.Path != "/ws" {
		t.Fatalf("unexpected ss-map plugin: %+v", ssMap.Plugin)
	}
	ssNoTLS := nodes[1].(*serverObj.Shadowsocks)
	if ssNoTLS.Plugin.Opts.Tls != "" || ssNoTLS.Plugin.Opts.Host != "plain.example" {
		t.Fatalf("unexpected ss-notls plugin: %+v", ssNoTLS.Plugin)
	}
	ssStr := nodes[2].(*serverObj.Shadowsocks)
	if ssStr.Plugin.Name != "simple-obfs" || ssStr.Plugin.Opts.Obfs != "http" || ssStr.Plugin.Opts.Host != "obfs.example" {
		t.Fatalf("unexpected ss-str plugin: %+v", ssStr.Plugin)
	}
	vlessH2 := nodes[3].(*serverObj.V2Ray)
	if vlessH2.Host != "a.example,b.example" || vlessH2.Path != "/h2path" {
		t.Fatalf("unexpected vless-h2: host=%q path=%q", vlessH2.Host, vlessH2.Path)
	}
	vmessHTTP := nodes[4].(*serverObj.V2Ray)
	if vmessHTTP.Net != "h2" || vmessHTTP.Host != "h1.example,h2.example" || vmessHTTP.Path != "/first" {
		t.Fatalf("unexpected vmess-http: net=%q host=%q path=%q", vmessHTTP.Net, vmessHTTP.Host, vmessHTTP.Path)
	}
}

func TestDetectRuleFirstIPRuleProtocolNotCatchAllAndCaseInsensitiveNetwork(t *testing.T) {
	tmpl := &v2ray.Template{}
	tmpl.Routing.Rules = []coreObj.RoutingRule{
		{
			OutboundTag: "block",
			Protocol:    []string{"bittorrent"},
		},
		{
			OutboundTag: "direct-src",
			Source:      []string{"10.0.0.0/8"},
		},
		{
			OutboundTag: "direct-ip",
			IP:          []string{"192.0.2.0/24"},
			Network:     "TCP,UDP",
		},
		{
			OutboundTag: "proxy",
		},
	}

	res := DetectRuleOnTemplate(tmpl, "192.0.2.10", "443", "tcp", "")
	if res.Matched != 2 || res.Outbound != "direct-ip" {
		t.Fatalf("DetectRuleOnTemplate matched=%d outbound=%q rules=%+v; want idx=2 outbound=direct-ip", res.Matched, res.Outbound, res.Rules)
	}
	if res.Rules[0].Matched || res.Rules[1].Matched {
		t.Fatalf("protocol/source rules must not match as catch-all: %+v", res.Rules[:2])
	}
}

func TestRemoteUpdateInfoConcurrentAccess(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			SetRemoteUpdateInfo(i%2 == 0, "2.2.0")
		}
	}()
	for i := 0; i < 100; i++ {
		_, _ = GetRemoteUpdateInfo()
	}
	<-done
}

func TestDeleteWhichPrunesDeletedSubscriptionsFromMemberRules(t *testing.T) {
	resetReviewDB(t)
	if err := configure.AddOutbound("auto1"); err != nil {
		t.Fatal(err)
	}
	if err := configure.AddOutbound("auto2"); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{
		{Address: "https://sub1.example", Servers: []configure.ServerRaw{reviewNode(t, "hk-1")}},
		{Address: "https://sub2.example", Servers: []configure.ServerRaw{reviewNode(t, "hk-2")}},
	}); err != nil {
		t.Fatal(err)
	}
	id1 := configure.GetSubscription(0).DatabaseID
	id2 := configure.GetSubscription(1).DatabaseID

	// auto1 depends on both subscriptions; auto2 depends only on sub1.
	s1 := configure.DefaultOutboundSetting()
	s1.MemberRule = &configure.OutboundMemberRule{SubscriptionIDs: []int64{id1, id2}, Mode: "keyword", Pattern: "hk"}
	if err := ApplyOutboundSetting("auto1", s1); err != nil {
		t.Fatal(err)
	}
	s2 := configure.DefaultOutboundSetting()
	s2.MemberRule = &configure.OutboundMemberRule{SubscriptionIDs: []int64{id1}, Mode: "keyword", Pattern: "hk"}
	if err := ApplyOutboundSetting("auto2", s2); err != nil {
		t.Fatal(err)
	}

	if err := DeleteWhich([]*configure.Which{{TYPE: configure.SubscriptionType, ID: 1}}); err != nil {
		t.Fatal(err)
	}

	got1 := configure.GetOutboundSetting("auto1")
	if got1.MemberRule == nil || !slices.Equal(got1.MemberRule.SubscriptionIDs, []int64{id2}) {
		t.Fatalf("auto1 MemberRule not pruned: %+v", got1.MemberRule)
	}
	if _, err := PreviewOutboundMembers("auto1", *got1.MemberRule); err != nil {
		t.Fatalf("PreviewOutboundMembers(auto1) failed after sub deletion: %v", err)
	}
	got2 := configure.GetOutboundSetting("auto2")
	if got2.MemberRule != nil {
		t.Fatalf("auto2 MemberRule should be nil after its only source subscription was deleted: %+v", got2.MemberRule)
	}
}

func TestWebdavListSkipsCollectionsAndBaseDirectory(t *testing.T) {
	propfindXML := `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/dav/v2raya</d:href>
    <d:propstat><d:prop><d:resourcetype/></d:prop></d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/v2raya/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/v2raya/subdir</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/v2raya/v2raya-backup-20260101.db</d:href>
    <d:propstat><d:prop><d:resourcetype/><d:getcontentlength>4096</d:getcontentlength></d:prop></d:propstat>
  </d:response>
</d:multistatus>`

	client := webdavClient{
		baseURL: "https://dav.example.test/dav/v2raya",
		clients: []*http.Client{{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				reqBody, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(reqBody), "resourcetype") {
					t.Errorf("PROPFIND did not request resourcetype: %s", string(reqBody))
				}
				return &http.Response{
					StatusCode: 207,
					Body:       io.NopCloser(strings.NewReader(propfindXML)),
					Header:     make(http.Header),
					Request:    r,
				}, nil
			}),
		}},
	}
	items, err := client.list()
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(items) != 1 || items[0].Name != "v2raya-backup-20260101.db" || items[0].Size != 4096 {
		t.Fatalf("unexpected webdav items: %+v", items)
	}
}

func TestBackupPathAndWebdavRejectInvalidNamesAndDirectories(t *testing.T) {
	resetReviewDB(t)
	if err := os.MkdirAll(filepath.Join(backupDir(), "adir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", ".", "..", "../evil.db", "a/b.db", "a\\b.db", "adir"} {
		if _, err := OpenBackup(bad); err == nil {
			t.Errorf("OpenBackup(%q) succeeded, want error", bad)
		}
	}
	for _, bad := range []string{"", ".", "..", "../evil.db"} {
		if err := DownloadWebdavBackup(bad, filepath.Join(t.TempDir(), "out.db")); err == nil {
			t.Errorf("DownloadWebdavBackup(%q) succeeded, want error", bad)
		}
		if _, err := OpenWebdavBackupStream(bad); err == nil {
			t.Errorf("OpenWebdavBackupStream(%q) succeeded, want error", bad)
		}
	}
}

func TestCreatedBackupCanBeOpenedAndRestored(t *testing.T) {
	resetReviewDB(t)
	item, err := CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenBackup(item.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() != item.Size || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("invalid backup file: %v", info)
	}
	path, err := BackupPath(item.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRestoreDatabase(path); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadWebdavBackupPermissionsAndRestoreCleanup(t *testing.T) {
	resetReviewDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-a-valid-sqlite-database")
	}))
	defer srv.Close()

	s := configure.GetSettingNotNil()
	s.WebdavUrl = srv.URL
	s.WebdavConnectionMode = configure.WebdavConnectionOnlyDirect
	if err := configure.SetSetting(s); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "downloaded.db")
	if err := DownloadWebdavBackup("bad.db", dst); err != nil {
		t.Fatalf("DownloadWebdavBackup error = %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("downloaded backup mode = %o, want 0600", perm)
		}
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(dst, 0644); err != nil {
			t.Fatal(err)
		}
		if err := DownloadWebdavBackup("bad.db", dst); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(dst)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("replacing an earlier download did not restore private permissions: %v %v", info, err)
		}
	}

	// RestoreFromBackup must remove staging file when validation fails.
	if err := RestoreFromBackup("webdav", "bad.db"); err == nil {
		t.Fatal("RestoreFromBackup accepted invalid database")
	}
	staging := filepath.Join(conf.GetEnvironmentConfig().Config, "restore-staging.db")
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging file was not removed after validation failure")
	}
}

func TestRestoreFromBackupResolvesExecutableBeforeMarkerAndCleansMarkerOnExecFail(t *testing.T) {
	resetReviewDB(t)
	if err := os.MkdirAll(backupDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	validBackup := filepath.Join(backupDir(), "valid.db")
	restoreFixture(t, validBackup, "ok")

	origResolve := resolveRestoreExecutable
	origExec := execRestoreProcess
	origDelay := restoreExecDelay
	t.Cleanup(func() {
		resolveRestoreExecutable = origResolve
		execRestoreProcess = origExec
		restoreExecDelay = origDelay
	})

	// 1. If binary cannot be resolved, fail BEFORE writing restore-pending.json.
	resolveRestoreExecutable = func() (string, error) {
		return "", errors.New("binary missing")
	}
	if err := RestoreFromBackup("local", "valid.db"); err == nil {
		t.Fatal("RestoreFromBackup succeeded when executable resolution failed")
	}
	marker := filepath.Join(conf.GetEnvironmentConfig().Config, "restore-pending.json")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("marker file was created despite executable resolution failure")
	}

	if runtime.GOOS == "windows" {
		if err := RestoreFromBackup("local", "valid.db"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("expected a pending restore for the next Windows service start", err)
		}
		return
	}

	// 2. If syscall.Exec fails in goroutine, marker file must be removed.
	execDone := make(chan string, 1)
	resolveRestoreExecutable = func() (string, error) { return "/fake/v2raya", nil }
	restoreExecDelay = 10 * time.Millisecond
	execRestoreProcess = func(argv0 string, _ []string, _ []string) error {
		execDone <- argv0
		return errors.New("exec failed")
	}
	if err := RestoreFromBackup("local", "valid.db"); err != nil {
		t.Fatalf("RestoreFromBackup error = %v", err)
	}
	select {
	case gotExe := <-execDone:
		if gotExe != "/fake/v2raya" {
			t.Fatalf("exec called with %q, want /fake/v2raya", gotExe)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for restore exec goroutine")
	}
	// Wait briefly for marker removal after execRestoreProcess returns.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("marker file was not removed after exec failure")
	}
}

func TestModifySubscriptionRemarkPreservesTelemetryAndValidatesAddress(t *testing.T) {
	resetReviewDB(t)
	sub := &configure.SubscriptionRaw{
		Address: "https://provider.example/sub",
		Remarks: "orig",
		Servers: []configure.ServerRaw{reviewNode(t, "node1")},
		Extra: configure.SubscriptionExtra{
			LastUpdateTime: 1700000000,
			Upload:         111,
			Download:       222,
			Total:          333,
			Expire:         1800000000,
		},
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
		t.Fatal(err)
	}

	// Invalid addresses must be rejected.
	for _, badAddr := range []string{"", "file:///etc/passwd", "ftp://example.com/sub", "not-a-url"} {
		err := ModifySubscriptionRemark(touch.Subscription{
			ID:      1,
			Address: badAddr,
			Remarks: "edited",
		})
		if err == nil {
			t.Errorf("ModifySubscriptionRemark accepted invalid address %q", badAddr)
		}
	}

	// Omitted/zeroed telemetry fields in PATCH must not overwrite stored telemetry.
	err := ModifySubscriptionRemark(touch.Subscription{
		ID:      1,
		Address: "https://provider.example/sub2",
		Remarks: "edited",
		Extra:   configure.SubscriptionExtra{},
	})
	if err != nil {
		t.Fatalf("ModifySubscriptionRemark error = %v", err)
	}
	got := configure.GetSubscription(0)
	if got.Remarks != "edited" || got.Address != "https://provider.example/sub2" {
		t.Fatalf("metadata not updated: %+v", got)
	}
	if got.Extra.LastUpdateTime != 1700000000 || got.Extra.Upload != 111 || got.Extra.Download != 222 || got.Extra.Total != 333 || got.Extra.Expire != 1800000000 {
		t.Fatalf("telemetry overwritten: %+v", got.Extra)
	}
}

func TestUpdateSubscriptionOldNameCountUsesAllOldServers(t *testing.T) {
	resetReviewDB(t)
	// Two old nodes share the same name "dup" on different IPs; only node 1 is connected.
	n1 := "vless://00000000-0000-0000-0000-000000000001@old1.example:443#dup"
	n2 := "vless://00000000-0000-0000-0000-000000000002@old2.example:443#dup"
	rotated := "vless://00000000-0000-0000-0000-000000000002@new2.example:443#dup"

	var body atomic.Value
	body.Store(n1 + "\n" + n2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body.Load().(string))
	}))
	defer srv.Close()

	if err := Import(srv.URL, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Verify zero Expire stored as 0 (L-4), not -62135596800.
	if got := configure.GetSubscription(0); got.Extra.Expire != 0 {
		t.Fatalf("Expire for subscription without expire header = %d, want 0", got.Extra.Expire)
	}
	if err := configure.AddConnect(configure.Which{TYPE: configure.SubscriptionServerType, Sub: 0, ID: 1, Outbound: "proxy"}); err != nil {
		t.Fatal(err)
	}

	// Now provider drops node 1 and rotates node 2's IP while keeping name "dup".
	// Because oldRaw had TWO nodes named "dup", node 1 must NOT be remapped to node 2!
	body.Store(rotated)
	if err := UpdateSubscription(0, true); err != nil {
		t.Fatal(err)
	}
	if conns := configure.GetConnectedServersByOutbound("proxy"); conns.Len() != 0 {
		t.Fatalf("ambiguous duplicate old name wrongly remapped connected server: %+v", conns.Get())
	}
}

func TestPreviewSubscriptionFilterNilGuard(t *testing.T) {
	resetReviewDB(t)
	if _, _, err := PreviewSubscriptionFilter(1, configure.SubscriptionExtra{}); err == nil {
		t.Fatal("PreviewSubscriptionFilter succeeded for missing subscription")
	}
}

func TestCheckUpdateHandlesNon200AndParsesValidPackages(t *testing.T) {
	origURL := checkUpdateURL
	origVer := conf.Version
	t.Cleanup(func() {
		checkUpdateURL = origURL
		conf.Version = origVer
	})

	status := http.StatusInternalServerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = io.WriteString(w, "Package: v2raya\nVersion: 9.9.9\n")
		}
	}))
	defer srv.Close()
	checkUpdateURL = srv.URL
	conf.Version = "2.0.0"

	if _, _, err := CheckUpdate(); err == nil {
		t.Fatal("CheckUpdate accepted HTTP 500 response")
	}
	status = http.StatusOK
	found, remote, err := CheckUpdate()
	if err != nil || !found || remote != "9.9.9" {
		t.Fatalf("CheckUpdate = (%v, %q, %v), want (true, 9.9.9, nil)", found, remote, err)
	}
}

func TestRefreshNoticesDoesNotMutateDefaultClientTimeout(t *testing.T) {
	resetReviewDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"notices":[]}`)
	}))
	defer srv.Close()
	origURL := noticeURL
	noticeURL = srv.URL
	origTimeout := http.DefaultClient.Timeout
	http.DefaultClient.Timeout = 0
	t.Cleanup(func() {
		noticeURL = origURL
		http.DefaultClient.Timeout = origTimeout
	})

	if err := RefreshNotices(); err != nil {
		t.Fatalf("RefreshNotices() error = %v", err)
	}
	if http.DefaultClient.Timeout != 0 {
		t.Fatalf("http.DefaultClient.Timeout mutated to %v", http.DefaultClient.Timeout)
	}
}
