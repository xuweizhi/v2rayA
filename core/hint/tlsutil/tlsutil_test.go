package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"
)

func TestParsePinnedChain(t *testing.T) {
	cases := []struct {
		name string
		pin  string
		ok   bool
	}{
		{"empty", "", true},
		{"std base64 padded", "YAeLpzeMUkvQyVF5zpGov2ZMfdNfnfqkFHEf+w4EA+c=", true},
		{"base64url unpadded", "YAeLpzeMUkvQyVF5zpGov2ZMfdNfnfqkFHEf-w4EA-c", true},
		{"base64url padded", "YAeLpzeMUkvQyVF5zpGov2ZMfdNfnfqkFHEf-w4EA-c=", true},
		{"hex", "601ceb02bd8a1e929519a9db9d00c1424c0948cd679bd06f407c913bc68a9da1", true},
		{"hex with colons", "60:1c:eb:02:bd:8a:1e:92:95:19:a9:db:9d:00:c1:42:4c:09:48:cd:67:9b:d0:6f:40:7c:91:3b:c6:8a:9d:a1", true},
		{"sha256 prefix hex", "sha256:601ceb02bd8a1e929519a9db9d00c1424c0948cd679bd06f407c913bc68a9da1", true},
		{"invalid", "not-a-pin", false},
		{"short base64", "YWJj", false},
	}
	for _, c := range cases {
		hash, err := ParsePinnedChain(c.pin)
		if c.ok {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", c.name, err)
				continue
			}
			if c.pin == "" {
				if len(hash) != 0 {
					t.Errorf("%s: empty pin must yield empty hash, got %d", c.name, len(hash))
				}
				continue
			}
			if len(hash) != 32 {
				t.Errorf("%s: expected 32-byte hash, got %d", c.name, len(hash))
			}
		} else if err == nil {
			t.Errorf("%s: expected error, got nil (hash len %d)", c.name, len(hash))
		}
	}
}

