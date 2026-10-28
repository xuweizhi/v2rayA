package service

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"sync"

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
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// ---- minimal WebDAV client ----

type webdavClient struct {
	baseURL  string
	username string
	password string
	clients  []*http.Client
}

type webdavBodyFactory func() (io.ReadCloser, error)

func newWebdavClient() (*webdavClient, error) {
	setting := configure.GetSettingNotNil()
	if setting.WebdavUrl == "" {
		return nil, fmt.Errorf("webdav is not configured")
	}
	clients, err := httpClient.GetHttpClientsForWebdav(setting.WebdavConnectionMode)
	if err != nil {
		return nil, err
	}
	for i, client := range clients {
		// Do not mutate http.DefaultClient or a shared subscription client.
		clone := *client
		clone.Timeout = 60 * time.Second
		clients[i] = &clone
	}
	return &webdavClient{
		baseURL:  strings.TrimSuffix(setting.WebdavUrl, "/"),
		username: setting.WebdavUsername,
		password: setting.WebdavPassword,
		clients:  clients,
	}, nil
}

func (w *webdavClient) do(method, remotePath string, bodyFactory webdavBodyFactory, headers http.Header) (*http.Response, error) {
	if len(w.clients) == 0 {
		return nil, fmt.Errorf("no WebDAV HTTP client configured")
	}

	var lastErr error
	for _, client := range w.clients {
		var body io.ReadCloser
		var err error
		if bodyFactory != nil {
			body, err = bodyFactory()
			if err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequest(method, w.baseURL+"/"+strings.TrimPrefix(remotePath, "/"), body)
		if err != nil {
			if body != nil {
				_ = body.Close()
			}
			return nil, err
		}
		for key, values := range headers {
			req.Header[key] = append([]string(nil), values...)
		}
		if w.username != "" {
			req.SetBasicAuth(w.username, w.password)
		}

		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}
		if body != nil {
			_ = body.Close()
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		lastErr = err
	}
	return nil, fmt.Errorf("webdav request failed through all configured routes: %w", lastErr)
}

// list returns the file names in the remote backup directory (depth 1).
func (w *webdavClient) list() ([]WebdavItem, error) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`)
	resp, err := w.do("PROPFIND", "", func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}, http.Header{
		"Depth":        []string{"1"},
		"Content-Type": []string{"application/xml"},
	})
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
	resp, err := w.do(http.MethodPut, item.Name, func() (io.ReadCloser, error) {
		return os.Open(local)
	}, nil)
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
	resp, err := w.do(http.MethodGet, name, nil, nil)
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
	resp, err := c.do(http.MethodGet, name, nil, nil)
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

var restoreMu sync.Mutex

// RestoreFromBackup replaces the database with a backup and restarts the
// service process. source is "local" (name is a local backup file) or
// "webdav" (name is a remote file).
func RestoreFromBackup(source, name string) error {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if source != "local" && source != "webdav" {
		return fmt.Errorf("invalid backup source")
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("invalid backup name")
	}
	configDir := conf.GetEnvironmentConfig().Config
	staging := filepath.Join(configDir, "restore-staging.db")
	marker := filepath.Join(configDir, "restore-pending.json")
	if _, err := os.Stat(marker); err == nil {
		return fmt.Errorf("a restore is already pending")
	} else if !os.IsNotExist(err) {
		return err
	}
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
	if err := validateRestoreDatabase(local); err != nil {
		return err
	}
	if source == "local" {
		// Copy local backup to staging so the pending marker can reference a
		// stable path.
		src, err := os.Open(local)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(staging, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if _, err = io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
	}
	// Validate the completed staging file, not just its source header.
	if err := validateRestoreDatabase(staging); err != nil {
		return err
	}
	// Persist the restore intent and restart the process. The database file
	// is swapped during the next startup (before the DB is opened).
	markerData, err := json.Marshal(struct {
		Staging string `json:"staging"`
	}{staging})
	if err != nil {
		return err
	}
	if err := os.WriteFile(marker, markerData, 0o600); err != nil {
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
	return applyPendingRestore(conf.GetEnvironmentConfig().Config)
}

func validateRestoreDatabase(filename string) error {
	// Backups are standalone snapshots; never read or mutate sidecar files.
	u := &url.URL{Scheme: "file", Path: filename, RawQuery: "mode=ro&immutable=1"}
	database, err := db.OpenStandalone(u.String())
	if err != nil {
		return err
	}
	defer database.Close()
	var check string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return fmt.Errorf("invalid backup database: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", check)
	}
	for _, table := range []string{"system_config", "servers", "subscriptions", "accounts", "outbound_names", "outbound_settings", "outbound_connections"} {
		var count int
		if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("backup is missing table %s", table)
		}
	}
	return nil
}

func applyPendingRestore(configDir string) error {
	return applyPendingRestoreFiles(configDir, os.Rename)
}

func applyPendingRestoreFiles(configDir string, rename func(string, string) error) (err error) {
	marker := filepath.Join(configDir, "restore-pending.json")
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var pending struct {
		Staging string `json:"staging"`
	}
	if err := jsonUnmarshal(data, &pending); err != nil {
		return fmt.Errorf("invalid restore marker: %w", err)
	}
	if filepath.Clean(pending.Staging) != filepath.Join(configDir, "restore-staging.db") {
		return fmt.Errorf("invalid restore staging path")
	}
	if err := validateRestoreDatabase(pending.Staging); err != nil {
		return err
	}
	dbPath := filepath.Join(configDir, "v2raya.db")
	safety := dbPath + ".pre-restore-" + time.Now().Format("20060102-150405.000000000")
	// Keep the original file at dbPath until the final rename succeeds. Preserve
	// WAL alongside the safety copy: committed data may not be in the main file yet.
	if src, openErr := os.Open(dbPath); openErr == nil {
		dst, err := os.OpenFile(safety, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		src.Close()
		closeErr := dst.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	} else if !os.IsNotExist(openErr) {
		return openErr
	}
	moved := []string{}
	defer func() {
		if err == nil {
			return
		}
		for _, suffix := range moved {
			err = errors.Join(err, rename(safety+suffix, dbPath+suffix))
		}
	}()
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := rename(dbPath+suffix, safety+suffix); err == nil {
			moved = append(moved, suffix)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := rename(pending.Staging, dbPath); err != nil {
		return fmt.Errorf("failed to apply restored database: %w", err)
	}
	// The database swap has committed; never restore old WAL onto the new DB.
	moved = nil
	if err := os.Remove(marker); err != nil {
		return err
	}
	log.Info("restored database; previous database preserved at %v", safety)
	return nil
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return jsoniter.Unmarshal(data, v)
}
