package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// NoticeURL is the remote notice feed.
const NoticeURL = "https://raw.githubusercontent.com/xuweizhi/v2rayA/main/notice.json"

// noticeRefreshInterval is how often the notice feed is polled.
const noticeRefreshInterval = 3 * time.Hour

// Notice is one announcement entry.
type Notice struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	URL        string `json:"url,omitempty"`
	UpdateTime int64  `json:"updateTime"`
	ExpireTime int64  `json:"expireTime,omitempty"`
}

type noticeFeed struct {
	Notices []Notice `json:"notices"`
}

// GetNotices returns the stored, unexpired notices.
func GetNotices() []Notice {
	var notices []Notice
	_ = db.Get("system", "notices", &notices)
	notices = filterExpiredNotices(notices)
	return notices
}

// GetReadNotices returns the IDs the user has read.
func GetReadNotices() []string {
	var read []string
	_ = db.Get("system", "noticesRead", &read)
	return read
}

// MarkNoticesRead marks the given notice IDs as read.
func MarkNoticesRead(ids []string) error {
	read := make(map[string]bool)
	for _, id := range GetReadNotices() {
		read[id] = true
	}
	for _, id := range ids {
		read[id] = true
	}
	all := make([]string, 0, len(read))
	for id := range read {
		all = append(all, id)
	}
	return db.Set("system", "noticesRead", all)
}

// RefreshNotices downloads the remote notice feed, merges it into the local
// store and prunes expired entries.
func RefreshNotices() error {
	c := httpClient.GetHttpClientAutomatically()
	c.Timeout = 30 * time.Second
	res, err := httpClient.HttpGetUsingSpecificClientWithUA(c, NoticeURL, "v2rayA/"+confVersion())
	if err != nil {
		return fmt.Errorf("fetch notices: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch notices: status %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	var feed noticeFeed
	if err = json.Unmarshal(b, &feed); err != nil {
		return fmt.Errorf("parse notices: %w", err)
	}
	if len(feed.Notices) == 0 {
		return nil
	}
	// Merge by ID, newest updateTime wins.
	byID := make(map[string]Notice)
	for _, n := range GetNotices() {
		byID[n.ID] = n
	}
	for _, n := range feed.Notices {
		if n.ID == "" {
			continue
		}
		if old, ok := byID[n.ID]; !ok || n.UpdateTime > old.UpdateTime {
			byID[n.ID] = n
		}
	}
	merged := make([]Notice, 0, len(byID))
	for _, n := range byID {
		merged = append(merged, n)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].UpdateTime > merged[j].UpdateTime })
	merged = filterExpiredNotices(merged)
	return db.Set("system", "notices", merged)
}

// filterExpiredNotices drops expired notices. For feeds that do not supply an
// explicit expiry, retain the former 30-day freshness safeguard.
func filterExpiredNotices(notices []Notice) []Notice {
	return filterNoticesAt(notices, time.Now().Unix())
}

func filterNoticesAt(notices []Notice, now int64) []Notice {
	out := make([]Notice, 0, len(notices))
	for _, n := range notices {
		if n.ExpireTime > 0 {
			if n.ExpireTime < now {
				continue
			}
		} else if n.UpdateTime < now-30*86400 {
			continue
		}
		out = append(out, n)
	}
	return out
}

// StartNoticeRefresher starts the periodic notice feed poller.
func StartNoticeRefresher() {
	go func() {
		if err := RefreshNotices(); err != nil {
			log.Trace("notice refresh: %v", err)
		}
		ticker := time.NewTicker(noticeRefreshInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := RefreshNotices(); err != nil {
				log.Trace("notice refresh: %v", err)
			}
		}
	}()
}

func confVersion() string {
	return conf.Version
}
