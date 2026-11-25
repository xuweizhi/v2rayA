package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// backupKeep is the number of automatic backups to retain.
const backupKeep = 5

// backupMinInterval is the minimum interval between two automatic backups.
const backupMinInterval = 10 * time.Minute

type BackupItem struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

var backupMu sync.Mutex
var lastAutoBackup time.Time

// backupDir returns the directory that stores backups.
func backupDir() string {
	return filepath.Join(conf.GetEnvironmentConfig().Config, "backups")
}

// CreateBackup snapshots the current database into the backups directory and
// prunes old backups beyond the retention limit.
func CreateBackup() (item BackupItem, err error) {
	backupMu.Lock()
	defer backupMu.Unlock()

	dir := backupDir()
	if err = os.MkdirAll(dir, os.ModeDir|0750); err != nil {
		return item, fmt.Errorf("failed to create backup directory: %w", err)
	}
	name := "v2raya-backup-" + time.Now().Format("20060102-150405.000000") + ".db"
	path := filepath.Join(dir, name)
	// Reserve the destination with private permissions before SQLite opens
	// it. VACUUM INTO accepts an existing empty file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return item, err
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(path)
		return item, err
	}
	if _, err = db.GetDB().Exec("VACUUM INTO ?", path); err != nil {
		_ = os.Remove(path)
		return item, fmt.Errorf("failed to backup database: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return item, err
	}
	item = BackupItem{Name: name, Size: info.Size(), ModTime: info.ModTime().Unix()}
	pruneBackups()
	return item, nil
}

// pruneBackups removes the oldest automatic backups beyond backupKeep.
func pruneBackups() {
	items, err := ListBackups()
	if err != nil {
		return
	}
	if len(items) <= backupKeep {
		return
	}
	for _, item := range items[backupKeep:] {
		_ = os.Remove(filepath.Join(backupDir(), item.Name))
		log.Info("pruned old backup %v", item.Name)
	}
}

// AutoBackup creates a debounced backup after configuration changes.
func AutoBackup() {
	if !configure.GetSettingNotNil().AutoBackup {
		return
	}
	backupMu.Lock()
	if time.Since(lastAutoBackup) < backupMinInterval {
		backupMu.Unlock()
		return
	}
	lastAutoBackup = time.Now()
	backupMu.Unlock()
	go func() {
		if _, err := CreateBackup(); err != nil {
			log.Warn("AutoBackup: %v", err)
		}
	}()
}

// ListBackups returns the available backups ordered by time descending.
func ListBackups() (items []BackupItem, err error) {
	dir := backupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupItem{}, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, BackupItem{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime > items[j].ModTime })
	return items, nil
}

func isValidBackupName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00\r\n") {
		return false
	}
	return true
}

// OpenBackup opens a regular file inside the backup directory. Keep using the
// checked handle for the entire download or restore, even if the name changes.
func OpenBackup(name string) (*os.File, error) {
	if !isValidBackupName(name) {
		return nil, fmt.Errorf("invalid backup name")
	}
	root, err := os.OpenRoot(backupDir())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid backup file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil || !os.SameFile(info, stat) {
		f.Close()
		return nil, fmt.Errorf("backup changed while opening")
	}
	return f, nil
}

// BackupPath returns the path of a current regular backup. Consumers that
// need its contents should use OpenBackup to retain the checked descriptor.
func BackupPath(name string) (string, error) {
	file, err := OpenBackup(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(backupDir(), name), file.Close()
}
