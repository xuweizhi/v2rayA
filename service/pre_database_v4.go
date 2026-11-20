package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	jsoniter "github.com/json-iterator/go"
	"github.com/tidwall/gjson"
	v4 "github.com/v2rayA/v2rayA-lib4/core/serverObj"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"go.etcd.io/bbolt"
)

// Read the v4 snapshot without invoking its database singleton, which parses
// the process's command line again and opens boltv4.db for writing.
func readV4ImportLinks(dir string) ([]string, error) {
	source, err := bbolt.Open(filepath.Join(dir, "boltv4.db"), 0600, &bbolt.Options{ReadOnly: true})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer source.Close()

	var links []string
	err = source.View(func(tx *bbolt.Tx) error {
		system := tx.Bucket([]byte("system"))
		touch := tx.Bucket([]byte("touch"))
		if system == nil || system.Stats().KeyN == 0 || touch == nil {
			return nil
		}
		subscriptions, err := v4List(touch, "subscriptions_v2")
		if err != nil {
			return err
		}
		for _, raw := range subscriptions {
			if address := gjson.GetBytes(raw, "address"); address.Type == gjson.String && address.Str != "" {
				links = append(links, address.Str)
			}
		}
		servers, err := v4List(touch, "servers_v2")
		if err != nil {
			return err
		}
		for i, raw := range servers {
			link, err := v4ServerLink(raw)
			if err != nil {
				log.Warn("Skipping v4 server %d: %v", i+1, err)
				continue
			}
			links = append(links, link)
		}
		return nil
	})
	return links, err
}

func v4List(bucket *bbolt.Bucket, key string) ([]json.RawMessage, error) {
	data := bucket.Get([]byte(key))
	if data == nil {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	return rows, nil
}

func v4ServerLink(raw json.RawMessage) (string, error) {
	object := gjson.GetBytes(raw, "serverObj")
	protocol := object.Get("protocol").String()
	// Use the legacy wire types directly. The v4 factory consults its CLI
	// parser for unknown protocols, including custom plugin records.
	var server v4.ServerObj
	switch protocol {
	case "", "vmess", "vless":
		server = &v4.V2Ray{Protocol: "vmess"}
	case "ss", "shadowsocks":
		server = &v4.Shadowsocks{}
	case "ssr", "shadowsocksr":
		server = &v4.ShadowsocksR{}
	case "trojan", "trojan-go":
		server = &v4.Trojan{}
	case "socks5":
		server = &v4.SOCKS{}
	case "http", "https", "http-proxy", "https-proxy":
		server = &v4.HTTP{}
	case "ping-tunnel", "pingtunnel":
		server = &v4.PingTunnel{}
	default:
		// The v4 plugin manager stores its original share link in pm_link.
		if link := object.Get("pm_link").String(); link != "" {
			return link, nil
		}
		return "", fmt.Errorf("unsupported v4 protocol %q", protocol)
	}
	if err := jsoniter.UnmarshalFromString(object.Raw, server); err != nil {
		return "", err
	}
	if link := server.ExportToURL(); link != "" {
		return link, nil
	}
	return "", fmt.Errorf("missing share link for v4 protocol %q", protocol)
}
