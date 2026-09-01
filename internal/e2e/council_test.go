package e2e

import (
	"context"
	"github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	runnergrpc "github.com/aicouncil/aicouncil/internal/runner/grpc"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

type runnerPort struct{ s *runnergrpc.Service }

func (r runnerPort) Describe(context.Context, string) (task.WorkspaceDescription, error) {
	return task.WorkspaceDescription{}, nil
}
func (r runnerPort) Execute(ctx context.Context, in task.ApprovedExecution) (schema.VerificationReport, error) {
	resp, err := r.s.ExecuteApprovedPlan(ctx, &runnerv1.ExecuteApprovedPlanRequest{RequestId: in.RequestID, RunId: in.RunID, WorkspaceId: in.WorkspaceID, PlanVersion: int32(in.Plan.Version), ApprovalHash: in.ApprovalHash, Acceptance: in.Plan.Acceptance})
	if err != nil {
		return schema.VerificationReport{}, err
	}
	return schema.VerificationReport{Passed: resp.Status == "SUCCEEDED"}, nil
}
func TestCouncilApprovalRunnerVerificationFlow(t *testing.T) {
	root := t.TempDir()
	db, err := sqlite.Open(filepath.Join(root, "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runRepo := sqlite.NewRunRepository(db)
	approvalRepo := sqlite.NewApprovalRepository(db)
	runner, err := runnergrpc.NewService(root)
	require.NoError(t, err)
	svc := task.NewService(runRepo, approvalRepo, nil, nil, runnerPort{runner}, 1)
	ctx := context.Background()
	created, err := svc.Create(ctx, "ws-1", "verify", []string{"tests pass"})
	require.NoError(t, err)
	require.NoError(t, svc.Start(ctx, created.ID))
	plan := created.Plan
	hash, err := approval.Hash(created.ID, "ws-1", plan)
	require.NoError(t, err)
	require.NoError(t, svc.Approve(ctx, created.ID, hash, "operator", 1))
	report, err := svc.Execute(ctx, created.ID, "request-1")
	require.NoError(t, err)
	require.True(t, report.Passed)
}
