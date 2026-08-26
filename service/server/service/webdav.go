package service

import (
	"bytes"
	"encoding/xml"
	"fmt"

	jsoniter "github.com/json-iterator/go"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/v2rayA/v2rayA/common/httpClient"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// ---- minimal WebDAV client ----

type webdavClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func newWebdavClient() (*webdavClient, error) {
	setting := configure.GetSettingNotNil()
	if setting.WebdavUrl == "" {
		return nil, fmt.Errorf("webdav is not configured")
	}
	c := httpClient.GetHttpClientAutomatically()
	c.Timeout = 60 * time.Second
	return &webdavClient{
		baseURL:  strings.TrimSuffix(setting.WebdavUrl, "/"),
		username: setting.WebdavUsername,
		password: setting.WebdavPassword,
		client:   c,
	}, nil
}

func (w *webdavClient) do(method, remotePath string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, w.baseURL+"/"+strings.TrimPrefix(remotePath, "/"), body)
	if err != nil {
		return nil, err
	}
	if w.username != "" {
		req.SetBasicAuth(w.username, w.password)
	}
	return w.client.Do(req)
}

// list returns the file names in the remote backup directory (depth 1).
func (w *webdavClient) list() ([]WebdavItem, error) {
	body := bytes.NewBufferString(`<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`)
	req, err := http.NewRequest("PROPFIND", w.baseURL+"/", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")
	if w.username != "" {
		req.SetBasicAuth(w.username, w.password)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 {
		return nil, fmt.Errorf("webdav PROPFIND failed: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var multi struct {
		Responses []struct {
			Href  string `xml:"href"`
			Props []struct {
				Length   string `xml:"getcontentlength"`
				Modified string `xml:"getlastmodified"`
			} `xml:"propstat>prop"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(raw, &multi); err != nil {
		return nil, fmt.Errorf("webdav PROPFIND parse: %w", err)
	}
	var items []WebdavItem
	for _, r := range multi.Responses {
		name := path.Base(strings.TrimSuffix(strings.TrimSpace(r.Href), "/"))
		if name == "" || name == "." || name == "/" {
			continue
		}
		item := WebdavItem{Name: name}
		if len(r.Props) > 0 {
			fmt.Sscanf(r.Props[0].Length, "%d", &item.Size)
			if t, err := time.Parse(time.RFC1123, r.Props[0].Modified); err == nil {
				item.ModTime = t.Unix()
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// WebdavItem is one remote backup file.
type WebdavItem struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// ListWebdavBackups lists the remote backup files.
func ListWebdavBackups() ([]WebdavItem, error) {
	w, err := newWebdavClient()
	if err != nil {
		return nil, err
	}
	items, err := w.list()
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []WebdavItem{}
	}
	return items, nil
}

// UploadLatestBackup creates a fresh local backup and uploads it to the
// configured WebDAV directory.
func UploadLatestBackup() (WebdavItem, error) {
	item, err := CreateBackup()
	if err != nil {
		return WebdavItem{}, err
	}
	w, err := newWebdavClient()
	if err != nil {
		return WebdavItem{}, err
	}
	local, err := BackupPath(item.Name)
	if err != nil {
		return WebdavItem{}, err
	}
	f, err := os.Open(local)
	if err != nil {
		return WebdavItem{}, err
	}
	defer f.Close()
	resp, err := w.do(http.MethodPut, item.Name, f)
	if err != nil {
		return WebdavItem{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return WebdavItem{}, fmt.Errorf("webdav PUT failed: status %d", resp.StatusCode)
	}
	return WebdavItem{Name: item.Name, Size: item.Size, ModTime: item.ModTime}, nil
}

// DownloadWebdavBackup streams a remote backup file to dst.
func DownloadWebdavBackup(name string, dst string) error {
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid backup name")
	}
	w, err := newWebdavClient()
	if err != nil {
		return err
	}
	resp, err := w.do(http.MethodGet, name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav GET failed: status %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// OpenWebdavBackup streams a remote backup file to an http.ResponseWriter.
func OpenWebdavBackup(name string, w io.Writer) error {
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid backup name")
	}
	c, err := newWebdavClient()
	if err != nil {
		return err
	}
	resp, err := c.do(http.MethodGet, name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav GET failed: status %d", resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// ---- restore ----

// RestoreFromBackup replaces the database with a backup and restarts the
// service process. source is "local" (name is a local backup file) or
// "webdav" (name is a remote file).
func RestoreFromBackup(source, name string) error {
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid backup name")
	}
	configDir := conf.GetEnvironmentConfig().Config
	staging := filepath.Join(configDir, "restore-staging.db")
	var local string
	if source == "webdav" {
		if err := DownloadWebdavBackup(name, staging); err != nil {
			return fmt.Errorf("download backup: %w", err)
		}
		local = staging
	} else {
		p, err := BackupPath(name)
		if err != nil {
			return fmt.Errorf("backup not found: %w", err)
		}
		local = p
	}
	// Validate that the target is an SQLite database.
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	head := make([]byte, 16)
	_, _ = f.Read(head)
	f.Close()
	if !strings.HasPrefix(string(head), "SQLite format 3") {
		return fmt.Errorf("backup file is not a valid v2rayA database")
	}
	if source == "local" {
		// Copy local backup to staging so the pending marker can reference a
		// stable path.
		src, err := os.Open(local)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.Create(staging)
		if err != nil {
			return err
		}
		if _, err = io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		dst.Close()
	}
	// Persist the restore intent and restart the process. The database file
	// is swapped during the next startup (before the DB is opened).
	if err := os.WriteFile(filepath.Join(configDir, "restore-pending.json"),
		[]byte(`{"staging":"`+staging+`"}`), 0o600); err != nil {
		return err
	}
	v2ray.ProcessManager.Stop(false)
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := syscall.Exec(os.Args[0], os.Args, os.Environ()); err != nil {
			log.Error("RestoreFromBackup: re-exec failed: %v", err)
		}
	}()
	return nil
}

// ApplyPendingRestore swaps in the staged database during startup. It is
// called before the database is opened.
func ApplyPendingRestore() error {
	configDir := conf.GetEnvironmentConfig().Config
	marker := filepath.Join(configDir, "restore-pending.json")
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil // no pending restore
	}
	var m struct {
		Staging string `json:"staging"`
	}
	_ = jsonUnmarshal(data, &m)
	_ = os.Remove(marker)
	if m.Staging == "" {
		return nil
	}
	if _, err := os.Stat(m.Staging); err != nil {
		log.Warn("restore staging file missing: %v", m.Staging)
		return nil
	}
	// Keep the current database as a safety copy before swapping.
	dbPath := filepath.Join(configDir, "v2raya.db")
	if _, err := os.Stat(dbPath); err == nil {
		safety := filepath.Join(configDir, "v2raya.db.pre-restore-"+time.Now().Format("20060102-150405"))
		if err := os.Rename(dbPath, safety); err != nil {
			return fmt.Errorf("failed to preserve current database: %w", err)
		}
		log.Info("preserved current database as %v", safety)
	}
	if err := os.Rename(m.Staging, dbPath); err != nil {
		return fmt.Errorf("failed to apply restored database: %w", err)
	}
	log.Info("restored database from backup")
	return nil
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return jsoniter.Unmarshal(data, v)
}
