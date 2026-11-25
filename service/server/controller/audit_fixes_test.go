package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
)

func setupControllerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = db.Close() })
	if err := configure.InitDefaultOutbound(); err != nil {
		t.Fatal(err)
	}
}

func TestGetWebdavBackupDownloadNoHeaderOnErrorAndSetsHeaderOn200(t *testing.T) {
	setupControllerTestDB(t)

	r := gin.New()
	r.GET("/webdav/download", GetWebdavBackupDownload)

	// 1. Invalid filename must return JSON FAIL without Content-Disposition header.
	req := httptest.NewRequest(http.MethodGet, "/webdav/download?filename=..", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code < 400 {
		t.Fatalf("expected an unsuccessful HTTP status, got %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("Content-Disposition set on error: %q", cd)
	}
	if !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
		t.Fatalf("expected JSON FAIL response, got: %s", w.Body.String())
	}

	// 2. WebDAV 404 must return JSON FAIL without Content-Disposition header.
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = rw.Write([]byte("backup-payload"))
		}
	}))
	defer srv.Close()
	s := configure.GetSettingNotNil()
	s.WebdavUrl = srv.URL
	s.WebdavConnectionMode = configure.WebdavConnectionOnlyDirect
	if err := configure.SetSetting(s); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, "/webdav/download?filename=backup.db", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code < 400 {
		t.Fatalf("expected an unsuccessful HTTP status, got %d", w.Code)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("Content-Disposition set on WebDAV 404: %q", cd)
	}
	if !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
		t.Fatalf("expected JSON FAIL response on WebDAV 404, got: %s", w.Body.String())
	}

	// 3. WebDAV 200 must set Content-Disposition and stream body.
	status = http.StatusOK
	req = httptest.NewRequest(http.MethodGet, "/webdav/download?filename=backup.db", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "backup-payload" {
		t.Fatalf("unexpected 200 download: status=%d body=%q", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "backup.db") {
		t.Fatalf("unexpected Content-Disposition: %q", cd)
	}
}

func TestCustomInboundCollisionsAndDeleteOutboundRoutingARef(t *testing.T) {
	setupControllerTestDB(t)
	if err := configure.SetPorts(&configure.Ports{Socks5: 20170, Http: 20171}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AddOutbound("customGrp"); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.POST("/customInbound", PostCustomInbound)
	r.DELETE("/outbound", DeleteOutbound)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/customInbound", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Colliding with configured Socks5 port.
	if w := post(`{"tag":"my-in","protocol":"socks","port":20170,"outbound":"proxy","outboundType":"direct"}`); !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
		t.Fatalf("allowed port collision with socks5: %s", w.Body.String())
	}
	// Colliding with transparent port 52345.
	if w := post(`{"tag":"my-in","protocol":"socks","port":52345,"outbound":"proxy","outboundType":"direct"}`); !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
		t.Fatalf("allowed port collision with transparent 52345: %s", w.Body.String())
	}
	// Colliding with built-in tag "socks" and "v2raya-probe".
	for _, tag := range []string{"socks", "api-in", "v2raya-probe"} {
		if w := post(`{"tag":"` + tag + `","protocol":"socks","port":21000,"outbound":"proxy","outboundType":"direct"}`); !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
			t.Fatalf("allowed reserved tag %q: %s", tag, w.Body.String())
		}
	}

	// Valid custom inbound bound to "proxy" but routing to "customGrp" in RoutingARules.
	w := post(`{"tag":"my-routing","protocol":"socks","port":21001,"outbound":"proxy","outboundType":"routingA","routingARules":"domain(geosite:google) -> customGrp"}`)
	if !strings.Contains(w.Body.String(), `"code":"SUCCESS"`) {
		t.Fatalf("valid custom inbound failed: %s", w.Body.String())
	}

	// DeleteOutbound("customGrp") must fail because my-routing's RoutingARules references it.
	delReq := httptest.NewRequest(http.MethodDelete, "/outbound", strings.NewReader(`{"outbound":"customGrp"}`))
	delReq.Header.Set("Content-Type", "application/json")
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)
	if !strings.Contains(delW.Body.String(), `"code":"FAIL"`) {
		t.Fatalf("DeleteOutbound allowed deleting outbound referenced by RoutingARules: %s", delW.Body.String())
	}
}

func TestExtractWsTokenCaseInsensitiveBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		target string
		header string
		want   string
	}{
		{"/ws?token=" + url.QueryEscape("bearer abc.def"), "", "abc.def"},
		{"/ws?token=" + url.QueryEscape("BEARER xyz.123"), "", "xyz.123"},
		{"/ws?Authorization=" + url.QueryEscape("Bearer qwe.rty"), "", "qwe.rty"},
		{"/ws", "bearer hdr.tok", "hdr.tok"},
		{"/ws", "Bearer hdr2.tok", "hdr2.tok"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		ctx.Request = req
		if got := extractWsToken(ctx); got != tc.want {
			t.Errorf("extractWsToken(%q, %q) = %q, want %q", tc.target, tc.header, got, tc.want)
		}
	}
}

func TestPostAccountRejectsBlankUsername(t *testing.T) {
	setupControllerTestDB(t)
	r := gin.New()
	r.POST("/account", PostAccount)
	for _, uname := range []string{"", "   "} {
		req := httptest.NewRequest(http.MethodPost, "/account", strings.NewReader(`{"username":"`+uname+`","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), `"code":"FAIL"`) {
			t.Fatalf("PostAccount accepted blank username %q: %s", uname, w.Body.String())
		}
	}
}

func TestSavingDNSRulesRejectsEmptyListAndPreservesRejectAction(t *testing.T) {
	setupControllerTestDB(t)
	r := gin.New()
	r.PUT("/dnsRules", PutDnsRules)
	put := func(body string) string {
		req := httptest.NewRequest(http.MethodPut, "/dnsRules", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Body.String()
	}
	if body := put(`[{"action":"reject","domain":"full:blocked.example"}]`); !strings.Contains(body, `"code":"SUCCESS"`) {
		t.Fatal(body)
	}
	if body := put("[]"); !strings.Contains(body, `"code":"FAIL"`) {
		t.Fatal(body)
	}
	rules := configure.GetDnsRulesNotNil()
	if len(rules) != 1 || rules[0].Action != "reject" {
		t.Fatalf("losing rules during empty save: %+v", rules)
	}
}