func TestParsePinnedChains(t *testing.T) {
	p1 := "601ceb02bd8a1e929519a9db9d00c1424c0948cd679bd06f407c913bc68a9da1"
	p2 := "YAeLpzeMUkvQyVF5zpGov2ZMfdNfnfqkFHEf+w4EA+c="
	pins, err := ParsePinnedChains(p1 + " , " + p2)
	if err != nil {
		t.Fatalf("ParsePinnedChains failed: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("got %d pins, want 2", len(pins))
	}
	if _, err := ParsePinnedChains(p1 + ",badpin"); err == nil {
		t.Fatalf("expected error on invalid pin in comma-separated list")
	}
}

func TestGenerateChainHash(t *testing.T) {
	cert1 := []byte("cert-1")
	cert2 := []byte("cert-2")
	h1 := GenerateChainHash([][]byte{cert1})
	h2 := GenerateChainHash([][]byte{cert2})
	h12 := GenerateChainHash([][]byte{cert1, cert2})
	if len(h1) != 32 || len(h2) != 32 || len(h12) != 32 {
		t.Fatalf("expected 32-byte hashes")
	}
	if string(h1) == string(h2) {
		t.Fatalf("different certs must not collide")
	}
	if string(h12) == string(h1) || string(h12) == string(h2) {
		t.Fatalf("chained hash must differ from single-cert hashes")
	}
}

func TestVerifyPeerChainSingleAndChained(t *testing.T) {
	leaf := []byte("leaf-cert")
	ca := []byte("ca-cert")
	chain := [][]byte{leaf, ca}

	chainHash := GenerateChainHash(chain)
	leafHash := sha256.Sum256(chain[0])
	if err := VerifyPeerChain(chain, [][]byte{chainHash}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPeerChain(chain, [][]byte{leafHash[:]}); err == nil {
		t.Fatal("chain pin accepted only the leaf")
	}
	if err := VerifyPeerChain(nil, [][]byte{chainHash}); err == nil {
		t.Fatalf("empty rawCerts must fail")
	}
}

func makeTestCertChain(t *testing.T, dnsName string) (leafDER, caDER []byte, caCert *x509.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err = x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err = x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return leafDER, caDER, caCert
}

func TestBuildVerifyPeerCertificate(t *testing.T) {
	leafDER, caDER, caCert := makeTestCertChain(t, "real.example.com")
	otherLeafDER, _, _ := makeTestCertChain(t, "real.example.com") // signed by a DIFFERENT CA

	rawChain := [][]byte{leafDER, caDER}
	leafHash := sha256.Sum256(leafDER)
	caHash := sha256.Sum256(caDER)
	chainHash := GenerateChainHash(rawChain)

	// 1. Leaf single-cert hash (Xray 25+ pinnedPeerCertSha256)
	check := func(raw [][]byte, certPin, chainPin []byte, names, sni string) error {
		cfg := &tls.Config{ServerName: sni}
		if err := ConfigurePeerVerification(cfg, hex.EncodeToString(certPin), hex.EncodeToString(chainPin), names); err != nil {
			return err
		}
		return cfg.VerifyPeerCertificate(raw, nil)
	}
	for _, tc := range []struct {
		label       string
		raw         [][]byte
		cert, chain []byte
		names       string
		ok          bool
	}{
		{"leaf", rawChain, leafHash[:], nil, "", true},
		{"legacy chain", rawChain, nil, chainHash, "", true},
		{"leaf alone cannot satisfy chain pin", rawChain, nil, leafHash[:], "", false},
		{"leaf and name", rawChain, leafHash[:], nil, "real.example.com", true},
		{"leaf and wrong name", rawChain, leafHash[:], nil, "wrong.example.com", false},
		{"CA and name", rawChain, caHash[:], nil, "other.test,real.example.com", true},
		{"CA and wrong name", rawChain, caHash[:], nil, "wrong.test", false},
		{"leaf signed by unrelated CA", [][]byte{otherLeafDER, caDER}, caHash[:], nil, "real.example.com", false},
		{"both pins", rawChain, leafHash[:], chainHash, "", true},
		{"mismatching chain pin", rawChain, leafHash[:], caHash[:], "", false},
	} {
		if err := check(tc.raw, tc.cert, tc.chain, tc.names, "real.example.com"); (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.label, err)
		}
	}
	// 5. verifyPeerCertByName alone (without pin) using RootCAs
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	tlsCfg := &tls.Config{ServerName: "sni.front.test", RootCAs: pool}
	if err := ConfigurePeerVerification(tlsCfg, "", "", "other.test, real.example.com"); err != nil {
		t.Fatalf("ConfigurePeerVerification failed: %v", err)
	}
	if !tlsCfg.InsecureSkipVerify || tlsCfg.VerifyPeerCertificate == nil {
		t.Fatalf("expected custom VerifyPeerCertificate with InsecureSkipVerify=true")
	}
	if err := tlsCfg.VerifyPeerCertificate(rawChain, nil); err != nil {
		t.Fatalf("verifyPeerCertByName alone failed: %v", err)
	}

	tlsCfgBad := &tls.Config{ServerName: "real.example.com", RootCAs: pool}
	if err := ConfigurePeerVerification(tlsCfgBad, "", "", "nope.example.com"); err != nil {
		t.Fatalf("ConfigurePeerVerification failed: %v", err)
	}
	if err := tlsCfgBad.VerifyPeerCertificate(rawChain, nil); err == nil {
		t.Fatalf("verifyPeerCertByName with wrong name must fail even when SNI matches")
	}

	// 6. ConfigurePeerVerification with hex leaf pin
	tlsCfgPin := &tls.Config{ServerName: "any.sni"}
	if err := ConfigurePeerVerification(tlsCfgPin, hex.EncodeToString(leafHash[:]), "", ""); err != nil {
		t.Fatalf("ConfigurePeerVerification hex leaf pin failed: %v", err)
	}
	if err := tlsCfgPin.VerifyPeerCertificate(rawChain, nil); err != nil {
		t.Fatalf("hex leaf pin verification failed: %v", err)
	}
}
