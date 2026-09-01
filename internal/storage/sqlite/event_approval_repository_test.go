package sqlite

import (
	"context"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestEventRepositorySequencesAndResumes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	r := NewEventRepository(db)
	ctx := context.Background()
	e1, err := r.Append(ctx, "run-1", "task.created", map[string]string{"x": "1"})
	require.NoError(t, err)
	e2, err := r.Append(ctx, "run-1", "state.changed", map[string]string{"state": "ANALYZING"})
	require.NoError(t, err)
	require.Equal(t, int64(1), e1.Sequence)
	require.Equal(t, int64(2), e2.Sequence)
	events, err := r.After(ctx, "run-1", 1, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(2), events[0].Sequence)
}
func TestApprovalRepositoryInvalidatesPriorApproval(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	r := NewApprovalRepository(db)
	ctx := context.Background()
	require.NoError(t, r.Save(ctx, ApprovalRecord{ID: "a1", RunID: "run-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))
	got, err := r.Current(ctx, "run-1", 1)
	require.NoError(t, err)
	require.Equal(t, "hash", got.SnapshotHash)
	require.NoError(t, r.Invalidate(ctx, "run-1"))
	_, err = r.Current(ctx, "run-1", 1)
	require.Error(t, err)
}
