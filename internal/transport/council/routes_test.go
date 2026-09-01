package council

import (
	"bytes"
	"context"
	"encoding/json"
	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
)

type routeCouncil struct{ analyzed, deliberated bool }

func (c *routeCouncil) Analyze(context.Context, string) error { c.analyzed = true; return nil }
func (c *routeCouncil) Deliberate(context.Context, string) (schema.ExecutionPlan, error) {
	c.deliberated = true
	return schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"ok"}}}}, nil
}
func (c *routeCouncil) ReviewExecution(context.Context, string, schema.VerificationReport) error {
	return nil
}

var _ appTask.CouncilPort = (*routeCouncil)(nil)

type routeRunner struct {
	req *runnerv1.ExecuteApprovedPlanRequest
}

func (r *routeRunner) DescribeWorkspace(context.Context, *runnerv1.DescribeWorkspaceRequest, ...grpc.CallOption) (*runnerv1.DescribeWorkspaceResponse, error) {
	return &runnerv1.DescribeWorkspaceResponse{Root: "normalized", IsGit: true, Dirty: true}, nil
}

func (r *routeRunner) ExecuteApprovedPlan(_ context.Context, req *runnerv1.ExecuteApprovedPlanRequest, _ ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	r.req = req
	return &runnerv1.ExecuteApprovedPlanResponse{RequestId: req.RequestId, Status: "SUCCEEDED"}, nil
}

func TestPersistentAPIRehydratesTasksAndEvents(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := NewPersistentAPI(db)
	routes := a.Routes()
	var create func(http.ResponseWriter, *http.Request)
	for _, r := range routes {
		if r.Method == http.MethodPost && r.Path == "/api/v1/tasks" {
			create = r.Handler
		}
	}
	rec := httptest.NewRecorder()
	create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws","requirement":"persist","acceptance":["ok"]}`)))
	require.Equal(t, 201, rec.Code)
	var body responseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	m := body.Data.(map[string]any)
	id := m["id"].(string)
	b := NewPersistentAPI(db)
	require.Contains(t, b.tasks, id)
	ev, err := b.eventRepo.After(context.Background(), id, 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, ev)
}

func TestPersistentAPIRehydratesWorkspaceFacts(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := NewPersistentAPI(db).WithRunnerClient(&routeRunner{})
	var create func(http.ResponseWriter, *http.Request)
	for _, r := range a.Routes() {
		if r.Method == http.MethodPost && r.Path == "/api/v1/workspaces" {
			create = r.Handler
		}
	}
	rec := httptest.NewRecorder()
	create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewBufferString(`{"root":"/workspace"}`)))
	require.Equal(t, http.StatusCreated, rec.Code)
	b := NewPersistentAPI(db)
	require.Len(t, b.workspaces, 1)
	for _, ws := range b.workspaces {
		require.Equal(t, "normalized", ws.Root)
		require.True(t, ws.IsGit)
		require.True(t, ws.Dirty)
	}
}

func TestPersistentAPIRestoresSequenceAcrossRestart(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	create := func(a *API) string {
		var handler func(http.ResponseWriter, *http.Request)
		for _, route := range a.Routes() {
			if route.Method == http.MethodPost && route.Path == "/api/v1/tasks" {
				handler = route.Handler
			}
		}
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws","requirement":"r","acceptance":["ok"]}`)))
		var env responseEnvelope
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
		return env.Data.(map[string]any)["id"].(string)
	}
	first := create(NewPersistentAPI(db))
	second := create(NewPersistentAPI(db))
	require.Equal(t, "task-1", first)
	require.Equal(t, "task-2", second)
}

func TestTaskLifecycleRequiresApproval(t *testing.T) {
	a := NewAPI()
	routes := a.Routes()
	find := func(method, path string) func(http.ResponseWriter, *http.Request) {
		for _, x := range routes {
			if x.Method == method && x.Path == path {
				return x.Handler
			}
		}
		t.Fatalf("missing route %s %s", method, path)
		return nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws-1","requirement":"add ready","acceptance":["tests pass"]}`))
	find(http.MethodPost, "/api/v1/tasks")(rec, req)
	require.Equal(t, 201, rec.Code)
	var created responseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	taskMap := created.Data.(map[string]any)
	id := taskMap["id"].(string)
	withID := func(h func(http.ResponseWriter, *http.Request)) (int, string) {
		r := httptest.NewRecorder()
		q := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, nil), map[string]string{"id": id})
		h(r, q)
		return r.Code, r.Body.String()
	}
	code, _ := withID(find(http.MethodPost, "/api/v1/tasks/:id/execute"))
	require.Equal(t, 403, code)
	code, startBody := withID(find(http.MethodPost, "/api/v1/tasks/:id/start"))
	require.Equal(t, 200, code)
	code, _ = withID(find(http.MethodPost, "/api/v1/tasks/:id/approve"))
	require.Equal(t, 400, code)
	r := httptest.NewRecorder()
	q := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, nil), map[string]string{"id": id})
	q.Body = http.NoBody // approval body is supplied below
	var started responseEnvelope
	require.NoError(t, json.Unmarshal([]byte(startBody), &started))
	startedTask := started.Data.(map[string]any)
	approvalHash := startedTask["approval_hash"].(string)
	q = pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id, bytes.NewBufferString(`{"plan_version":1,"approval_hash":"`+approvalHash+`"}`)), map[string]string{"id": id})
	find(http.MethodPost, "/api/v1/tasks/:id/approve")(r, q)
	require.Equal(t, 200, r.Code)
	code, _ = withID(find(http.MethodPost, "/api/v1/tasks/:id/execute"))
	require.Equal(t, 200, code)
}

func TestPersistentAPIExecutesCouncilPlanThroughRunner(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	council := &routeCouncil{}
	runner := &routeRunner{}
	a := NewPersistentAPI(db).WithCouncil(council).WithRunnerClient(runner)
	find := func(method, path string) func(http.ResponseWriter, *http.Request) {
		for _, x := range a.Routes() {
			if x.Method == method && x.Path == path {
				return x.Handler
			}
		}
		t.Fatalf("missing route %s %s", method, path)
		return nil
	}
	create := httptest.NewRecorder()
	find(http.MethodPost, "/api/v1/tasks")(create, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"ws","requirement":"ship","acceptance":["ok"]}`)))
	var envelope responseEnvelope
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &envelope))
	id := envelope.Data.(map[string]any)["id"].(string)
	withID := func(method, path string, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := pathvar.WithVars(httptest.NewRequest(method, "/api/v1/tasks/"+id, bytes.NewBufferString(body)), map[string]string{"id": id})
		find(method, path)(rec, req)
		return rec
	}
	start := withID(http.MethodPost, "/api/v1/tasks/:id/start", "")
	require.Equal(t, 200, start.Code)
	var started responseEnvelope
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &started))
	approvalHash := started.Data.(map[string]any)["approval_hash"].(string)
	require.True(t, council.analyzed)
	require.True(t, council.deliberated)
	require.Equal(t, 200, withID(http.MethodPost, "/api/v1/tasks/:id/approve", `{"plan_version":1,"approval_hash":"`+approvalHash+`"}`).Code)
	require.Equal(t, 200, withID(http.MethodPost, "/api/v1/tasks/:id/execute", "").Code)
	require.NotNil(t, runner.req)
	require.Equal(t, int32(1), runner.req.PlanVersion)
	require.Equal(t, []string{"echo", "ok"}, append([]string{runner.req.Commands[0].Executable}, runner.req.Commands[0].Args...))
	reloaded := NewPersistentAPI(db)
	require.NotNil(t, reloaded.tasks[id].Verification)
}
