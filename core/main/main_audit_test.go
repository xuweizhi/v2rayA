package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/v2rayA/v2raya-core/dns"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net/cnc"
	"github.com/xtls/xray-core/transport/pipe"
	"google.golang.org/protobuf/proto"
)

func writeTestGeositeDat(t *testing.T, sites []*geodata.GeoSite) string {
	t.Helper()
	var buf bytes.Buffer
	for _, site := range sites {
		body, err := proto.Marshal(site)
		if err != nil {
			t.Fatalf("marshal geosite: %v", err)
		}
		buf.WriteByte(0x0a)
		var lenBuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenBuf[:], uint64(len(body)))
		buf.Write(lenBuf[:n])
		buf.Write(body)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "geosite.dat")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write geosite.dat: %v", err)
	}
	return dir
}

func TestGeositeAttrFilteringAndAllDomainTypes(t *testing.T) {
	assetDir := writeTestGeositeDat(t, []*geodata.GeoSite{
		{
			Code: "GOOGLE",
			Domain: []*geodata.Domain{
				{
					Type:  geodata.Domain_Domain,
					Value: "google.cn",
					Attribute: []*geodata.Domain_Attribute{
						{Key: "CN", TypedValue: &geodata.Domain_Attribute_BoolValue{BoolValue: true}},
					},
				},
				{
					Type:  geodata.Domain_Full,
					Value: "dl.google.com",
					Attribute: []*geodata.Domain_Attribute{
						{Key: "cn", TypedValue: &geodata.Domain_Attribute_BoolValue{BoolValue: true}},
						{Key: "dl", TypedValue: &geodata.Domain_Attribute_BoolValue{BoolValue: true}},
					},
				},
				{
					Type:  geodata.Domain_Regex,
					Value: `^mtalk[0-9]*\.google\.com$`,
					Attribute: []*geodata.Domain_Attribute{
						{Key: "cn", TypedValue: &geodata.Domain_Attribute_BoolValue{BoolValue: true}},
					},
				},
				{
					Type:  geodata.Domain_Substr,
					Value: "googleapis",
					Attribute: []*geodata.Domain_Attribute{
						{Key: "cn", TypedValue: &geodata.Domain_Attribute_BoolValue{BoolValue: true}},
					},
				},
				{
					Type:  geodata.Domain_Domain,
					Value: "google.com",
				},
			},
		},
	})

	t.Setenv("XRAY_LOCATION_ASSET", assetDir)
	geositePath := filepath.Join(assetDir, "geosite.dat")

	// Case-insensitive tag + @attr filtering.
	cnDomains, err := loadGeositeTag(geositePath, "google@cn")
	if err != nil {
		t.Fatalf("loadGeositeTag google@cn: %v", err)
	}
	if len(cnDomains) != 4 {
		t.Fatalf("google@cn returned %d domains, want 4", len(cnDomains))
	}

	// Multiple @attr AND filtering.
	cnDlDomains, err := loadGeositeTag(geositePath, "GOOGLE@CN@DL")
	if err != nil {
		t.Fatalf("loadGeositeTag GOOGLE@CN@DL: %v", err)
	}
	if len(cnDlDomains) != 1 || cnDlDomains[0].Value != "dl.google.com" {
		t.Fatalf("GOOGLE@CN@DL unexpected result: %+v", cnDlDomains)
	}

	// Negated @!attr filtering.
	nonCnDomains, err := loadGeositeTag(geositePath, "google@!cn")
	if err != nil {
		t.Fatalf("loadGeositeTag google@!cn: %v", err)
	}
	if len(nonCnDomains) != 1 || nonCnDomains[0].Value != "google.com" {
		t.Fatalf("google@!cn unexpected result: %+v", nonCnDomains)
	}

	// Missing tag returns error on BOTH first and cached second lookup.
	if _, err := loadGeositeTag(geositePath, "missing"); err == nil {
		t.Fatal("expected error for missing tag")
	}
	if _, err := loadGeositeTag(geositePath, "missing"); err == nil {
		t.Fatal("expected error for cached missing tag")
	}

	// Integration with dns.BuildMatchers / Router: Regex and Substr must match accurately.
	rule := &dns.DnsRule{ID: "g-cn", Domain: []string{"geosite:google@cn"}, Upstream: "cn"}
	matchers, err := dns.BuildMatchers(rule, geositeResolver)
	if err != nil {
		t.Fatalf("BuildMatchers: %v", err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"www.google.cn", true},
		{"dl.google.com", true},
		{"sub.dl.google.com", false},   // Full match only
		{"mtalk4.google.com", true},    // Regex match
		{"fonts.googleapis.com", true}, // Substr/keyword match
		{"www.google.com", false},      // Filtered out by @cn
	}
	for _, tc := range cases {
		if got := dns.MatchAll(matchers, &dns.DnsQuery{Name: tc.name, QType: dns.TypeA}); got != tc.want {
			t.Errorf("MatchAll(google@cn, %q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDeadlineConnAbortsStalledXrayCNCConnection(t *testing.T) {
	// 1. SetReadDeadline must interrupt a blocked Read on xray's cnc.Connection.
	r1, w1 := pipe.New()
	r2, w2 := pipe.New()
	defer w1.Close()
	defer r2.Interrupt()
	raw := cnc.NewConnection(cnc.ConnectionInputMulti(w2), cnc.ConnectionOutputMulti(r1))
	conn := newDeadlineConn(context.Background(), raw)
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	start := time.Now()
	buf := make([]byte, 16)
	_, err := conn.Read(buf)
	if err == nil {
		t.Fatal("expected deadline error from stalled cnc.Connection")
	}
	var netErr net.Error
	if !errors.Is(err, os.ErrDeadlineExceeded) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
		t.Errorf("expected timeout/deadline error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Read did not abort promptly on deadline: took %v", elapsed)
	}

	// 2. Context cancellation must also abort a blocked Read.
	r3, w3 := pipe.New()
	r4, w4 := pipe.New()
	defer w3.Close()
	defer r4.Interrupt()
	ctx, cancel := context.WithCancel(context.Background())
	raw2 := cnc.NewConnection(cnc.ConnectionInputMulti(w4), cnc.ConnectionOutputMulti(r3))
	conn2 := newDeadlineConn(ctx, raw2)
	defer conn2.Close()

	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	start = time.Now()
	_, err = conn2.Read(buf)
	if err == nil {
		t.Fatal("expected error after context cancellation")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Read did not abort promptly on ctx cancel: took %v", elapsed)
	}
}
