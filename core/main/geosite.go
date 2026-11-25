package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

// geositeResolver implements dns.GeositeResolver using xray-core's geodata.
// It loads geosite.dat from the standard asset directory and expands tags
// (e.g. "cn") into domain/suffix patterns.
//
// The geosite.dat file uses a custom format: varint-length-delimited GeoSite
// protobuf messages concatenated together (NOT a single GeoSiteList message).
// This function correctly parses that format.
func geositeResolver(tag string) (domains, suffixes []string) {
	// Determine geosite.dat location from environment.
	assetDir := os.Getenv("XRAY_LOCATION_ASSET")
	if assetDir == "" {
		assetDir = os.Getenv("V2RAY_LOCATION_ASSET")
	}
	if assetDir == "" {
		log.Printf("[dns] geosite: XRAY_LOCATION_ASSET not set, skipping geosite expansion")
		return nil, nil
	}

	geositePath := filepath.Join(assetDir, "geosite.dat")
	geosite, err := loadGeositeTag(geositePath, tag)
	if err != nil {
		log.Printf("[dns] geosite: cannot load tag %q from %s: %v", tag, geositePath, err)
		return nil, nil
	}

	for _, domain := range geosite {
		if domain == nil || domain.Value == "" {
			continue
		}
		switch domain.Type {
		case geodata.Domain_Domain:
			// Domain match: treat as suffix match (covers subdomains).
			suffixes = append(suffixes, domain.Value)
		case geodata.Domain_Full:
			// Full match: exact domain only.
			domains = append(domains, domain.Value)
		case geodata.Domain_Regex:
			// Regex: prefix with regexp: so BuildMatchers compiles it as a regex matcher.
			domains = append(domains, "regexp:"+domain.Value)
		case geodata.Domain_Substr:
			// Substring: prefix with keyword: so BuildMatchers compiles it as a substring matcher.
			domains = append(domains, "keyword:"+domain.Value)
		}
	}

	if len(domains) > 0 || len(suffixes) > 0 {
		log.Printf("[dns] geosite: expanded %s → %d domains, %d suffixes", tag, len(domains), len(suffixes))
	}
	return domains, suffixes
}

type geositeCacheEntry struct {
	domains []*geodata.Domain
	found   bool
}

// geositeCache caches parsed GeoSite entries so we don't re-read the file
// on every rule expansion. The cache is keyed by (filePath, UPPER(baseTag)).
var geositeCache struct {
	mu   sync.RWMutex
	data map[string]geositeCacheEntry // key: "filePath:TAG"
}

func init() {
	geositeCache.data = make(map[string]geositeCacheEntry)
}

type geositeAttrFilter struct {
	key     string // lowercased attribute name
	negated bool
}

func parseGeositeTag(tag string) (baseTag string, filters []geositeAttrFilter, err error) {
	baseTag, rawAttrs, hasAttr := strings.Cut(tag, "@")
	baseTag = strings.TrimSpace(baseTag)
	if baseTag == "" {
		return "", nil, fmt.Errorf("empty geosite tag %q", tag)
	}
	if !hasAttr {
		return baseTag, nil, nil
	}
	if rawAttrs == "" || strings.HasSuffix(rawAttrs, "@") || strings.Contains(rawAttrs, "@@") {
		return "", nil, fmt.Errorf("invalid geosite attribute syntax in %q", tag)
	}
	for _, part := range strings.Split(rawAttrs, "@") {
		negated := false
		if strings.HasPrefix(part, "!") {
			negated = true
			part = strings.TrimPrefix(part, "!")
		}
		part = strings.TrimSpace(part)
		if part == "" {
			return "", nil, fmt.Errorf("empty geosite attribute in %q", tag)
		}
		filters = append(filters, geositeAttrFilter{
			key:     strings.ToLower(part),
			negated: negated,
		})
	}
	return baseTag, filters, nil
}

