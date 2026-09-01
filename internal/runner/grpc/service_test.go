package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/stretchr/testify/require"
)

func TestExecuteApprovedPlanAppliesPatchAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	svc, err := NewService(root)
	require.NoError(t, err)
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

func TestExecuteApprovedPlanRejectsTamperedApproval(t *testing.T) {
	svc, err := NewService(t.TempDir())
	require.NoError(t, err)
	_, err = svc.ExecuteApprovedPlan(context.Background(), &runnerv1.ExecuteApprovedPlanRequest{RequestId: "req-1", RunId: "run-1", WorkspaceId: "ws-1", PlanVersion: 1, ApprovalHash: "tampered"})
	require.Error(t, err)
}
