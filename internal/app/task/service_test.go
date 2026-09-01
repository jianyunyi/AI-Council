package task

import (
	"context"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

type fakeEvents struct{ events []sqlite.Event }

func (e *fakeEvents) Append(_ context.Context, run, typ string, data any) (sqlite.Event, error) {
	v := sqlite.Event{RunID: run, Sequence: int64(len(e.events) + 1), Type: typ}
	e.events = append(e.events, v)
	return v, nil
}
func (e *fakeEvents) After(context.Context, string, int64, int) ([]sqlite.Event, error) {
	return e.events, nil
}

type fakeRunner struct{}

func (fakeRunner) Describe(context.Context, string) (WorkspaceDescription, error) {
	return WorkspaceDescription{}, nil
}
func (fakeRunner) Execute(context.Context, ApprovedExecution) (schema.VerificationReport, error) {
	return schema.VerificationReport{Passed: true}, nil
}
func TestServiceRequiresApprovalAndExecutesApprovedPlan(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runs := sqlite.NewRunRepository(db)
	approvals := sqlite.NewApprovalRepository(db)
	events := &fakeEvents{}
	svc := NewService(runs, approvals, events, nil, fakeRunner{}, 1)
	ctx := context.Background()
	created, err := svc.Create(ctx, "ws-1", "add endpoint", []string{"tests pass"})
	require.NoError(t, err)
	require.ErrorIs(t, func() error { _, e := svc.Execute(ctx, created.ID, "req-1"); return e }(), ErrApprovalRequired)
	require.NoError(t, svc.Start(ctx, created.ID))
	require.NoError(t, svc.Approve(ctx, created.ID, "hash", "user", 1))
	report, err := svc.Execute(ctx, created.ID, "req-1")
	require.NoError(t, err)
	require.True(t, report.Passed)
	require.GreaterOrEqual(t, len(events.events), 2)
}
