package task

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

type countingRunner struct{ executes atomic.Int32 }

func (r *countingRunner) Describe(context.Context, string) (WorkspaceDescription, error) {
	return WorkspaceDescription{}, nil
}
func (r *countingRunner) Execute(context.Context, ApprovedExecution) (schema.VerificationReport, error) {
	r.executes.Add(1)
	return schema.VerificationReport{Passed: true}, nil
}

// A task on a group_shared workspace must be rejected by the kind guard
// before any approval lookup or Runner call.
func TestExecuteRejectsGroupSharedWorkspace(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "ws-group", OwnerUserID: "u1", Kind: wskind.GroupShared}).Error)

	runner := &countingRunner{}
	svc := NewService(sqlite.NewRunRepository(db), sqlite.NewApprovalRepository(db), &fakeEvents{}, nil, runner, 1).
		WithKindLookup(sqlite.NewWorkspaceKindStore(db))
	ctx := context.Background()
	created, err := svc.Create(ctx, "ws-group", "do something", []string{"done"})
	require.NoError(t, err)
	_, err = svc.Execute(ctx, created.ID, "req-1")
	require.ErrorIs(t, err, wskind.ErrNotExecutable)
	require.Equal(t, int32(0), runner.executes.Load())
}

// Standard workspaces pass the guard and reach the normal approval check.
func TestExecuteGuardAllowsStandardWorkspace(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "ws-std", OwnerUserID: "u1", Kind: wskind.Standard}).Error)

	runner := &countingRunner{}
	svc := NewService(sqlite.NewRunRepository(db), sqlite.NewApprovalRepository(db), &fakeEvents{}, nil, runner, 1).
		WithKindLookup(sqlite.NewWorkspaceKindStore(db))
	ctx := context.Background()
	created, err := svc.Create(ctx, "ws-std", "do something", []string{"done"})
	require.NoError(t, err)
	_, err = svc.Execute(ctx, created.ID, "req-1")
	require.ErrorIs(t, err, ErrApprovalRequired) // guard passed; approval gate is what stops it
	require.Equal(t, int32(0), runner.executes.Load())
}
