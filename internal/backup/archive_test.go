package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

func TestEncryptedBackupRestore(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "council.db")
	db, e := sqlite.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	db.Exec("CREATE TABLE probe(value TEXT)")
	db.Exec("INSERT INTO probe VALUES ('restored')")
	pool, _ := db.DB()
	pool.Close()
	art := filepath.Join(root, "artifacts")
	os.MkdirAll(filepath.Join(art, "run"), 0700)
	os.WriteFile(filepath.Join(art, "run", "result.json"), []byte(`{"ok":true}`), 0600)
	key := make([]byte, 32)
	archive := filepath.Join(root, "backup.aicb")
	if e = Create(dbPath, art, archive, key); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, "restore")
	if e = Restore(archive, dest, key); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(dest, "artifacts", "run", "result.json")); e != nil || string(b) != `{"ok":true}` {
		t.Fatalf("artifact %q %v", b, e)
	}
	restored, e := sqlite.Open(filepath.Join(dest, "council.db"))
	if e != nil {
		t.Fatal(e)
	}
	var got string
	restored.Raw("SELECT value FROM probe").Scan(&got)
	p, _ := restored.DB()
	p.Close()
	if got != "restored" {
		t.Fatal(got)
	}
	if e = Restore(archive, dest, key); e == nil {
		t.Fatal("overwrote destination")
	}
}

func TestRestoreRejectsWrongKey(t *testing.T) {
	root := t.TempDir()
	key := make([]byte, 32)
	bad := make([]byte, 32)
	bad[0] = 1
	dbPath := filepath.Join(root, "council.db")
	db, e := sqlite.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	pool.Close()
	art := filepath.Join(root, "artifacts")
	if e = os.Mkdir(art, 0700); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(root, "backup.aicb")
	if e = Create(dbPath, art, archive, key); e != nil {
		t.Fatal(e)
	}
	if e = Restore(archive, filepath.Join(root, "out"), bad); e == nil {
		t.Fatal("accepted wrong key")
	}
}
func TestBackupIncludesRunnerDatabase(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "council.db")
	cdb, e := sqlite.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := cdb.DB()
	p.Close()
	runnerPath := filepath.Join(root, "runner.db")
	rdb, e := sqlite.Open(runnerPath)
	if e != nil {
		t.Fatal(e)
	}
	rdb.Exec("CREATE TABLE probe(v TEXT)")
	rdb.Exec("INSERT INTO probe VALUES('runner')")
	rp, _ := rdb.DB()
	rp.Close()
	art := filepath.Join(root, "artifacts")
	os.MkdirAll(art, 0o700)
	key := make([]byte, 32)
	archive := filepath.Join(root, "b.aicb")
	if e = CreateWithRunner(dbPath, runnerPath, art, archive, key); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, "restore")
	if e = Restore(archive, dest, key); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dest, "runner.db")); e != nil {
		t.Fatalf("runner.db missing from restore: %v", e)
	}
}
