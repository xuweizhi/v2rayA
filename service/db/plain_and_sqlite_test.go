package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/v2rayA/v2rayA/conf"
)

func TestPlainOpExactPrefixAndLegacyAccounts(t *testing.T) {
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = Close() })

	// Set keys with wildcard-like characters and mixed-case bucket names.
	if err := Set("outbound.a_b", "setting", "underscore"); err != nil {
		t.Fatal(err)
	}
	if err := Set("outbound.axb", "setting", "letter-x"); err != nil {
		t.Fatal(err)
	}
	if err := Set("outbound.Proxy", "setting", "upper"); err != nil {
		t.Fatal(err)
	}
	if err := Set("outbound.proxy", "setting", "lower"); err != nil {
		t.Fatal(err)
	}
	if err := Set("outbound.foo%", "setting", "percent"); err != nil {
		t.Fatal(err)
	}
	if err := Set("outbound.foobar", "setting", "foobar"); err != nil {
		t.Fatal(err)
	}

	if n, err := GetBucketLen("outbound.a_b"); err != nil || n != 1 {
		t.Fatalf("GetBucketLen(outbound.a_b) = %d, %v; want 1", n, err)
	}
	if n, err := GetBucketLen("outbound.Proxy"); err != nil || n != 1 {
		t.Fatalf("GetBucketLen(outbound.Proxy) = %d, %v; want 1", n, err)
	}
	if n, err := GetBucketLen("outbound.foo%"); err != nil || n != 1 {
		t.Fatalf("GetBucketLen(outbound.foo%%) = %d, %v; want 1", n, err)
	}
	keys, err := GetBucketKeys("outbound.a_b")
	if err != nil || len(keys) != 1 || keys[0] != "setting" {
		t.Fatalf("GetBucketKeys(outbound.a_b) = %v, %v", keys, err)
	}

	// Clearing outbound.a_b must NOT delete outbound.axb.
	if err := BucketClear("outbound.a_b"); err != nil {
		t.Fatal(err)
	}
	if !Exists("outbound.axb", "setting") {
		t.Fatal("BucketClear(outbound.a_b) deleted outbound.axb")
	}
	// Clearing outbound.Proxy must NOT delete outbound.proxy.
	if err := BucketClear("outbound.Proxy"); err != nil {
		t.Fatal(err)
	}
	if !Exists("outbound.proxy", "setting") {
		t.Fatal("BucketClear(outbound.Proxy) deleted outbound.proxy")
	}
	// Clearing outbound.foo% must NOT delete outbound.foobar.
	if err := BucketClear("outbound.foo%"); err != nil {
		t.Fatal(err)
	}
	if !Exists("outbound.foobar", "setting") {
		t.Fatal("BucketClear(outbound.foo%%) deleted outbound.foobar")
	}

	// Legacy unprefixed account key + prefixed account key must both be counted and cleared.
	if _, err := GetDB().Exec(`INSERT INTO system_config(key, value) VALUES ('legacyadmin', '"hash1"')`); err != nil {
		t.Fatal(err)
	}
	if err := Set("accounts", "newadmin", "hash2"); err != nil {
		t.Fatal(err)
	}
	if n, err := GetBucketLen("accounts"); err != nil || n != 2 {
		t.Fatalf("GetBucketLen(accounts) = %d, %v; want 2", n, err)
	}
	if err := BucketClear("accounts"); err != nil {
		t.Fatal(err)
	}
	if n, err := GetBucketLen("accounts"); err != nil || n != 0 {
		t.Fatalf("GetBucketLen(accounts) after clear = %d, %v; want 0", n, err)
	}
	// Other buckets must remain intact.
	if !Exists("outbound.proxy", "setting") {
		t.Fatal("BucketClear(accounts) affected other buckets")
	}
}

func TestListRemoveAtomicTransaction(t *testing.T) {
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = Close() })

	if err := ListAppend("touch", "servers", []map[string]string{{"name": "a"}, {"name": "b"}, {"name": "c"}}); err != nil {
		t.Fatal(err)
	}
	// Abort during sort reindex and verify DELETE was rolled back atomically.
	if _, err := GetDB().Exec(`CREATE TRIGGER abort_reindex BEFORE UPDATE OF sort ON servers BEGIN SELECT RAISE(ABORT, 'reindex failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := ListRemove("touch", "servers", []int{0}); err == nil {
		t.Fatal("expected ListRemove failure from trigger")
	}
	if _, err := GetDB().Exec(`DROP TRIGGER abort_reindex`); err != nil {
		t.Fatal(err)
	}
	if n, err := ListLen("touch", "servers"); err != nil || n != 3 {
		t.Fatalf("partial delete persisted after failed reindex: len=%d, err=%v", n, err)
	}
}

func TestSQLiteConcurrentAccessAndListGetAllSubscriptions(t *testing.T) {
	conf.GetEnvironmentConfig().Config = t.TempDir()
	t.Cleanup(func() { _ = Close() })

	// Populate multiple subscriptions with servers and ensure ListGetAll does not deadlock with MaxOpenConns(1).
	if err := ListAppend("touch", "subscriptions", []map[string]any{
		{"address": "https://one.test", "remarks": "one", "servers": []map[string]string{{"name": "s1"}, {"name": "s2"}}},
		{"address": "https://two.test", "remarks": "two", "servers": []map[string]string{{"name": "s3"}}},
	}); err != nil {
		t.Fatal(err)
	}
	all, err := ListGetAll("touch", "subscriptions")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListGetAll(subscriptions) = %d, %v", len(all), err)
	}

	// Concurrent reads and writes must not hit SQLITE_BUSY.
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Set("concurrent", "key", i); err != nil {
				errCh <- err
				return
			}
			var got int
			if err := Get("concurrent", "key", &got); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent db access failed: %v", err)
	}
}

func TestReadOnlyModeCopiesWALAndCleansTempFilesOnClose(t *testing.T) {
	dir := t.TempDir()
	origDB := filepath.Join(dir, "v2raya.db")

	// Create a DB with committed data still residing only in the WAL file.
	writer, err := sql.Open(sqliteDriverName, origDB)
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	if err := InitSchema(writer); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=0",
		`INSERT INTO system_config(key, value) VALUES ('system:waltest', '"from-wal"')`,
	} {
		if _, err := writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// Keep writer open while taking the read-only snapshot so data remains in -wal.
	defer writer.Close()

	conf.GetEnvironmentConfig().Config = dir
	readOnly = true
	t.Cleanup(func() {
		_ = Close()
		readOnly = false
	})

	var val string
	if err := Get("system", "waltest", &val); err != nil || val != "from-wal" {
		t.Fatalf("read-only snapshot missed WAL data: val=%q err=%v", val, err)
	}
	tmpPath := readOnlyTempPath
	if tmpPath == "" {
		t.Fatal("expected readOnlyTempPath to be recorded")
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(tmpPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("temp file %s%s was not cleaned up by Close()", tmpPath, suffix)
		}
	}
}
