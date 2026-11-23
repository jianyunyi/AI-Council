package idempotency

import (
	"path/filepath"
	"testing"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestStoreRecoversCompletedResultAcrossProcesses(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "runner.db"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	first := NewWithDB(db)
	response := &runnerv1.ExecuteApprovedPlanResponse{RequestId: "cross-process", Status: "SUCCEEDED"}
	release, err := first.Begin(response.RequestId)
	require.NoError(t, err)
	release()
	first.Complete(response.RequestId, response)
	second := NewWithDB(db)
	recovered, ok := second.Get(response.RequestId)
	require.True(t, ok)
	require.Equal(t, response.Status, recovered.Status)
}

func TestStoreClaimsRunningRequestAcrossProcesses(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "runner.db"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	first := NewWithDB(db)
	release, err := first.Begin("running")
	require.NoError(t, err)
	defer release()
	second := NewWithDB(db)
	_, err = second.Begin("running")
	require.ErrorIs(t, err, ErrInProgress)
}
// A running row left by a crashed process is recovered to a terminal
// runner_interrupted failure, so the key no longer blocks as InProgress.
func TestRecoverInterruptedMarksRunningAsFailed(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "runner.db"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Create(&sqlite.ExecutionRecord{RequestID: "stuck", Status: "running", ResponseJSON: []byte("{}")}).Error)

	store := NewWithDB(db)
	require.NoError(t, store.RecoverInterrupted())

	got, ok := store.Get("stuck")
	require.True(t, ok)
	require.Equal(t, "FAILED", got.Status)
	require.Equal(t, "runner_interrupted", got.ErrorCode)
	// A fresh Begin must not report InProgress for a recovered key.
	release, err := NewWithDB(db).Begin("stuck")
	require.NoError(t, err)
	release()
}
