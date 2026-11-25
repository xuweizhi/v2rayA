package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

var (
	dbMu             sync.Mutex
	sqlDB            *sql.DB
	dbPath           string
	readOnly         bool
	readOnlyTempPath string
	IsNewDB          bool // true if the database was just created (no pre-existing file)
)

// ErrNeedMigration is returned when an old BoltDB database is detected,
// indicating that migration to SQLite is required before normal operation.
var ErrNeedMigration = errors.New("bolt.db exists, migration required")

func validateSQLiteDriver() error {
	if sqliteDriverName == "" {
		return errors.New("mips/loong64 build requires CGO_ENABLED=1 to enable sqlite driver")
	}
	return nil
}

// OpenStandalone opens an independent database using the driver selected for
// this architecture. It never initializes or changes the application's DB.
func OpenStandalone(dsn string) (*sql.DB, error) {
	if err := validateSQLiteDriver(); err != nil {
		return nil, err
	}
	return sql.Open(sqliteDriverName, dsn)
}

func openSQLiteFile(path, query string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") { // Windows drive letter
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: query}
	return OpenStandalone(u.String())
}

// SetReadOnly sets the database to read-only mode.
// In read-only mode, a temporary copy of the database is created.
func SetReadOnly() {
	readOnly = true
}

func sqlitePragmaQuery() string {
	switch sqliteDriverName {
	case "sqlite":
		return "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	case "sqlite3":
		return "_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	default:
		return ""
	}
}

// initDB opens or creates the SQLite database with WAL mode
func initDB() {
	confPath := conf.GetEnvironmentConfig().Config
	dbPath = filepath.Join(confPath, "v2raya.db")

	if readOnly {
		// VACUUM INTO takes a consistent snapshot including live, committed WAL
		// frames. Copying the main and WAL files separately races checkpoints.
		f, err := os.CreateTemp("", "v2raya_tmp_sqlite_*.db")
		if err != nil {
			panic(err)
		}
		newPath := f.Name()
		if err := f.Close(); err != nil {
			os.Remove(newPath)
			panic(err)
		}
		if _, err := os.Stat(dbPath); err == nil {
			source, err := openSQLiteFile(dbPath, "mode=ro")
			if err == nil {
				_, snapshotErr := source.Exec("VACUUM INTO ?", newPath)
				err = errors.Join(snapshotErr, source.Close())
			}
			if err != nil {
				os.Remove(newPath)
				panic(err)
			}
		} else if !os.IsNotExist(err) {
			os.Remove(newPath)
			panic(err)
		}
		readOnlyTempPath = newPath
		dbPath = newPath
	}

	var err error
	if err = validateSQLiteDriver(); err != nil {
		log.Fatal("SQLite driver is unavailable: %v", err)
	}
	sqlDB, err = openSQLiteFile(dbPath, sqlitePragmaQuery())
	if err != nil {
		log.Fatal("sql.Open: %v", err)
	}
	// SQLite only supports a single writer; keeping a single pooled connection
	// ensures session PRAGMAs apply consistently and avoids SQLITE_BUSY across
	// secondary pool connections.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// Configure PRAGMAs for WAL mode and performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-8000",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			log.Fatal("Failed to set PRAGMA %s: %v", p, err)
		}
	}

	// Initialize schema
	if err := InitSchema(sqlDB); err != nil {
		log.Fatal("InitSchema: %v", err)
	}

	// Apply incremental schema migrations for existing databases
	if err := MigrateSchema(sqlDB); err != nil {
		log.Fatal("MigrateSchema: %v", err)
	}
}

// GetDB returns the singleton SQLite database connection
func GetDB() *sql.DB {
	dbMu.Lock()
	defer dbMu.Unlock()
	if sqlDB == nil {
		initDB()
	}
	return sqlDB
}

// DBPath returns the path to the SQLite database file
func DBPath() string {
	dbMu.Lock()
	defer dbMu.Unlock()
	if sqlDB == nil {
		initDB()
	}
	return dbPath
}

// Open initializes the database connection.
// It first checks if an old BoltDB database (bolt.db) exists.
// If bolt.db exists, it returns ErrNeedMigration to signal that
// migration is required before SQLite can be used.
// Otherwise, it creates/opens the SQLite database and initializes the schema.
func Open() error {
	confPath := conf.GetEnvironmentConfig().Config
	boltPath := filepath.Join(confPath, "bolt.db")
	sqlitePath := filepath.Join(confPath, "v2raya.db")

	if _, err := os.Stat(boltPath); err == nil {
		return ErrNeedMigration
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect BoltDB: %w", err)
	}
	// An overlapping startup may already have published a migrated database.
	// Exclusive creation keeps that file intact.
	f, err := os.OpenFile(sqlitePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	switch {
	case err == nil:
		IsNewDB = true
		if err := f.Close(); err != nil {
			return err
		}
	case os.IsExist(err):
		IsNewDB = false
	default:
		return fmt.Errorf("open SQLite database file: %w", err)
	}

	GetDB()
	return nil
}

const databaseEmpty = `NOT (
	EXISTS (SELECT 1 FROM system_config) OR EXISTS (SELECT 1 FROM accounts) OR
	EXISTS (SELECT 1 FROM servers) OR EXISTS (SELECT 1 FROM subscriptions) OR
	EXISTS (SELECT 1 FROM outbound_names))`

// IsEmpty includes schema-only databases left behind by an interrupted first run.
func IsEmpty() (bool, error) {
	var empty bool
	err := GetDB().QueryRow("SELECT " + databaseEmpty).Scan(&empty)
	return empty, err
}

// InitDefaultsIfEmpty writes the first-run defaults in one statement. Only the
// startup that initialized the database should import the v4 links.
func InitDefaultsIfEmpty(setting, ports any) (bool, error) {
	settingJSON, err := json.Marshal(setting)
	if err != nil {
		return false, err
	}
	portsJSON, err := json.Marshal(ports)
	if err != nil {
		return false, err
	}
	result, err := GetDB().Exec(`WITH defaults(key, value) AS (VALUES
		('system:setting', ?), ('system:ports', ?), ('system:routingA', 'null'))
		INSERT INTO system_config(key, value) SELECT key, value FROM defaults WHERE `+
		databaseEmpty, string(settingJSON), string(portsJSON))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n != 0, err
}

// Close closes the SQLite database connection
func Close() error {
	dbMu.Lock()
	defer dbMu.Unlock()
	var err error
	if sqlDB != nil {
		err = sqlDB.Close()
		sqlDB = nil
	}
	if readOnlyTempPath != "" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(readOnlyTempPath + suffix)
		}
		readOnlyTempPath = ""
	}
	return err
}

// ReadModifyWrite executes a function within a read-write transaction.
// If the function returns an error, the transaction is rolled back.
// Otherwise, the transaction is committed.
func ReadModifyWrite(fn func(tx *sql.Tx) error) error {
	db := GetDB()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}
