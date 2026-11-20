//go:build linux

package v2ray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"golang.org/x/sys/unix"
)

const (
	HijackFlag     = "# v2rayA DNS hijack"
	hijackedResolv = HijackFlag + "\nnameserver 127.2.0.17\nnameserver 119.29.29.29\n"
	checkInterval  = 3 * time.Second
)

var (
	resolvPath = "/etc/resolv.conf"
	hijackerMu sync.Mutex
	hijacker   *ResolvHijacker
)

// The snapshot distinguishes a missing file from an empty one. Link targets
// are stored verbatim, including dangling and relative symlinks.
type resolvSnapshot struct {
	Kind string      `json:"kind"`
	Data []byte      `json:"data,omitempty"`
	Mode os.FileMode `json:"mode,omitempty"`
}

func snapshotPath(path string) string { return path + ".v2raya.json" }

// Keep the lock file at a stable pathname. Unlinking it on close could let a
// new process acquire a different inode while another still holds this one.
func lockResolver(path string) (*os.File, error) {
	lock, err := os.OpenFile(path+".v2raya.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock resolver %s: %w", path, err)
	}
	return lock, nil
}

func snapshotResolv(path string) (resolvSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return resolvSnapshot{Kind: "missing"}, nil
	}
	if err != nil {
		return resolvSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return resolvSnapshot{Kind: "link", Data: []byte(target)}, err
	}
	if !info.Mode().IsRegular() {
		return resolvSnapshot{}, fmt.Errorf("%s is not a regular file or symlink", path)
	}
	data, err := os.ReadFile(path)
	if err == nil && string(data) == hijackedResolv {
		// A release predating snapshots died during a hijack. It left no
		// original to recover; use its normal stopped resolver list.
		data = []byte(HijackFlag + "\nnameserver 223.6.6.6\nnameserver 119.29.29.29\n")
	}
	return resolvSnapshot{Kind: "file", Data: data, Mode: info.Mode().Perm()}, err
}

func syncResolvDir(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func atomicResolvFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".v2raya-resolv-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncResolvDir(path)
}

// Update regular files in place to preserve ownership and support Docker's
// bind-mounted resolv.conf. Replacing a symlink instead leaves its target intact.
func writeResolv(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ELOOP) {
		return atomicResolvFile(path, data, mode)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to write non-regular resolver %s", path)
	}
	if err = file.Truncate(0); err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func restoreResolv(path string) error {
	raw, err := os.ReadFile(snapshotPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var original resolvSnapshot
	if err := json.Unmarshal(raw, &original); err != nil {
		return fmt.Errorf("read resolver snapshot: %w", err)
	}
	switch original.Kind {
	case "file", "missing":
	case "link":
		if len(original.Data) == 0 {
			return fmt.Errorf("empty symlink target in resolver snapshot")
		}
	default:
		return fmt.Errorf("unknown resolver snapshot kind %q", original.Kind)
	}
	// A resolver manager may have installed its own configuration during a
	// reboot. Only roll back our resolver, including an interrupted in-place
	// write on a bind mount. An already restored file needs no second write.
	info, statErr := os.Lstat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
	case statErr != nil:
		return statErr
	case info.Mode()&os.ModeSymlink != 0:
		return os.Remove(snapshotPath(path))
	case info.Mode().IsRegular():
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		partialRestore := original.Kind == "file" && len(data) < len(original.Data) && bytes.HasPrefix(original.Data, data)
		if !bytes.HasPrefix([]byte(hijackedResolv), data) && !partialRestore {
			return os.Remove(snapshotPath(path))
		}
	default:
		return fmt.Errorf("unexpected resolver file type at %s", path)
	}
	switch original.Kind {
	case "file":
		err = writeResolv(path, original.Data, original.Mode)
	case "link":
		// A rename over the live resolver avoids a window without DNS.
		var dir string
		dir, err = os.MkdirTemp(filepath.Dir(path), ".v2raya-resolv-link-*")
		if err == nil {
			defer os.RemoveAll(dir)
			link := filepath.Join(dir, "resolv.conf")
			if err = os.Symlink(string(original.Data), link); err == nil {
				err = os.Rename(link, path)
			}
		}
	case "missing":
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	if err := syncResolvDir(path); err != nil {
		return err
	}
	return os.Remove(snapshotPath(path))
}

// RecoverResolvConf restores a snapshot left when the daemon exited without
// cleanup, before a new core template reads the system's bootstrap DNS.
func RecoverResolvConf() error {
	if conf.GetEnvironmentConfig().Lite {
		return nil
	}
	return recoverResolv(resolvPath)
}

func recoverResolv(path string) error {
	if _, err := os.Lstat(snapshotPath(path)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := lockResolver(path)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return nil // Another instance still owns the active resolver.
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	return restoreResolv(path)
}

// readSystemResolvConf also serves template reloads while the proxy is running.
// In that case the live file would point the new core's bootstrap back to itself.
func readSystemResolvConf() ([]byte, error) {
	hijackerMu.Lock()
	defer hijackerMu.Unlock()
	if hijacker == nil {
		return os.ReadFile(resolvPath)
	}
	original := hijacker.original
	if original.Kind != "link" {
		return original.Data, nil
	}
	target := string(original.Data)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(hijacker.path), target)
	}
	return os.ReadFile(target)
}

