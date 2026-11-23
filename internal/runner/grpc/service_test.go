package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/files"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newRegisteredService(t *testing.T, root string, ids ...string) *Service {
	t.Helper()
	svc, err := NewService(root)
	require.NoError(t, err)
	for _, id := range ids {
		// A workspace is now a named subdirectory beneath the root, never ".".
		rel := id
		require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o700))
		_, err := svc.RegisterWorkspace(context.Background(), &runnerv1.RegisterWorkspaceRequest{WorkspaceId: id, RelativePath: rel})
		require.NoError(t, err)
	}
	return svc
}

// Rejecting "." keeps a single caller from claiming the entire shared root.
func TestRegisterRejectsWorkspaceRoot(t *testing.T) {
	svc, err := NewService(t.TempDir())
	require.NoError(t, err)
	_, err = svc.RegisterWorkspace(context.Background(), &runnerv1.RegisterWorkspaceRequest{WorkspaceId: "root", RelativePath: "."})
	require.Error(t, err)
}

func TestExecuteApprovedPlanAppliesPatchAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	svc := newRegisteredService(t, root, "ws-1")
	path := filepath.Join(root, "ws-1", "main.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	h := sha256.Sum256([]byte("old\n"))
	plan := schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: "main.go", UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n", BeforeHash: hex.EncodeToString(h[:])}}}
	hash, err := approval.Hash("run-1", "ws-1", plan)
	require.NoError(t, err)
	req := &runnerv1.ExecuteApprovedPlanRequest{RequestId: "req-1", RunId: "run-1", WorkspaceId: "ws-1", PlanVersion: 1, ApprovalHash: hash, Patches: []*runnerv1.ApprovedPatch{{Path: "main.go", UnifiedDiff: plan.Patches[0].UnifiedDiff, BeforeHash: plan.Patches[0].BeforeHash}}}
	first, err := svc.ExecuteApprovedPlan(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", first.Status)
	got, _ := os.ReadFile(path)
	require.Equal(t, "new\n", string(got))
	second, err := svc.ExecuteApprovedPlan(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, first.RequestId, second.RequestId)
	got, _ = os.ReadFile(path)
	require.Equal(t, "new\n", string(got))
}

func TestRegisteredWorkspaceIDsResolveToIsolatedRootsAndPersist(t *testing.T) {
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	firstRoot := filepath.Join(base, "first")
	secondRoot := filepath.Join(base, "second")
	require.NoError(t, os.Mkdir(firstRoot, 0o700))
	require.NoError(t, os.Mkdir(secondRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(firstRoot, "first.txt"), []byte("first"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secondRoot, "second.txt"), []byte("second"), 0o600))
	db, err := sqlite.Open(filepath.Join(base, "runner.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	svc, err := NewServiceWithDB(filepath.Join(base), db)
	require.NoError(t, err)
	for id, path := range map[string]string{"ws-first": "first", "ws-second": "second"} {
		_, err := svc.RegisterWorkspace(context.Background(), &runnerv1.RegisterWorkspaceRequest{WorkspaceId: id, RelativePath: path})
		require.NoError(t, err)
	}
	first, err := svc.ReadWorkspaceContext(context.Background(), &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "ws-first"})
	require.NoError(t, err)
	require.Equal(t, firstRoot, first.Root)
	require.Equal(t, "first.txt", first.Files[0].Path)
	second, err := svc.ReadWorkspaceContext(context.Background(), &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "ws-second"})
	require.NoError(t, err)
	require.Equal(t, secondRoot, second.Root)
	require.Equal(t, "second.txt", second.Files[0].Path)
	_, err = svc.DescribeWorkspace(context.Background(), &runnerv1.DescribeWorkspaceRequest{WorkspaceId: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err))

	svc, err = NewServiceWithDB(base, db)
	require.NoError(t, err)
	registered, err := svc.DescribeWorkspace(context.Background(), &runnerv1.DescribeWorkspaceRequest{WorkspaceId: "ws-first"})
	require.NoError(t, err)
	require.Equal(t, firstRoot, registered.Root)
}

func TestRunnerRejectsUnknownWorkspaceForContextAndExecution(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "known")
	_, err := svc.ReadWorkspaceContext(context.Background(), &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{RequestId: "unknown-run", RunId: "run", WorkspaceId: "unknown"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestExecuteApprovedPlanRestoresPatchWhenVerificationFails(t *testing.T) {
	root := t.TempDir()
	svc := newRegisteredService(t, root, "ws-verify")
	path := filepath.Join(root, "ws-verify", "main.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	h := sha256.Sum256([]byte("old\n"))
	plan := schema.ExecutionPlan{
		Version:              1,
		Patches:              []schema.Patch{{Path: "main.go", UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n", BeforeHash: hex.EncodeToString(h[:])}},
		VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
	}
	hash, err := approval.Hash("run-verify", "ws-verify", plan)
	require.NoError(t, err)
	req := &runnerv1.ExecuteApprovedPlanRequest{
		RequestId: "req-verify", RunId: "run-verify", WorkspaceId: "ws-verify", PlanVersion: 1, ApprovalHash: hash,
		Patches:              []*runnerv1.ApprovedPatch{{Path: "main.go", UnifiedDiff: plan.Patches[0].UnifiedDiff, BeforeHash: plan.Patches[0].BeforeHash}},
		VerificationCommands: []*runnerv1.ApprovedCommand{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
	}

	response, err := svc.ExecuteApprovedPlan(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "FAILED", response.Status)
	require.Len(t, response.Steps, 1)
	require.Equal(t, "verification", response.Steps[0].Kind)
	require.Equal(t, "FAILED", response.Steps[0].Status)
	require.Equal(t, "verification_failed:go", response.ErrorCode)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old\n", string(got))
}

func TestExecuteApprovedPlanStopsAfterCommandFailure(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "ws-command-failure")

	plan := schema.ExecutionPlan{
		Version:              1,
		Commands:             []schema.Command{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
		VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 5}},
	}
	hash, err := approval.Hash("run-command-failure", "ws-command-failure", plan)
	require.NoError(t, err)

	response, err := svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{
		RequestId: "req-command-failure", RunId: "run-command-failure", WorkspaceId: "ws-command-failure", PlanVersion: 1, ApprovalHash: hash,
		Commands:             []*runnerv1.ApprovedCommand{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
		VerificationCommands: []*runnerv1.ApprovedCommand{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 5}},
	})
	require.NoError(t, err)
	require.Equal(t, "FAILED", response.Status)
	require.Equal(t, "command_failed:go", response.ErrorCode)
	require.Len(t, response.Steps, 1)
	require.Equal(t, "command", response.Steps[0].Kind)
	require.Equal(t, "FAILED", response.Steps[0].Status)
}

func TestExecuteApprovedPlanReportsRollbackFailure(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	svc := newRegisteredService(t, root)
	// Legacy binding: workspaces registered before the "." ban could point at
	// the runner root itself. Such records still resolve, and the shared
	// runtime (with its injectable transaction factory) serves them.
	require.NoError(t, svc.registry.db.Create(&sqlite.RunnerWorkspaceRecord{WorkspaceID: "ws-rollback-failure", CanonicalRoot: root, CreatedAt: time.Now()}).Error)
	svc.newTransaction = func() transaction { return restoreFailingTransaction{} }

	plan := schema.ExecutionPlan{
		Version:  1,
		Patches:  []schema.Patch{{Path: "main.go", UnifiedDiff: "ignored by test transaction"}},
		Commands: []schema.Command{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
	}
	hash, err := approval.Hash("run-rollback-failure", "ws-rollback-failure", plan)
	require.NoError(t, err)

	response, err := svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{
		RequestId: "req-rollback-failure", RunId: "run-rollback-failure", WorkspaceId: "ws-rollback-failure", PlanVersion: 1, ApprovalHash: hash,
		Patches:  []*runnerv1.ApprovedPatch{{Path: "main.go", UnifiedDiff: "ignored by test transaction"}},
		Commands: []*runnerv1.ApprovedCommand{{Executable: "go", Args: []string{"tool", "not-a-real-tool"}, TimeoutSeconds: 5}},
	})
	require.NoError(t, err)
	require.Equal(t, "FAILED", response.Status)
	require.Equal(t, "rollback_failed:command_failed:go", response.ErrorCode)
	require.Len(t, response.Steps, 1)
	require.Equal(t, "command", response.Steps[0].Kind)
	require.Equal(t, "FAILED", response.Steps[0].Status)
}

type restoreFailingTransaction struct{}

func (restoreFailingTransaction) Apply(context.Context, []schema.Patch) ([]files.Snapshot, error) {
	return nil, nil
}

func (restoreFailingTransaction) Restore() error { return errors.New("restore failed") }

func TestExecuteApprovedPlanRejectsTamperedApproval(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "ws-1")
	_, err := svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{RequestId: "req-1", RunId: "run-1", WorkspaceId: "ws-1", PlanVersion: 1, ApprovalHash: "tampered"})
	require.Error(t, err)
}

func TestExecuteApprovedPlanRejectsTamperedRequest(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "ws-tampered")

	plan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"approved"}}}}
	hash, err := approval.Hash("run-tampered", "ws-tampered", plan)
	require.NoError(t, err)

	_, err = svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{
		RequestId: "req-tampered", RunId: "run-tampered", WorkspaceId: "ws-tampered", PlanVersion: 1, ApprovalHash: hash,
		Commands: []*runnerv1.ApprovedCommand{{Executable: "echo", Args: []string{"tampered"}}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestDescribeWorkspaceDetectsGitAndStacks(t *testing.T) {
	root := t.TempDir()
	svc := newRegisteredService(t, root, "ws-describe")
	require.NoError(t, os.Mkdir(filepath.Join(root, "ws-describe", ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ws-describe", "go.mod"), []byte("module sample\n"), 0o600))
	resp, err := svc.DescribeWorkspace(context.Background(), &runnerv1.DescribeWorkspaceRequest{WorkspaceId: "ws-describe"})
	require.NoError(t, err)
	require.True(t, resp.IsGit)
	require.Contains(t, resp.DetectedStacks, "go")
}

func TestReadWorkspaceContextUsesConfiguredRootAndSafeFiles(t *testing.T) {
	root := t.TempDir()
	svc := newRegisteredService(t, root, "ws-context")
	require.NoError(t, os.Mkdir(filepath.Join(root, "ws-context", ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ws-context", "main.go"), []byte("package main\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ws-context", ".env"), []byte("SECRET=value\n"), 0o600))
	described, err := svc.DescribeWorkspace(context.Background(), &runnerv1.DescribeWorkspaceRequest{WorkspaceId: "ws-context"})
	require.NoError(t, err)

	resp, err := svc.ReadWorkspaceContext(context.Background(), &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "ws-context"})
	require.NoError(t, err)
	require.Equal(t, described.Root, resp.Root)
	require.Equal(t, described.IsGit, resp.IsGit)
	require.Equal(t, described.Dirty, resp.Dirty)
	require.Equal(t, []*runnerv1.WorkspaceFile{{Path: "main.go", Content: "package main\n"}}, resp.Files)
}

func TestReadWorkspaceContextMapsCanceledContextToCanceled(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "ws-canceled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.ReadWorkspaceContext(ctx, &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "ws-canceled"})
	require.Equal(t, codes.Canceled, status.Code(err))
}

func TestReadWorkspaceContextMapsExpiredContextToDeadlineExceeded(t *testing.T) {
	svc := newRegisteredService(t, t.TempDir(), "ws-deadline")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := svc.ReadWorkspaceContext(ctx, &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: "ws-deadline"})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
}
