package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/v2rayA/v2rayA/db"
)

func restoreFixture(t *testing.T, filename, value string) {
	t.Helper()
	database, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, table := range []string{"system_config", "servers", "subscriptions", "accounts", "outbound_names", "outbound_settings", "outbound_connections"} {
		if _, err := database.Exec("CREATE TABLE " + table + " (value TEXT)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec("INSERT INTO system_config VALUES (?)", value); err != nil {
		t.Fatal(err)
	}
}

func stageRestore(t *testing.T, dir string) string {
	t.Helper()
	stage := filepath.Join(dir, "restore-staging.db")
	data, err := json.Marshal(map[string]string{"staging": stage})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "restore-pending.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return stage
}

func restoreValue(t *testing.T, filename string) string {
	t.Helper()
	database, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value string
	if err := database.QueryRow("SELECT value FROM system_config").Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRestoreRejectsCorruptDatabaseWithoutChangingCurrent(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "v2raya.db")
	restoreFixture(t, original, "original")
	stage := stageRestore(t, dir)
	if err := os.WriteFile(stage, []byte("SQLite format 3\x00not-a-database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := applyPendingRestore(dir); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if restoreValue(t, original) != "original" {
		t.Fatal("original database changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-pending.json")); err != nil {
		t.Fatal("failed restore discarded marker")
	}
}

func TestRestoreSwapFailureKeepsDatabaseAndSidecars(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "v2raya.db")
	restoreFixture(t, original, "original")
	stage := stageRestore(t, dir)
	restoreFixture(t, stage, "restored")
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(original+suffix, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	err := applyPendingRestoreFiles(dir, func(from, to string) error {
		if from == stage {
			return fmt.Errorf("injected rename failure")
		}
		return os.Rename(from, to)
	})
	if err == nil {
		t.Fatal("expected rename failure")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		content, err := os.ReadFile(original + suffix)
		if err != nil || string(content) != "preserve" {
			t.Fatalf("sidecar lost: %s, %v", suffix, err)
		}
		if err := os.Remove(original + suffix); err != nil {
			t.Fatal(err)
		}
	}
	if restoreValue(t, original) != "original" {
		t.Fatal("old database lost")
	}
}

func TestRestorePreservesCommittedWALInSafetyCopy(t *testing.T) {
	// Create a crash-like snapshot while committed data is still only in WAL.
	source := filepath.Join(t.TempDir(), "source.db")
	restoreFixture(t, source, "before-wal")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	for _, query := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "UPDATE system_config SET value='committed-wal'"} {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "quoted-\"dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "v2raya.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		content, err := os.ReadFile(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(original+suffix, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	stage := stageRestore(t, dir)
	restoreFixture(t, stage, "restored")
	if err := applyPendingRestore(dir); err != nil {
		t.Fatal(err)
	}
	if restoreValue(t, original) != "restored" {
		t.Fatal("restore failed")
	}
	backups, err := filepath.Glob(original + ".pre-restore-*")
	if err != nil {
		t.Fatal(err)
	}
	for _, backup := range backups {
		if filepath.Ext(backup) == "" {
			continue
		}
		if len(backup) >= 4 && (backup[len(backup)-4:] == "-wal" || backup[len(backup)-4:] == "-shm") {
			continue
		}
		if restoreValue(t, backup) != "committed-wal" {
			t.Fatal("safety copy lost committed WAL data")
		}
		return
	}
	t.Fatal("safety copy missing")
}

func TestRestoreAcrossOutboundSchemas(t *testing.T) {
	for _, layout := range []string{"legacy", "current"} {
		t.Run(layout, func(t *testing.T) {
			dir := t.TempDir()
			staged := stageRestore(t, dir)
			database, err := db.OpenStandalone(staged)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.InitSchema(database); err != nil {
				t.Fatal(err)
			}
			const groupSetting = `{"preProxy":"entrance","probeURL":"https://example.test/health"}`
			queries := []string{
				"INSERT INTO outbound_names(name) VALUES ('exit')",
				"INSERT INTO subscriptions(address, decrypt_password, extra_json) VALUES ('https://example.test/sub', 'fixture', '{\"preferNodeDNS\":true}')",
				"INSERT INTO servers(id, type, sub_id, sort) VALUES (7, 'subscription_server', 1, 0)",
			}
			if layout == "legacy" {
				queries = append(queries,
					"CREATE TABLE outbound_connections(id INTEGER PRIMARY KEY, outbound_name TEXT, sort INTEGER, server_id INTEGER REFERENCES servers(id))",
					"CREATE TABLE outbound_settings(outbound_name TEXT PRIMARY KEY, setting_json TEXT)",
					"INSERT INTO outbound_connections(outbound_name, sort, server_id) VALUES ('exit',0,7)",
					"INSERT INTO outbound_settings VALUES ('exit','"+groupSetting+"')",
				)
			} else {
				queries = append(queries,
					`INSERT INTO system_config VALUES ('outbound.exit:connectedServers','{"touches":[{"_type":"subscriptionServer","id":1,"sub":0,"outbound":"exit"}]}')`,
					"INSERT INTO system_config VALUES ('outbound.exit:setting','"+groupSetting+"')",
				)
			}
			for _, q := range queries {
				if _, err := database.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := applyPendingRestore(dir); err != nil {
				t.Fatal(err)
			}
			restored, err := db.OpenStandalone(filepath.Join(dir, "v2raya.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err := db.MigrateSchema(restored); err != nil {
				t.Fatal(err)
			}
			var setting, memberType, password, extra string
			var index, subIndex int
			if err := restored.QueryRow("SELECT value FROM system_config WHERE key='outbound.exit:setting'").Scan(&setting); err != nil || setting != groupSetting {
				t.Fatalf("restored setting %q, err %v", setting, err)
			}
			if err := restored.QueryRow(`SELECT json_extract(value,'$.touches[0]._type'), json_extract(value,'$.touches[0].id'), json_extract(value,'$.touches[0].sub') FROM system_config WHERE key = 'outbound.exit:connectedServers'`).Scan(&memberType, &index, &subIndex); err != nil || memberType != "subscriptionServer" || index != 1 || subIndex != 0 {
				t.Fatalf("restored member %s/%d/%d, err %v", memberType, index, subIndex, err)
			}
			if err := restored.QueryRow("SELECT decrypt_password, extra_json FROM subscriptions").Scan(&password, &extra); err != nil || password != "fixture" || extra != `{"preferNodeDNS":true}` {
				t.Fatalf("restored subscription options %q, %q, err %v", password, extra, err)
			}
			if _, err := restored.Exec("PRAGMA foreign_keys=ON; DELETE FROM servers WHERE id=7"); err != nil {
				t.Fatalf("stale foreign key: %v", err)
			}
		})
	}
}
