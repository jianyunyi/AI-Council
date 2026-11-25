package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// errTxReadPhase / errTxWritePhase mark which phase of a read-then-write
// transaction failed, so the regression test can tell the deferred
// lock-upgrade bug (write phase) from BEGIN-timeout queueing (read phase).
// See TestReadThenWriteTransactionsDoNotFailBusy.
var (
	errTxReadPhase  = errors.New("txlock test: read phase failed")
	errTxWritePhase = errors.New("txlock test: write phase failed")
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
//
// The assertion is split by failure PHASE, because the two ways this test can
// fail are different mechanisms with different meanings:
//
//   - write-phase failure (the UPDATE fails): the deferred lock-upgrade bug.
//     A deferred transaction reads under a SHARED lock and must upgrade to
//     write, and SQLite refuses that upgrade immediately, ignoring the busy
//     timeout. With BEGIN IMMEDIATE the write lock is taken up front, so a
//     failure here can only mean the fix regressed. Any write-phase failure
//     fails the test.
//   - read/commit-phase failure (the first read fails, or the body succeeded
//     but the commit fails): BEGIN/commit timed out after the 5 s
//     busy_timeout while queueing behind another writer. That is the
//     acknowledged residual, not the bug. It cannot be the upgrade bug: the
//     upgrade happens at the first write statement, inside the body, and
//     the body already succeeded by the time a commit fails. Observed
//     evidence on this shared 1.5-CPU VM (3 runs, 2026-11-25): 1-2/400
//     failures, all at COMMIT, zero at the read — the lock holder stalls
//     for seconds under scheduler/GC jitter and the commit's EXCLUSIVE
//     upgrade times out. A systematic collapse (a timeout of effectively 0,
//     a writer that never releases) would fail hundreds, so the bound below
//     (10) keeps the test from going vacuous while tolerating environment
//     jitter.
//
// The no-lost-update check compares each row's final N against the number of
// transactions that actually succeeded for that row, so a residual failure
// (whose increment never happened) does not masquerade as a lost update.
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
	var residualFailures, writeFailures atomic.Int64
	var successes [4]atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := string(rune('a' + g))
			for i := 0; i < 100; i++ {
				err := db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
					var p txProbe
					if err := tx.Where("id = ?", id).Take(&p).Error; err != nil { // read first
						// BEGIN timed out queueing: the residual
						return fmt.Errorf("%w: %v", errTxReadPhase, err)
					}
					if err := tx.Model(&txProbe{}).Where("id = ? AND n = ?", id, p.N).
						Update("n", p.N+1).Error; err != nil { // then write
						// lock upgrade refused: the bug
						return fmt.Errorf("%w: %v", errTxWritePhase, err)
					}
					return nil
				})
				switch {
				case err == nil:
					successes[g].Add(1)
				case errors.Is(err, errTxWritePhase):
					writeFailures.Add(1)
					t.Logf("tx failed at write (lock upgrade, the bug): %v", err)
				default:
					// Read-phase (BEGIN timeout) or commit-phase: the
					// residual. A commit failure is classified here, not
					// as the bug — the body already succeeded, so the
					// upgrade already happened.
					residualFailures.Add(1)
					t.Logf("tx failed at read/commit (BEGIN/commit timeout, residual): %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	if n := writeFailures.Load(); n != 0 {
		t.Fatalf("%d/400 read-then-write transactions failed at the write (deferred lock upgrade?)", n)
	}
	if n := residualFailures.Load(); n > 10 {
		t.Fatalf("%d/400 read-then-write transactions failed at read/commit (BEGIN/commit timeout); bound is 10, systematic queueing collapse?", n)
	}
	t.Logf("residual (BEGIN/commit timeout) failures: %d/400", residualFailures.Load())
	var rows []txProbe
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		g := int(r.ID[0] - 'a')
		if want := successes[g].Load(); r.N != want {
			t.Fatalf("row %s: n=%d want %d (lost update)", r.ID, r.N, want)
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
