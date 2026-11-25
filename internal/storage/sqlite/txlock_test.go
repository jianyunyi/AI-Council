package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

type txProbe struct {
	ID string `gorm:"primaryKey;size:40"`
	N  int64  `gorm:"not null;default:0"`
}

func (txProbe) TableName() string { return "tx_probe" }

func TestWithImmediateTxLock(t *testing.T) {
	cases := map[string]string{
		"/tmp/a.db":                   "/tmp/a.db?_txlock=immediate",
		"file:a.db?_pragma=x":         "file:a.db?_pragma=x&_txlock=immediate",
		"file:a.db?_txlock=exclusive": "file:a.db?_txlock=exclusive",
	}
	for in, want := range cases {
		if got := withImmediateTxLock(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

// Finding 1 regression: the group service's transaction shape (read, then write) from
// 4 goroutines × 100 transactions on the production DSN must not fail with SQLITE_BUSY.
func TestReadThenWriteTransactionsDoNotFailBusy(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "txlock.db")) // F8
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&txProbe{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := db.Create(&txProbe{ID: string(rune('a' + i))}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var failures atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				err := db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
					var p txProbe
					if err := tx.Where("id = ?", id).Take(&p).Error; err != nil { // read first
						return err
					}
					return tx.Model(&txProbe{}).Where("id = ? AND n = ?", id, p.N).
						Update("n", p.N+1).Error // then write
				})
				if err != nil {
					failures.Add(1)
					t.Logf("tx failed: %v", err)
				}
			}
		}(string(rune('a' + g)))
	}
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d/400 read-then-write transactions failed (deferred lock upgrade?)", n)
	}
	var rows []txProbe
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.N != 100 {
			t.Fatalf("row %s: n=%d want 100 (lost update)", r.ID, r.N)
		}
	}
}

func TestOpenRejectsDeferredTransactions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "verify.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyImmediateTxLock(context.Background(), db); err != nil {
		t.Fatalf("production DSN must use BEGIN IMMEDIATE: %v", err)
	}
}
