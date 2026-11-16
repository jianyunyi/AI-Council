package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenConfiguresEveryConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nested", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Hold each connection so subsequent calls acquire a distinct one.
	for i := 0; i < 3; i++ {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for pragma, want := range map[string]string{"foreign_keys": "1", "busy_timeout": "5000", "journal_mode": "wal"} {
			var got string
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("connection %d: %s = %s, want %s", i, pragma, got, want)
			}
		}
	}
}

func TestOpenRejectsIntegrityFailureBeforeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	fixture, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE damaged (value INTEGER CHECK(value > 0))",
		"PRAGMA ignore_check_constraints = ON",
		"INSERT INTO damaged VALUES (-1)",
	} {
		if _, err := fixture.Exec(query); err != nil {
			fixture.Close()
			t.Fatal(err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if db != nil {
		pool, _ := db.DB()
		pool.Close()
	}
	if err == nil {
		t.Fatal("Open accepted a database that fails integrity_check")
	}
	if !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("missing integrity context: %v", err)
	}
	fixture, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = fixture.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'run_records'").Scan(&count)
	fixture.Close()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("migration ran before integrity validation")
	}
	// Windows refuses removal if failed Open leaked an open file handle.
	if err := os.Remove(path); err != nil {
		t.Fatalf("failed Open left database open: %v", err)
	}
}

func TestOpenMemory(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var mode string
	if err := pool.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "memory" {
		t.Fatalf("journal mode = %q", mode)
	}
	if !db.Migrator().HasTable(&RunRecord{}) {
		t.Fatal("memory database was not migrated")
	}
}
