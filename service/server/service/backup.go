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
	// VACUUM INTO refuses to overwrite an existing target file.
	_ = os.Remove(path)
	// VACUUM INTO does not support bound parameters; escape the single
	// quotes of the (user-configurable) path ourselves.
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err = db.GetDB().Exec(`VACUUM INTO '` + escaped + `'`); err != nil {
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
		if entry.IsDir() {
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

// BackupPath returns the full path of a backup by name, guarding against
// path traversal.
func BackupPath(name string) (string, error) {
	if filepath.Base(name) != name {
		return "", fmt.Errorf("invalid backup name")
	}
	path := filepath.Join(backupDir(), name)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
