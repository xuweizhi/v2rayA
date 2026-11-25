package service

import (
	"bytes"
	"context"
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
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
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

	target, err := url.Parse(w.baseURL)
	if err != nil {
		return nil, err
	}
	if remotePath != "" && !isValidBackupName(remotePath) {
		return nil, fmt.Errorf("invalid backup name")
	}
	escaped := strings.TrimRight(target.EscapedPath(), "/") + "/" + url.PathEscape(remotePath)
	target.Path, err = url.PathUnescape(escaped)
	if err != nil {
		return nil, err
	}
	target.RawPath = escaped
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
		req, err := http.NewRequest(method, target.String(), body)
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
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:displayname/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`)
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
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Length   string `xml:"getcontentlength"`
				Modified string `xml:"getlastmodified"`
			} `xml:"propstat>prop"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(raw, &multi); err != nil {
		return nil, fmt.Errorf("webdav PROPFIND parse: %w", err)
	}
	basePath := ""
	if baseURL, err := url.Parse(w.baseURL); err == nil {
		basePath = strings.TrimRight(path.Clean("/"+baseURL.Path), "/")
	}
	var items []WebdavItem
	for _, r := range multi.Responses {
		rawHref := strings.TrimSpace(r.Href)
		if rawHref == "" || strings.HasSuffix(rawHref, "/") {
			continue
		}
		isCollection := false
		for _, prop := range r.Props {
			if prop.ResourceType.Collection != nil {
				isCollection = true
				break
			}
		}
		if isCollection {
			continue
		}
		hrefPath := rawHref
		if hrefURL, err := url.Parse(rawHref); err == nil {
			hrefPath = hrefURL.Path
		}
		hrefPath = strings.TrimRight(path.Clean("/"+hrefPath), "/")
		if hrefPath == "" || hrefPath == "/" || hrefPath == basePath {
			continue
		}
		name := path.Base(hrefPath)
		if !isValidBackupName(name) {
			continue
		}
		item := WebdavItem{Name: name}
		for _, prop := range r.Props {
			if item.Size == 0 && prop.Length != "" {
				fmt.Sscanf(prop.Length, "%d", &item.Size)
			}
			if item.ModTime == 0 && prop.Modified != "" {
				if t, err := time.Parse(time.RFC1123, prop.Modified); err == nil {
					item.ModTime = t.Unix()
				} else if t, err := time.Parse(time.RFC1123Z, prop.Modified); err == nil {
					item.ModTime = t.Unix()
				}
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
	resp, err := w.do(http.MethodPut, item.Name, func() (io.ReadCloser, error) {
		return OpenBackup(item.Name)
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

// writeBackupFile publishes a private, fully copied file. Existing files may
// have permissions left by an older version, so create a new inode in all cases.
func writeBackupFile(dst string, src io.Reader) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".v2raya-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, copyErr := io.Copy(f, src)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}

func DownloadWebdavBackup(name, dst string) error {
	resp, err := OpenWebdavBackupStream(name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return writeBackupFile(dst, resp.Body)
}

// OpenWebdavBackupStream validates name, confirms HTTP 200 from WebDAV, and
// returns the response for the caller to stream and close.
func OpenWebdavBackupStream(name string) (*http.Response, error) {
	if !isValidBackupName(name) {
		return nil, fmt.Errorf("invalid backup name")
	}
	c, err := newWebdavClient()
	if err != nil {
		return nil, err
	}
	resp, err := c.do(http.MethodGet, name, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("webdav GET failed: status %d", resp.StatusCode)
	}
	return resp, nil
}

// OpenWebdavBackup streams a remote backup file to an http.ResponseWriter.
func OpenWebdavBackup(name string, w io.Writer) error {
	resp, err := OpenWebdavBackupStream(name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// ---- restore ----

var restoreMu sync.Mutex

var (
	resolveRestoreExecutable = func() (string, error) {
		if exe, err := os.Executable(); err == nil {
			return exe, nil
		}
		if len(os.Args) == 0 || os.Args[0] == "" {
			return "", fmt.Errorf("cannot determine executable path")
		}
		return exec.LookPath(os.Args[0])
	}
	execRestoreProcess = syscall.Exec
	restoreExecDelay   = 500 * time.Millisecond
)

// RestoreFromBackup replaces the database with a backup and restarts the
// service process. source is "local" (name is a local backup file) or
// "webdav" (name is a remote file).
func RestoreFromBackup(source, name string) error {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if source != "local" && source != "webdav" {
		return fmt.Errorf("invalid backup source")
	}
	if !isValidBackupName(name) {
		return fmt.Errorf("invalid backup name")
	}
	exePath, err := resolveRestoreExecutable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	configDir := conf.GetEnvironmentConfig().Config
	staging := filepath.Join(configDir, "restore-staging.db")
	marker := filepath.Join(configDir, "restore-pending.json")
	if _, err := os.Stat(marker); err == nil {
		return fmt.Errorf("a restore is already pending")
	} else if !os.IsNotExist(err) {
		return err
	}
	if source == "webdav" {
		if err := DownloadWebdavBackup(name, staging); err != nil {
			return fmt.Errorf("download backup: %w", err)
		}
	} else {
		src, err := OpenBackup(name)
		if err != nil {
			return fmt.Errorf("backup not found: %w", err)
		}
		defer src.Close()
		if err := writeBackupFile(staging, src); err != nil {
			return err
		}
	}
	// Validate the completed staging file, not just its source header.
	if err := validateRestoreDatabase(staging); err != nil {
		_ = os.Remove(staging)
		return err
	}
	// Persist the restore intent and restart the process. The database file
	// is swapped during the next startup (before the DB is opened).
	markerData, err := json.Marshal(struct {
		Staging string `json:"staging"`
	}{staging})
	if err != nil {
		_ = os.Remove(staging)
		return err
	}
	if err := os.WriteFile(marker, markerData, 0o600); err != nil {
		_ = os.Remove(staging)
		return err
	}
	if runtime.GOOS == "windows" {
		// syscall.Exec is unavailable on Windows. Keep serving until the
		// operator restarts the process or its registered Windows service.
		return nil
	}
	go func() {
		time.Sleep(restoreExecDelay)
		membershipMu.Lock()
		defer membershipMu.Unlock()
		previous := v2ray.ProcessManager.Process()
		v2ray.ProcessManager.Stop(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := previous.WaitUntilExit(ctx)
		cancel()
		if err == nil {
			err = db.Close()
		}
		if err == nil {
			err = execRestoreProcess(exePath, os.Args, os.Environ())
		}
		if err != nil {
			restoreMu.Lock()
			defer restoreMu.Unlock()
			err = errors.Join(err, os.Remove(staging), os.Remove(marker))
			if previous != nil {
				err = errors.Join(err, v2ray.UpdateV2RayConfig())
			}
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

func sqliteFileURI(filename, rawQuery string) (string, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") { // Windows drive letter
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed, RawQuery: rawQuery}
	return u.String(), nil
}

func validateRestoreDatabase(filename string) error {
	// Backups are standalone snapshots; never read or mutate sidecar files.
	uri, err := sqliteFileURI(filename, "mode=ro&immutable=1")
	if err != nil {
		return err
	}
	database, err := db.OpenStandalone(uri)
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
	for _, table := range []string{"system_config", "servers", "subscriptions", "accounts", "outbound_names"} {
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