type ResolvHijacker struct {
	original resolvSnapshot
	path     string
	lock     *os.File
	done     chan struct{}
	stopped  chan struct{}
	mu       sync.Mutex
}

func newResolvHijacker(path string) (*ResolvHijacker, error) {
	lock, err := lockResolver(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			lock.Close()
		}
	}()
	// A previous instance may have saved the original and then crashed.
	if err = restoreResolv(path); err != nil {
		return nil, err
	}
	original, err := snapshotResolv(path)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	if err = atomicResolvFile(snapshotPath(path), raw, 0600); err != nil {
		return nil, err
	}
	h := &ResolvHijacker{original: original, path: path, lock: lock, done: make(chan struct{}), stopped: make(chan struct{})}
	if err = h.HijackResolv(); err != nil {
		return nil, errors.Join(err, restoreResolv(path))
	}
	go func() {
		defer close(h.stopped)
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-h.done:
				return
			case <-ticker.C:
				if err := h.HijackResolv(); err != nil {
					log.Warn("refresh system resolver: %v", err)
				}
			}
		}
	}()
	return h, nil
}

func NewResolvHijacker() *ResolvHijacker {
	h, err := newResolvHijacker(resolvPath)
	if err != nil {
		log.Warn("take over system resolver: %v", err)
	}
	return h
}

func (h *ResolvHijacker) HijackResolv() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done:
		return nil
	default:
	}
	info, err := os.Lstat(h.path)
	if err == nil && info.Mode().IsRegular() {
		current, err := os.ReadFile(h.path)
		if err != nil {
			return err
		}
		if bytes.Equal(current, []byte(hijackedResolv)) {
			return nil
		}
	}
	return writeResolv(h.path, []byte(hijackedResolv), 0644)
}

func (h *ResolvHijacker) Close() error {
	h.mu.Lock()
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	h.mu.Unlock()
	<-h.stopped // Wait for an in-flight tick before restoring the snapshot.
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lock == nil {
		return nil
	}
	err := restoreResolv(h.path)
	closeErr := h.lock.Close()
	h.lock = nil
	return errors.Join(err, closeErr)
}

func resetResolvHijacker() error {
	hijackerMu.Lock()
	defer hijackerMu.Unlock()
	if hijacker != nil {
		return hijacker.HijackResolv()
	}
	var err error
	hijacker, err = newResolvHijacker(resolvPath)
	return err
}

func removeResolvHijacker() {
	hijackerMu.Lock()
	defer hijackerMu.Unlock()
	var err error
	if hijacker != nil {
		err = hijacker.Close()
		hijacker = nil
	} else {
		err = recoverResolv(resolvPath)
	}
	if err != nil {
		log.Warn("restore system resolver: %v", err)
	}
}
