package v2ray

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResolverLifecycle(t *testing.T) {
	for _, kind := range []string{"regular", "empty", "missing", "link", "dangling link", "legacy active"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "resolv.conf")
			const original = "nameserver 192.0.2.1\nsearch lan\n"
			want := original
			switch kind {
			case "link", "dangling link":
				if err := os.Symlink("resolver-managed", path); err != nil {
					t.Fatal(err)
				}
				if kind == "link" {
					if err := os.WriteFile(filepath.Join(dir, "resolver-managed"), []byte(original), 0644); err != nil {
						t.Fatal(err)
					}
				}
			case "regular", "empty", "legacy active":
				content := original
				if kind == "empty" {
					content, want = "", ""
				}
				if kind == "legacy active" {
					content, want = hijackedResolv, HijackFlag+"\nnameserver 223.6.6.6\nnameserver 119.29.29.29\n"
				}
				if err := os.WriteFile(path, []byte(content), 0640); err != nil {
					t.Fatal(err)
				}
			}

			h, err := newResolvHijacker(path)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			assertResolvContents(t, path, hijackedResolv)
			switch kind {
			case "link":
				assertResolvContents(t, filepath.Join(dir, "resolver-managed"), original)
			case "dangling link":
				if _, err := os.Stat(filepath.Join(dir, "resolver-managed")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dangling symlink target created: %v", err)
				}
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(snapshotPath(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("snapshot not removed: %v", err)
			}
			switch kind {
			case "link", "dangling link":
				if target, err := os.Readlink(path); err != nil || target != "resolver-managed" {
					t.Fatalf("restored link %q: %v", target, err)
				}
			case "missing":
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unexpected resolver", err)
				}
			default:
				assertResolvContents(t, path, want)
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("wrong restored file mode: %v, %v", info, err)
				}
			}
		})
	}
}

func assertResolvContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s contains %q, want %q (%v)", path, got, want, err)
	}
}

func TestResolverRecoveryHonorsOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	const original = "nameserver 192.0.2.1\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	h, err := newResolvHijacker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, err := newResolvHijacker(path); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("second owner: %v", err)
	}
	if err := recoverResolv(path); err != nil {
		t.Fatal(err)
	}
	assertResolvContents(t, path, hijackedResolv)

	// Release the daemon's resources as the OS does after an ungraceful exit.
	close(h.done)
	<-h.stopped
	if err := h.lock.Close(); err != nil {
		t.Fatal(err)
	}
	h.lock = nil
	if err := recoverResolv(path); err != nil {
		t.Fatal(err)
	}
	assertResolvContents(t, path, original)
	if err := recoverResolv(path); err != nil {
		t.Fatal(err)
	}
}

func TestResolverFailuresPreserveCurrentAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	const original = "nameserver 192.0.2.1\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	// A damaged backup must not be replaced by another snapshot.
	if err := os.WriteFile(snapshotPath(path), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newResolvHijacker(path); err == nil {
		t.Fatal("accepted damaged backup")
	}
	assertResolvContents(t, path, original)
	if err := os.Remove(snapshotPath(path)); err != nil {
		t.Fatal(err)
	}
	h, err := newResolvHijacker(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err == nil {
		t.Fatal("restore over directory succeeded")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := recoverResolv(path); err != nil {
		t.Fatal(err)
	}
	assertResolvContents(t, path, original)
}

func TestResolverLeavesNetworkManagerReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	h, err := newResolvHijacker(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new-resolver", path); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(path); err != nil || link != "new-resolver" {
		t.Fatalf("replacement lost: %q %v", link, err)
	}
}

func TestResolverConcurrentResetAndStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	resolvPath = path
	t.Cleanup(func() { removeResolvHijacker(); resolvPath = "/etc/resolv.conf" })
	const original = "nameserver 192.0.2.1\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := resetResolvHijacker(); err != nil {
		t.Fatal(err)
	}
	if servers := getSystemDnsServers(); len(servers) != 1 || servers[0] != "192.0.2.1:53" {
		t.Fatalf("reloaded bootstrap: %v", servers)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 8 {
				if err := resetResolvHijacker(); err != nil {
					t.Error(err)
				}
				removeResolvHijacker()
			}
		})
	}
	wg.Wait()
	assertResolvContents(t, path, original)
}
