package configure

import (
	jsoniter "github.com/json-iterator/go"
	"github.com/tidwall/gjson"
	"github.com/v2rayA/v2rayA/kernel/serverObj"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

type ServerRaw struct {
	ServerObj serverObj.ServerObj `json:"serverObj"`
	Latency   string              `json:"latency"`
	Disabled  bool                `json:"disabled,omitempty"`
	Fav       bool                `json:"fav,omitempty"`
}

// SubscriptionExtra holds per-subscription advanced settings that are stored
// in a single JSON column of the subscriptions table.
type SubscriptionExtra struct {
	// UserAgent is the User-Agent used when downloading the subscription.
	// Empty means the default clash-compatible UA.
	UserAgent string `json:"userAgent,omitempty"`
	// UserAgentAppend appends the v2rayA UA to the configured User-Agent.
	UserAgentAppend bool `json:"userAgentAppend,omitempty"`
	// XHWID is the value of the X-HWID request header (device binding).
	XHWID string `json:"xhwid,omitempty"`
	// DownloadStrategy controls how the subscription is downloaded:
	// "" (follow system), preferProxy, preferDirect, onlyProxy, onlyDirect.
	DownloadStrategy string `json:"downloadStrategy,omitempty"`
	// IncludeRegex keeps only nodes whose name matches this regular expression.
	IncludeRegex string `json:"includeRegex,omitempty"`
	// ExcludeRegex drops nodes whose name matches this regular expression.
	ExcludeRegex string `json:"excludeRegex,omitempty"`
	// UpdateIntervalHour is the per-subscription auto-update interval in hours.
	// 0 means follow the global setting.
	UpdateIntervalHour int `json:"updateIntervalHour,omitempty"`
	// LastUpdateTime is the unix timestamp of the last successful update.
	LastUpdateTime int64 `json:"lastUpdateTime,omitempty"`
	// DisabledTags are node names that the user disabled.
	DisabledTags []string `json:"disabledTags,omitempty"`
	// RemovedTags are node names deleted by the user; they stay removed
	// across subscription updates.
	RemovedTags []string `json:"removedTags,omitempty"`
	// FavTags are node names marked as favorite.
	FavTags []string `json:"favTags,omitempty"`
	// Upload/Download/Total/Expire are parsed from the
	// subscription-userinfo response header.
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"`
}

type SubscriptionRaw struct {
	Remarks         string            `json:"remarks,omitempty"`
	Address         string            `json:"address"`
	Status          string            `json:"status"` //update time, error info, etc.
	Servers         []ServerRaw       `json:"servers"`
	Info            string            `json:"info"` // maybe include some info from provider
	AutoSelect      bool              `json:"autoSelect"`
	DecryptPassword string            `json:"decryptPassword,omitempty"`
	Extra           SubscriptionExtra `json:"extra,omitempty"`
}

func Bytes2SubscriptionRaw(b []byte) (*SubscriptionRaw, error) {
	var s SubscriptionRaw
	rawList := gjson.GetBytes(b, "servers").Array()
	for _, raw := range rawList {
		var obj serverObj.ServerObj
		obj, err := serverObj.New(raw.Get("serverObj.protocol").String())
		if err != nil {
			return nil, err
		}
		s.Servers = append(s.Servers, ServerRaw{ServerObj: obj})
	}
	if err := jsoniter.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Servers == nil {
		s.Servers = []ServerRaw{}
	}
	return &s, nil
}

func Bytes2ServerRaw(b []byte) (*ServerRaw, error) {
	var s ServerRaw
	var obj serverObj.ServerObj
	protocol := gjson.GetBytes(b, "serverObj.protocol").String()
	if protocol == "" {
		log.Warn("empty protocol, fallback to vmess: %v", gjson.GetBytes(b, "serverObj.ps").String())
		protocol = "vmess"
	}
	obj, err := serverObj.New(protocol)
	if err != nil {
		return nil, err
	}
	s.ServerObj = obj
	if err := jsoniter.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
