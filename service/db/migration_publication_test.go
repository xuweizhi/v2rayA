package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConcurrentMigrationPublication(t *testing.T) {
	for _, previousAttempt := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "v2raya.db")
		if previousAttempt {
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var stages []string
		for _, value := range []string{"first", "second"} {
			stage := filepath.Join(dir, value)
			database, err := createSQLiteDB(stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO system_config(key,value) VALUES (?,?)", value, value); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			stages = append(stages, stage)
		}
		errors := make(chan error, len(stages))
		for _, stage := range stages {
			go func() { errors <- publishMigration(stage, path) }()
		}
		for range stages {
			if err := <-errors; err != nil {
				t.Fatal(err)
			}
		}
		published, err := OpenStandalone(path)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := published.QueryRow("SELECT COUNT(*) FROM system_config").Scan(&count); err != nil || count != 1 {
			t.Fatalf("expected one verified database, got %d rows: %v", count, err)
		}
		if _, err := published.Exec("INSERT INTO system_config(key,value) VALUES ('new','active')"); err != nil {
			t.Fatal(err)
		}
		if err := publishMigration(stages[1], path); err != nil {
			t.Fatal(err)
		}
		if err := published.QueryRow("SELECT COUNT(*) FROM system_config WHERE key='new'").Scan(&count); err != nil || count != 1 {
			t.Fatalf("retry replaced active SQLite data: %v", err)
		}
		published.Close()
	}
}