func domainHasAttribute(d *geodata.Domain, attrLower string) bool {
	if d == nil {
		return false
	}
	for _, attr := range d.Attribute {
		if attr == nil {
			continue
		}
		if !strings.EqualFold(attr.Key, attrLower) {
			continue
		}
		if bv, ok := attr.TypedValue.(*geodata.Domain_Attribute_BoolValue); ok && !bv.BoolValue {
			continue
		}
		return true
	}
	return false
}

func filterDomainsByAttrs(domains []*geodata.Domain, filters []geositeAttrFilter) []*geodata.Domain {
	if len(filters) == 0 {
		return domains
	}
	filtered := make([]*geodata.Domain, 0, len(domains))
	for _, d := range domains {
		matched := true
		for _, f := range filters {
			has := domainHasAttribute(d, f.key)
			if (f.negated && has) || (!f.negated && !has) {
				matched = false
				break
			}
		}
		if matched {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// loadGeositeTag reads geosite.dat and extracts the domains for a specific tag,
// supporting optional @attr attribute filters (e.g. "google@cn", "google@!cn",
// "google@cn@ads"), matched case-insensitively.
//
// The geosite.dat file uses a custom format where each entry is a
// varint-length-delimited GeoSite protobuf message. This is NOT a single
// GeoSiteList message — attempting proto.Unmarshal of the entire file as
// GeoSiteList would only parse the first entry.
//
// This function correctly reads the stream by:
//  1. Reading the outer varint: frame tag (0x0a = field 1, wire type 2) + length
//  2. Extracting the message body
//  3. Unmarshalling it as GeoSite
//  4. Checking if the code matches (case-insensitively) and applying @attr filters
func loadGeositeTag(filePath, tag string) ([]*geodata.Domain, error) {
	baseTag, filters, err := parseGeositeTag(tag)
	if err != nil {
		return nil, err
	}

	baseDomains, err := loadGeositeBaseTag(filePath, baseTag)
	if err != nil {
		return nil, err
	}
	return filterDomainsByAttrs(baseDomains, filters), nil
}

func loadGeositeBaseTag(filePath, baseTag string) ([]*geodata.Domain, error) {
	cacheKey := filePath + ":" + strings.ToUpper(baseTag)
	geositeCache.mu.RLock()
	if cached, ok := geositeCache.data[cacheKey]; ok {
		geositeCache.mu.RUnlock()
		if !cached.found {
			return nil, fmt.Errorf("tag %q not found in %s", baseTag, filePath)
		}
		return cached.domains, nil
	}
	geositeCache.mu.RUnlock()

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)

	for {
		// Read the outer frame tag byte.
		// Each entry starts with field 1 wire type 2 (length-delimited) = 0x0a.
		tagByte, err := br.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read frame tag: %w", err)
		}
		if tagByte != 0x0a {
			// Not the expected frame format — skip.
			continue
		}

		// Read the varint length of the message body.
		bodyLen, err := binary.ReadUvarint(br)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read frame length: %w", err)
		}

		// Read the message body.
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("read frame body: %w", err)
		}

		// Unmarshal as GeoSite and check code case-insensitively.
		var site geodata.GeoSite
		if err := proto.Unmarshal(body, &site); err != nil {
			// Skip entries we can't parse.
			continue
		}

		if !strings.EqualFold(site.Code, baseTag) {
			continue
		}

		// Found matching entry — cache and return.
		domains := site.Domain
		geositeCache.mu.Lock()
		geositeCache.data[cacheKey] = geositeCacheEntry{domains: domains, found: true}
		geositeCache.mu.Unlock()
		return domains, nil
	}

	// Tag not found — cache negative lookup to avoid re-scanning.
	geositeCache.mu.Lock()
	geositeCache.data[cacheKey] = geositeCacheEntry{found: false}
	geositeCache.mu.Unlock()

	return nil, fmt.Errorf("tag %q not found in %s", baseTag, filePath)
}
