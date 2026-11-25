package parseGeoIP

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/v2rayA/v2rayA/kernel/v2ray/asset"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type parserResult struct {
	ipv4 []string
	ipv6 []string
}

type parserCacheKey struct {
	path        string
	size        int64
	modTime     int64
	countryCode string
}

var (
	parserCache   = make(map[parserCacheKey]parserResult)
	parserCacheMu sync.Mutex
	fakeIPv4Net   = mustParseCIDR("198.18.0.0/15")
	fakeIPv6Net   = mustParseCIDR("fc00::/7")
)

func mustParseCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return ipnet
}

func Parser(filename string, countryCode string) ([]string, []string, error) {
	realpath, err := asset.GetV2rayLocationAsset(filename)
	if err != nil {
		return nil, nil, err
	}
	// Serialize misses, including stat, so a waiter cannot restore an old generation.
	parserCacheMu.Lock()
	defer parserCacheMu.Unlock()
	info, err := os.Stat(realpath)
	if err != nil {
		return nil, nil, err
	}

	cacheKey := parserCacheKey{
		path:        realpath,
		size:        info.Size(),
		modTime:     info.ModTime().UnixNano(),
		countryCode: strings.ToUpper(countryCode),
	}
	if result, ok := parserCache[cacheKey]; ok {
		return result.ipv4, result.ipv6, nil
	}
	for key := range parserCache {
		if key.path == realpath && (key.size != cacheKey.size || key.modTime != cacheKey.modTime) {
			delete(parserCache, key)
		}
	}

	data, err := os.ReadFile(realpath)
	if err != nil {
		return nil, nil, err
	}

	ipv4List, ipv6List, err := parseGeoIP(data, countryCode)
	if err != nil {
		return ipv4List, ipv6List, err
	}

	parserCache[cacheKey] = parserResult{ipv4: ipv4List, ipv6: ipv6List}
	return ipv4List, ipv6List, nil
}

func parseGeoIP(data []byte, countryCode string) ([]string, []string, error) {
	var ipv4List []string
	var ipv6List []string

	// GeoIPList contains repeated GeoIP messages in field 1. Decoding the
	// complete list expands a roughly 22 MiB asset to hundreds of MiB in
	// memory. Stop after decoding the requested country.
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return ipv4List, ipv6List, protowire.ParseError(n)
		}
		data = data[n:]

		if number == 1 && wireType == protowire.BytesType {
			entryData, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return ipv4List, ipv6List, protowire.ParseError(consumed)
			}
			data = data[consumed:]

			var geo GeoIP
			if err := proto.Unmarshal(entryData, &geo); err != nil {
				return ipv4List, ipv6List, err
			}
			if !strings.EqualFold(geo.CountryCode, countryCode) {
				continue
			}

			for _, c := range geo.Cidr {
				ip := net.IP(c.Ip)
				if ip4 := ip.To4(); ip4 != nil {
					if fakeIPv4Net.Contains(ip4) {
						// 跳过 fakeip 网段 (198.18.0.0/15)
						continue
					}
					ipv4List = append(ipv4List, fmt.Sprintf("%s/%d", ip4.String(), c.Prefix))
				} else if len(ip) == net.IPv6len {
					if fakeIPv6Net.Contains(ip) {
						// 跳过 fakeip 网段 (fc00::/7)
						continue
					}
					ipv6List = append(ipv6List, fmt.Sprintf("%s/%d", ip.String(), c.Prefix))
				}
			}
			return ipv4List, ipv6List, nil
		}

		consumed := protowire.ConsumeFieldValue(number, wireType, data)
		if consumed < 0 {
			return ipv4List, ipv6List, protowire.ParseError(consumed)
		}
		data = data[consumed:]
	}

	return ipv4List, ipv6List, nil
}
