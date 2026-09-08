package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	runnergrpc "github.com/aicouncil/aicouncil/internal/runner/grpc"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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

func TestCouncilApprovalExecutionVerificationLifecycle(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "message.txt"), []byte("before\n"), 0o600))
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	runner, err := runnergrpc.NewService(root)
	require.NoError(t, err)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	runnerv1.RegisterWorkspaceRunnerServer(server, runner)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	before := sha256.Sum256([]byte("before\n"))
	workflow := council.NewWorkflow(
		council.NewEngine(provider.NewRegistry(&lifecycleProvider{name: "proposer", values: []any{schema.Proposal{ID: "proposal-1"}}}, &lifecycleProvider{name: "reviewer", values: []any{schema.PeerReview{Verdict: "approve"}}}, &lifecycleProvider{name: "judge", values: []any{schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: "message.txt", UnifiedDiff: "@@ -1 +1 @@\n-before\n+council\n", BeforeHash: hex.EncodeToString(before[:])}}, Commands: []schema.Command{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 10}}, VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"version"}, TimeoutSeconds: 10}}}}}}, &lifecycleProvider{name: "red-team", values: []any{schema.RedTeamReport{}}}), nil, council.Limits{}),
		[]council.Seat{{ID: "proposer", Provider: "proposer", Model: "test"}},
		[]council.Seat{{ID: "reviewer", Provider: "reviewer", Model: "test"}},
		council.Seat{ID: "judge", Provider: "judge", Model: "test"},
		council.Seat{ID: "red-team", Provider: "red-team", Model: "test"},
	)
	api := transport.NewPersistentAPI(db).WithRunnerClient(runnerv1.NewWorkspaceRunnerClient(connection)).WithCouncil(workflow)
	workspace := e2eCall(t, api, http.MethodPost, "/api/v1/workspaces", e2eJSON(t, map[string]any{"root": root}), "")
	require.Equal(t, http.StatusCreated, workspace.Code)
	workspaceID := e2eData(t, workspace)["id"].(string)
	created := e2eCall(t, api, http.MethodPost, "/api/v1/tasks", e2eJSON(t, map[string]any{"workspace_id": workspaceID, "requirement": "replace the message", "acceptance": []string{"message is council"}}), "")
	require.Equal(t, http.StatusCreated, created.Code)
	id := e2eData(t, created)["id"].(string)
	started := e2eCall(t, api, http.MethodPost, "/api/v1/tasks/"+id+"/start", "", id)
	require.Equal(t, http.StatusOK, started.Code)
	startedData := e2eData(t, started)
	approvalHash := startedData["approval_hash"].(string)
	approved := e2eCall(t, api, http.MethodPost, "/api/v1/tasks/"+id+"/approve", e2eJSON(t, map[string]any{"plan_version": 1, "approval_hash": approvalHash}), id)
	require.Equal(t, http.StatusOK, approved.Code)

	executed := e2eCall(t, api, http.MethodPost, "/api/v1/tasks/"+id+"/execute", "", id)
	require.Equal(t, http.StatusOK, executed.Code)
	require.Equal(t, "council\n", string(mustReadFile(t, filepath.Join(root, "message.txt"))))
	executedData := e2eData(t, executed)
	verification, ok := executedData["verification"].(map[string]any)
	require.True(t, ok)
	steps, ok := verification["steps"].([]any)
	require.True(t, ok)
	require.True(t, e2eHasSuccessfulStep(steps, "command"))
	require.True(t, e2eHasSuccessfulStep(steps, "verification"))

	readback := e2eCall(t, api, http.MethodGet, "/api/v1/tasks/"+id, "", id)
	require.Equal(t, http.StatusOK, readback.Code)
	require.Equal(t, "SUCCEEDED", e2eData(t, readback)["state"])
	second := e2eCall(t, api, http.MethodPost, "/api/v1/tasks/"+id+"/execute", "", id)
	require.Equal(t, http.StatusConflict, second.Code)
	require.Equal(t, "council\n", string(mustReadFile(t, filepath.Join(root, "message.txt"))))
}

type lifecycleProvider struct {
	name   string
	values []any
	next   int
}

func (p *lifecycleProvider) Name() string { return p.name }

func (p *lifecycleProvider) Generate(_ context.Context, _ provider.Request) (provider.Response, error) {
	encoded, err := json.Marshal(p.values[p.next])
	if err != nil {
		return provider.Response{}, err
	}
	p.next++
	return provider.Response{Content: encoded}, nil
}

func e2eCall(t *testing.T, api *transport.API, method, requestPath, body, id string) *httptest.ResponseRecorder {
	t.Helper()
	for _, route := range api.Routes() {
		if route.Method != method {
			continue
		}
		if (id == "" && route.Path == requestPath) || (id != "" && route.Path == "/api/v1/tasks/:id"+strings.TrimPrefix(requestPath, "/api/v1/tasks/"+id)) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, requestPath, bytes.NewBufferString(body))
			if id != "" {
				request = pathvar.WithVars(request, map[string]string{"id": id})
			}
			route.Handler(recorder, request)
			return recorder
		}
	}
	t.Fatalf("missing route %s %s", method, requestPath)
	return nil
}

func e2eData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

func e2eJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}

func e2eHasSuccessfulStep(steps []any, kind string) bool {
	for _, value := range steps {
		step, ok := value.(map[string]any)
		if ok && step["kind"] == kind && step["status"] == "SUCCEEDED" {
			return true
		}
	}
	return false
}
