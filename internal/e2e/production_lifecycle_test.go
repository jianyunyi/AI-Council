package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	runnergrpc "github.com/aicouncil/aicouncil/internal/runner/grpc"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestProductionLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "workspace")
	require.NoError(t, os.Mkdir(workspaceRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, "message.txt"), []byte("before\n"), 0o600))

	councilDB, err := sqlite.Open(filepath.Join(root, "council.sqlite"))
	require.NoError(t, err)
	councilSQL, err := councilDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = councilSQL.Close() })

	runnerDBPath := filepath.Join(root, "runner.sqlite")
	runnerDB, err := sqlite.Open(runnerDBPath)
	require.NoError(t, err)
	runnerSQL, err := runnerDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = runnerSQL.Close() })

	runner, err := runnergrpc.NewServiceWithDB(root, runnerDB)
	require.NoError(t, err)
	signer, err := approval.NewSigner(make([]byte, 32))
	require.NoError(t, err)
	runner = runner.WithSigner(signer)
	runnerConn, stopRunner := startProductionRunner(t, runner, "runner-e2e-token")

	before := sha256.Sum256([]byte("before\n"))
	plan := schema.ExecutionPlan{
		Version:    1,
		Acceptance: []string{"message contains council"},
		Patches: []schema.Patch{{
			Path:        "message.txt",
			UnifiedDiff: "@@ -1 +1 @@\n-before\n+council\n",
			BeforeHash:  hex.EncodeToString(before[:]),
		}},
		Commands:             []schema.Command{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 10}},
		VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 10}},
	}
	workflow := council.NewWorkflow(
		council.NewEngine(provider.NewRegistry(
			&lifecycleProvider{name: "proposer", values: []any{schema.Proposal{ID: "proposal-1"}}},
			&lifecycleProvider{name: "reviewer", values: []any{schema.PeerReview{Verdict: "approve"}}},
			&lifecycleProvider{name: "judge", values: []any{schema.CouncilDecision{Plan: plan}}},
			&lifecycleProvider{name: "red-team", values: []any{schema.RedTeamReport{}}},
		), nil, council.Limits{}),
		[]council.Seat{{ID: "proposer", Provider: "proposer", Model: "test"}},
		[]council.Seat{{ID: "reviewer", Provider: "reviewer", Model: "test"}},
		council.Seat{ID: "judge", Provider: "judge", Model: "test"},
		council.Seat{ID: "red-team", Provider: "red-team", Model: "test"},
	)

	auth := rbac.New(councilDB)
	adminToken, err := auth.BootstrapAdmin(ctx, "admin", "admin-e2e-password", time.Hour)
	require.NoError(t, err)
	adminIdentity, err := auth.Authenticate(ctx, adminToken)
	require.NoError(t, err)
	require.Contains(t, adminIdentity.Roles, "admin")

	require.NoError(t, auth.CreateRole(ctx, "workspace-member"))
	for _, permission := range []string{"workspace:read", "workspace:write", "task:read", "task:write", "task:approve", "task:execute"} {
		require.NoError(t, auth.GrantPermission(ctx, "workspace-member", permission))
	}
	for _, user := range []struct{ subject, token string }{
		{subject: "owner", token: "owner-e2e-token"},
		{subject: "member", token: "member-e2e-token"},
		{subject: "outsider", token: "outsider-e2e-token"},
	} {
		require.NoError(t, auth.CreateUser(ctx, user.subject, user.token))
		require.NoError(t, auth.GrantRole(ctx, user.subject, "workspace-member"))
	}

	api := transport.NewPersistentAPI(councilDB).
		WithRunnerClient(runnerv1.NewWorkspaceRunnerClient(runnerConn)).
		WithCouncil(workflow).
		WithApprovalSigner(signer).
		WithArtifactRoot(filepath.Join(root, "artifacts"))

	createdWorkspace := e2eCallAs(t, api, auth, "owner-e2e-token", http.MethodPost, "/api/v1/workspaces", e2eJSON(t, map[string]string{"root": "workspace"}))
	require.Equal(t, http.StatusCreated, createdWorkspace.Code)
	workspaceID := e2eData(t, createdWorkspace)["id"].(string)
	grant := e2eCallAs(t, api, auth, "owner-e2e-token", http.MethodPut, "/api/v1/workspaces/"+workspaceID+"/shares/member", e2eJSON(t, map[string]string{"access": "editor"}))
	require.Equal(t, http.StatusNoContent, grant.Code)
	require.Equal(t, http.StatusNotFound, e2eCallAs(t, api, auth, "outsider-e2e-token", http.MethodGet, "/api/v1/workspaces/"+workspaceID, "").Code)

	createdTask := e2eCallAs(t, api, auth, "member-e2e-token", http.MethodPost, "/api/v1/tasks", e2eJSON(t, map[string]any{
		"workspace_id": workspaceID,
		"requirement":  "replace the message after human approval",
		"acceptance":   plan.Acceptance,
	}))
	require.Equal(t, http.StatusCreated, createdTask.Code)
	taskID := e2eData(t, createdTask)["id"].(string)
	require.Equal(t, http.StatusForbidden, e2eCallAs(t, api, auth, "member-e2e-token", http.MethodPost, "/api/v1/tasks/"+taskID+"/execute", "").Code)
	require.Equal(t, http.StatusNotFound, e2eCallAs(t, api, auth, "outsider-e2e-token", http.MethodGet, "/api/v1/tasks/"+taskID, "").Code)
	require.Equal(t, http.StatusNotFound, e2eCallAs(t, api, auth, "outsider-e2e-token", http.MethodPost, "/api/v1/tasks/"+taskID+"/execute", "").Code)

	started := e2eCallAs(t, api, auth, "member-e2e-token", http.MethodPost, "/api/v1/tasks/"+taskID+"/start", "")
	require.Equal(t, http.StatusOK, started.Code)
	startedData := e2eData(t, started)
	approvalHash := startedData["approval_hash"].(string)
	require.Equal(t, "AWAITING_APPROVAL", startedData["state"])

	approved := e2eCallAs(t, api, auth, "owner-e2e-token", http.MethodPost, "/api/v1/tasks/"+taskID+"/approve", e2eJSON(t, map[string]any{"plan_version": 1, "approval_hash": approvalHash}))
	require.Equal(t, http.StatusOK, approved.Code)
	executed := e2eCallAs(t, api, auth, "member-e2e-token", http.MethodPost, "/api/v1/tasks/"+taskID+"/execute", "")
	require.Equal(t, http.StatusOK, executed.Code)
	require.Equal(t, "council\n", string(mustReadFile(t, filepath.Join(workspaceRoot, "message.txt"))))
	executedData := e2eData(t, executed)
	require.Equal(t, "SUCCEEDED", executedData["state"])
	verification, ok := executedData["verification"].(map[string]any)
	require.True(t, ok)
	steps, ok := verification["steps"].([]any)
	require.True(t, ok)
	require.True(t, e2eHasSuccessfulStep(steps, "command"))
	require.True(t, e2eHasSuccessfulStep(steps, "verification"))
	artifactID, ok := executedData["verification_artifact_id"].(string)
	require.True(t, ok, "successful Runner verification must have a retrievable artifact ID")
	artifact := e2eCallAs(t, api, auth, "member-e2e-token", http.MethodGet, "/api/v1/tasks/"+taskID+"/artifacts/"+artifactID, "")
	require.Equal(t, http.StatusOK, artifact.Code)
	require.Equal(t, "runner.verification", e2eData(t, artifact)["type"])
	require.Equal(t, http.StatusNotFound, e2eCallAs(t, api, auth, "outsider-e2e-token", http.MethodGet, "/api/v1/tasks/"+taskID+"/artifacts/"+artifactID, "").Code)

	request := productionExecutionRequest(taskID, workspaceID, approvalHash, plan)
	stopRunner()
	require.NoError(t, runnerSQL.Close())

	restartedDB, err := sqlite.Open(runnerDBPath)
	require.NoError(t, err)
	restartedSQL, err := restartedDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = restartedSQL.Close() })
	restartedRunner, err := runnergrpc.NewServiceWithDB(root, restartedDB)
	require.NoError(t, err)
	restartedConn, stopRestartedRunner := startProductionRunner(t, restartedRunner, "runner-e2e-token")
	t.Cleanup(stopRestartedRunner)
	replayed, err := runnerv1.NewWorkspaceRunnerClient(restartedConn).ExecuteApprovedPlan(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", replayed.Status)
	require.Equal(t, "council\n", string(mustReadFile(t, filepath.Join(workspaceRoot, "message.txt"))), "replayed idempotency key must not apply the patch twice")

	require.Equal(t, http.StatusOK, e2eCallAs(t, api, auth, "owner-e2e-token", http.MethodGet, "/api/v1/workspaces/"+workspaceID, "").Code)
}

