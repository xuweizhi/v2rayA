// Package tlsutil provides shared certificate pinning and peer-name verification
// helpers used by the native outbound protocol handlers (anytls, tuic, juicity).
//
// SPDX-License-Identifier: MPL-2.0
package tlsutil

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// ParsePinnedChain accepts a single pin value (hex or base64/base64url, optional
// "sha256:" prefix, optional openssl-style colon separators) and returns the
// parsed 32-byte hash. An empty pin yields (nil, nil). A non-empty but invalid
// pin returns an error so callers can fail loudly instead of silently dropping
// the pin.
func ParsePinnedChain(pin string) ([]byte, error) {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return nil, nil
	}
	pin = strings.TrimPrefix(pin, "sha256:")
	pin = strings.TrimPrefix(pin, "SHA256:")
	// Strip openssl-style colons: AB:CD:EF:...
	pin = strings.ReplaceAll(pin, ":", "")
	pin = strings.TrimSpace(pin)

	if raw, err := hex.DecodeString(pin); err == nil && len(raw) == 32 {
		return raw, nil
	}
	// Standard and URL-safe (base64url) alphabets, with and without padding.
	for _, dec := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if raw, err := dec.DecodeString(pin); err == nil && len(raw) == 32 {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("unrecognized certificate chain pin: %q", pin)
}

// ParsePinnedChains parses a comma-separated list of certificate / chain SHA-256
// pins (matching Xray 25+ pinnedPeerCertSha256 format). Empty input yields
// (nil, nil).
func ParsePinnedChains(pins string) ([][]byte, error) {
	if strings.TrimSpace(pins) == "" {
		return nil, nil
	}
	var out [][]byte
	for part := range strings.SplitSeq(pins, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty certificate pin")
		}
		h, err := ParsePinnedChain(part)
		if err != nil {
			return nil, err
		}
		if len(h) > 0 {
			out = append(out, h)
		}
	}
	return out, nil
}

// ParseVerifyNames parses a comma-separated list of DNS names for
// verifyPeerCertByName.
func ParseVerifyNames(names string) []string {
	if strings.TrimSpace(names) == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(names, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// GenerateChainHash mirrors legacy xray-core's tls.GenerateCertChainHash so
// that chain pin values remain interchangeable across protocols.
func GenerateChainHash(rawCerts [][]byte) []byte {
	var hashValue []byte
	for _, certValue := range rawCerts {
		out := sha256.Sum256(certValue)
		if hashValue == nil {
			hashValue = out[:]
		} else {
			newHash := sha256.Sum256(append(hashValue, out[:]...))
			hashValue = newHash[:]
		}
	}
	return hashValue
}

func hashMatchesAny(got []byte, expected [][]byte) bool {
	for _, h := range expected {
		if len(h) > 0 && hmac.Equal(got, h) {
			return true
		}
	}
	return false
}

// VerifyPeerChain checks a legacy pin against the entire peer chain, in order.
func VerifyPeerChain(rawCerts [][]byte, expected [][]byte) error {
	if len(rawCerts) == 0 || !hashMatchesAny(GenerateChainHash(rawCerts), expected) {
		return fmt.Errorf("peer certificate chain did not match a configured chain pin")
	}
	return nil
}

// PinVerifier checks the legacy whole-chain fingerprint.
func PinVerifier(hash []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return VerifyPeerChain(rawCerts, [][]byte{hash})
	}
}

// ConfigurePeerVerification keeps legacy whole-chain pins distinct from
// certificate fingerprints. If both are configured, both must match.
func ConfigurePeerVerification(config *tls.Config, certSpec, chainSpec, nameSpec string) error {
	certPins, err := ParsePinnedChains(certSpec)
	if err != nil {
		return err
	}
	chainPins, err := ParsePinnedChains(chainSpec)
	if err != nil {
		return err
	}
	names := ParseVerifyNames(nameSpec)
	if nameSpec != "" && len(names) == 0 {
		return fmt.Errorf("no verification name specified")
	}
	if len(certPins)+len(chainPins)+len(names) == 0 {
		return nil
	}
	config.InsecureSkipVerify = true // Verification is performed below, including custom trust roots.
	serverName, roots := config.ServerName, config.RootCAs
	config.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(chainPins) > 0 {
			if err := VerifyPeerChain(rawCerts, chainPins); err != nil {
				return err
			}
		}
		return verifyCertificates(rawCerts, certPins, names, serverName, roots, len(chainPins) > 0)
	}
	return nil
}

func verifyCertificates(rawCerts [][]byte, pins [][]byte, names []string, sni string, roots *x509.CertPool, chainPinned bool) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("peer presented no certificates")
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	leafHash := sha256.Sum256(leaf.Raw)
	leafPinned := hashMatchesAny(leafHash[:], pins)
	if leafPinned || (chainPinned && len(pins) == 0) {
		if len(names) == 0 {
			return nil
		}
		for _, name := range names {
			if err := leaf.VerifyHostname(name); err == nil {
				return nil
			}
		}
		return fmt.Errorf("pinned certificate does not match any configured peer name")
	}

	intermediates := x509.NewCertPool()
	if len(pins) > 0 {
		// Only pinned CAs can anchor this chain. An unrelated presented
		// certificate with a matching fingerprint cannot authenticate the leaf.
		roots = x509.NewCertPool()
	}
	for _, raw := range rawCerts[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return err
		}
		intermediates.AddCert(cert)
		h := sha256.Sum256(raw)
		if cert.IsCA && hashMatchesAny(h[:], pins) {
			roots.AddCert(cert)
		}
	}
	if len(names) == 0 {
		if sni == "" {
			return fmt.Errorf("a server name is required for a pinned CA")
		}
		names = []string{sni}
	}
	for _, name := range names {
		_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: name})
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("peer certificate verification failed: %w", err)
}
