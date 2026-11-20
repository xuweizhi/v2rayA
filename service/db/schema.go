package db

import (
	"database/sql"
	"fmt"

	"github.com/v2rayA/v2rayA/pkg/util/log"
)

// SQL statements for creating all tables
const schemaSQL = `
CREATE TABLE IF NOT EXISTS system_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS servers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL DEFAULT 'server',
    sub_id INTEGER DEFAULT NULL,
    address TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL DEFAULT 0,
    protocol TEXT NOT NULL DEFAULT '',
    config_json TEXT NOT NULL DEFAULT '{}',
    intel TEXT DEFAULT '',
    latency TEXT DEFAULT '',
    link TEXT DEFAULT '',
    url TEXT DEFAULT '',
    sort INTEGER NOT NULL DEFAULT 0,
    group_id TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT NOT NULL DEFAULT '',
    remarks TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    info TEXT DEFAULT '',
    auto_select INTEGER NOT NULL DEFAULT 0,
    decrypt_password TEXT NOT NULL DEFAULT '',
    extra_json TEXT NOT NULL DEFAULT '{}',
    filter TEXT DEFAULT '',
    group_id TEXT DEFAULT '',
    sort INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS outbound_names (
    name TEXT PRIMARY KEY,
    sort INTEGER NOT NULL DEFAULT 0
);
`

// InitSchema creates all tables if they don't exist
func InitSchema(db *sql.DB) error {
	log.Info("Initializing database schema")
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return err
	}
	log.Info("Database schema initialized successfully")
	return nil
}

// MigrateSchema applies incremental schema migrations for existing databases.
// Unlike InitSchema (which uses CREATE TABLE IF NOT EXISTS), this handles
// ALTER TABLE additions for columns added after the initial schema version.
func MigrateSchema(db *sql.DB) error {
	// Check if remarks column exists (added after initial schema)
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'remarks'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for remarks column: %w", err)
	}
	if count == 0 {
		log.Info("Adding remarks column to subscriptions table")
		if _, err := db.Exec("ALTER TABLE subscriptions ADD COLUMN remarks TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("failed to add remarks column: %w", err)
		}
	}

	// Check if auto_select column exists (added after initial schema)
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'auto_select'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for auto_select column: %w", err)
	}
	if count == 0 {
		log.Info("Adding auto_select column to subscriptions table")
		if _, err := db.Exec("ALTER TABLE subscriptions ADD COLUMN auto_select INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("failed to add auto_select column: %w", err)
		}
	}

	// Check if decrypt_password column exists (added for password-protected subscriptions)
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'decrypt_password'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for decrypt_password column: %w", err)
	}
	if count == 0 {
		log.Info("Adding decrypt_password column to subscriptions table")
		if _, err := db.Exec("ALTER TABLE subscriptions ADD COLUMN decrypt_password TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("failed to add decrypt_password column: %w", err)
		}
	}

	// Check if extra_json column exists (per-subscription advanced settings)
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'extra_json'").Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check for extra_json column: %w", err)
	}
	if count == 0 {
		log.Info("Adding extra_json column to subscriptions table")
		if _, err := db.Exec("ALTER TABLE subscriptions ADD COLUMN extra_json TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return fmt.Errorf("failed to add extra_json column: %w", err)
		}
	}

	return migrateLegacyOutboundTables(db)
}

// Earlier SQLite releases stored migrated group memberships as server row IDs.
// The runtime uses list positions and keeps both group values in system_config.
func migrateLegacyOutboundTables(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var connections, settings bool
	err = tx.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='outbound_connections'),
		EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='outbound_settings')`).Scan(&connections, &settings)
	if err != nil {
		return err
	}
	if connections {
		if err := recoverOutboundConnections(tx); err != nil {
			return fmt.Errorf("recover old outbound members: %w", err)
		}
		if _, err := tx.Exec("DROP TABLE outbound_connections"); err != nil {
			return err
		}
	}
	if settings {
		// A group edited since the old migration already has an authoritative key.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO system_config(key, value)
			SELECT 'outbound.' || outbound_name || ':setting', setting_json FROM outbound_settings
			WHERE setting_json IS NOT NULL AND setting_json NOT IN ('', '{}')`); err != nil {
			return fmt.Errorf("recover old outbound settings: %w", err)
		}
		if _, err := tx.Exec("DROP TABLE outbound_settings"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func recoverOutboundConnections(tx *sql.Tx) error {
	// Server lists are one-based in Which. Subscription indexes are zero-based.
	// A deleted server/subscription can leave a stale foreign key in older DBs.
	_, err := tx.Exec(`WITH nodes AS (
			SELECT id, type, sub_id,
				row_number() OVER (PARTITION BY type, sub_id ORDER BY sort, id) AS node_index
			FROM servers
		), subs AS (
			SELECT id, row_number() OVER (ORDER BY sort, id) - 1 AS sub_index FROM subscriptions
		)
		INSERT OR IGNORE INTO system_config(key, value)
		SELECT 'outbound.' || outbound_name || ':connectedServers',
			json_object('touches', json_group_array(json_object(
				'_type', node_type, 'id', node_index, 'sub', sub_index, 'outbound', outbound_name)))
		FROM (
			SELECT c.outbound_name, s.node_index,
				CASE s.type WHEN 'server' THEN 'server' ELSE 'subscriptionServer' END AS node_type,
				COALESCE(sub.sub_index, 0) AS sub_index
			FROM outbound_connections c
			JOIN nodes s ON s.id = c.server_id
			LEFT JOIN subs sub ON s.sub_id = sub.id
			WHERE s.type = 'server' OR (s.type = 'subscription_server' AND sub.id IS NOT NULL)
			ORDER BY c.outbound_name, c.sort, c.id
		) GROUP BY outbound_name`)
	return err
}