func startProductionRunner(t *testing.T, runner *runnergrpc.Service, token string) (*grpc.ClientConn, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(runnergrpc.UnaryAuthInterceptor(token))
	runnerv1.RegisterWorkspaceRunnerServer(server, runner)
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, request, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), method, request, reply, cc, options...)
		}),
	)
	require.NoError(t, err)
	stop := func() {
		server.Stop()
		_ = connection.Close()
		_ = listener.Close()
	}
	t.Cleanup(stop)
	return connection, stop
}

func productionExecutionRequest(taskID, workspaceID, approvalHash string, plan schema.ExecutionPlan) *runnerv1.ExecuteApprovedPlanRequest {
	request := &runnerv1.ExecuteApprovedPlanRequest{
		RequestId: taskID + ":" + "1", RunId: taskID, WorkspaceId: workspaceID,
		PlanVersion: int32(plan.Version), ApprovalHash: approvalHash, Acceptance: append([]string(nil), plan.Acceptance...),
	}
	for _, patch := range plan.Patches {
		request.Patches = append(request.Patches, &runnerv1.ApprovedPatch{Path: patch.Path, UnifiedDiff: patch.UnifiedDiff, BeforeHash: patch.BeforeHash})
	}
	for _, command := range plan.Commands {
		request.Commands = append(request.Commands, &runnerv1.ApprovedCommand{Executable: command.Executable, Args: command.Args, WorkDir: command.WorkDir, TimeoutSeconds: int32(command.TimeoutSeconds), Purpose: command.Purpose})
	}
	for _, command := range plan.VerificationCommands {
		request.VerificationCommands = append(request.VerificationCommands, &runnerv1.ApprovedCommand{Executable: command.Executable, Args: command.Args, WorkDir: command.WorkDir, TimeoutSeconds: int32(command.TimeoutSeconds), Purpose: command.Purpose})
	}
	return request
}
