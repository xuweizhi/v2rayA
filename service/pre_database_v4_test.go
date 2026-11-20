//go:build !windows

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/v2rayA/v2rayA/db"
	"go.etcd.io/bbolt"
)

func TestMain(m *testing.M) {
	if os.Getenv("V2RAYA_TEST_INIT_CONFIG") == "1" {
		initConfigure()
		if err := db.Close(); err != nil {
			panic(err)
		}
		return
	}
	os.Exit(m.Run())
}

func TestFirstRunCoreFlag(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "v4-after-interrupted-start"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "boltv4.db")
			var before []byte
			if legacy {
				// The old CLI failure happened after SQLite had created its schema.
				database, err := db.OpenStandalone(filepath.Join(dir, "v2raya.db"))
				if err != nil {
					t.Fatal(err)
				}
				if err := db.InitSchema(database); err != nil {
					t.Fatal(err)
				}
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
				writeV4ServerFixture(t, path)
				before, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				cmd := exec.Command(os.Args[0], "--lite", "--config="+dir, "--core-type=xray")
				cmd.Env = []string{"V2RAYA_TEST_INIT_CONFIG=1", "PATH=" + dir}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("first-run configuration: %v\n%s", err, out)
				}
			}
			database, err := db.OpenStandalone(filepath.Join(dir, "v2raya.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var keys int
			if err := database.QueryRow(`SELECT count(*) FROM system_config WHERE key IN
				('system:setting', 'system:ports', 'system:routingA')`).Scan(&keys); err != nil || keys != 3 {
				t.Fatalf("stored %d default keys: %v", keys, err)
			}
			if !legacy {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("fresh startup created a v4 file: %v", err)
				}
				return
			}
			var count int
			var name string
			if err := database.QueryRow(`SELECT count(*), json_extract(config_json, '$.serverObj.name') FROM servers`).Scan(&count, &name); err != nil || count != 1 || name != "legacy" {
				t.Fatalf("migrated servers: count=%d name=%q err=%v", count, name, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("v4 source changed: %v", err)
			}
		})
	}
}

func writeV4ServerFixture(t *testing.T, path string) {
	t.Helper()
	source, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Update(func(tx *bbolt.Tx) error {
		system, err := tx.CreateBucket([]byte("system"))
		if err != nil {
			return err
		}
		if err := system.Put([]byte("setting"), []byte("{}")); err != nil {
			return err
		}
		touch, err := tx.CreateBucket([]byte("touch"))
		if err != nil {
			return err
		}
		return touch.Put([]byte("servers_v2"), []byte(`[{"serverObj":{"protocol":"socks5","name":"legacy","server":"127.0.0.1","port":1080}}]`))
	}); err != nil {
		t.Fatal(err)
	}
}
