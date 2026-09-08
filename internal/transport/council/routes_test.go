package council

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type routeCouncil struct {
	analyzed, deliberated bool
	workspaceFiles        []schema.WorkspaceFile
	plan                  schema.ExecutionPlan
	analyzeErr            error
	deliberateErr         error
}

func (c *routeCouncil) Analyze(context.Context, string) error { c.analyzed = true; return c.analyzeErr }
func (c *routeCouncil) Deliberate(context.Context, string) (schema.ExecutionPlan, error) {
	c.deliberated = true
	if c.deliberateErr != nil {
		return schema.ExecutionPlan{}, c.deliberateErr
	}
	if c.plan.Version != 0 {
		return c.plan, nil
	}
	return schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "echo", Args: []string{"ok"}}}}, nil
}
func (c *routeCouncil) DeliberateWithWorkspace(ctx context.Context, requirement string, files []schema.WorkspaceFile) (schema.ExecutionPlan, error) {
	c.workspaceFiles = append([]schema.WorkspaceFile(nil), files...)
	return c.Deliberate(ctx, requirement)
}
func (c *routeCouncil) ReviewExecution(context.Context, string, schema.VerificationReport) error {
	return nil
}

var _ appTask.CouncilPort = (*routeCouncil)(nil)

type routeRunner struct {
	req      *runnerv1.ExecuteApprovedPlanRequest
	calls    int
	response *runnerv1.ExecuteApprovedPlanResponse
	err      error
	context  *runnerv1.ReadWorkspaceContextResponse
}

func (r *routeRunner) DescribeWorkspace(context.Context, *runnerv1.DescribeWorkspaceRequest, ...grpc.CallOption) (*runnerv1.DescribeWorkspaceResponse, error) {
	return &runnerv1.DescribeWorkspaceResponse{Root: "normalized", IsGit: true, Dirty: true}, nil
}

func (r *routeRunner) ReadWorkspaceContext(context.Context, *runnerv1.ReadWorkspaceContextRequest, ...grpc.CallOption) (*runnerv1.ReadWorkspaceContextResponse, error) {
	if r.context != nil {
		return r.context, nil
	}
	return &runnerv1.ReadWorkspaceContextResponse{}, nil
}

func (r *routeRunner) ExecuteApprovedPlan(_ context.Context, req *runnerv1.ExecuteApprovedPlanRequest, _ ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	r.calls++
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	if r.response != nil {
		return r.response, nil
	}
	return &runnerv1.ExecuteApprovedPlanResponse{RequestId: req.RequestId, Status: "SUCCEEDED"}, nil
}

func registerWorkspace(a *API, id string) {
	a.workspaces[id] = workspace{ID: id, Root: "/workspace"}
}

func TestPersistentAPIRehydratesTasksAndEvents(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := NewPersistentAPI(db)
	registerWorkspace(a, "ws")
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

func TestCreateTaskRejectsUnknownWorkspace(t *testing.T) {
	a := NewAPI()
	var create func(http.ResponseWriter, *http.Request)
	for _, route := range a.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/v1/tasks" {
			create = route.Handler
		}
	}
	rec := httptest.NewRecorder()
	create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"workspace_id":"missing","requirement":"ship","acceptance":["ok"]}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unknown_workspace")
}

func TestCreateWorkspaceReusesRegisteredRoot(t *testing.T) {
	a := NewAPI()
	var create func(http.ResponseWriter, *http.Request)
	for _, route := range a.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/v1/workspaces" {
			create = route.Handler
		}
	}
	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		create(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewBufferString(`{"root":"/workspace"}`)))
		return rec
	}
	first, second := request(), request()
	require.Equal(t, http.StatusCreated, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	var firstEnvelope, secondEnvelope responseEnvelope
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstEnvelope))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondEnvelope))
	require.Equal(t, firstEnvelope.Data.(map[string]any)["id"], secondEnvelope.Data.(map[string]any)["id"])
}

func TestPersistentAPIRestoresSequenceAcrossRestart(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	create := func(a *API) string {
		registerWorkspace(a, "ws")
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
	a := NewAPI().WithRunnerClient(&routeRunner{})
	registerWorkspace(a, "ws-1")
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
	registerWorkspace(a, "ws")
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

func TestPersistentAPIConsumesApprovalBeforeExecutingOnce(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runner := &routeRunner{}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	var execute func(http.ResponseWriter, *http.Request)
	for _, route := range a.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/v1/tasks/:id/execute" {
			execute = route.Handler
		}
	}
	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"})
		execute(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, request().Code)
	require.Equal(t, http.StatusConflict, request().Code)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, "task-1:1", runner.req.RequestId)
}

func TestPersistentAPIStartPassesRunnerWorkspaceContextToCouncil(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	council := &routeCouncil{}
	runner := &routeRunner{context: &runnerv1.ReadWorkspaceContextResponse{Files: []*runnerv1.WorkspaceFile{{Path: "main.go", Content: "package main"}}}}
	a := NewPersistentAPI(db).WithCouncil(council).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "DRAFT", WorkspaceID: "ws", Requirement: "ship", Acceptance: []string{"ok"}, PlanVersion: 1}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", Requirement: "ship", State: "DRAFT", PlanVersion: 1}).Error)

	start := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/start")
	rec := httptest.NewRecorder()
	start(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/start", nil), map[string]string{"id": "task-1"}))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []schema.WorkspaceFile{{Path: "main.go", Content: "package main"}}, council.workspaceFiles)
	require.Equal(t, "AWAITING_APPROVAL", a.tasks["task-1"].State)
	events, err := a.eventRepo.After(context.Background(), "task-1", 0, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"state.changed", "state.changed"}, []string{events[0].Type, events[1].Type})
}

func TestPersistentAPIExecuteForwardsVerificationCommands(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runner := &routeRunner{}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	plan := schema.ExecutionPlan{Version: 1, VerificationCommands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true, Plan: plan}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	execute := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	rec := httptest.NewRecorder()
	execute(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"}))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, runner.req.VerificationCommands, 1)
	require.Equal(t, "go", runner.req.VerificationCommands[0].Executable)
	require.Equal(t, []string{"test", "./..."}, runner.req.VerificationCommands[0].Args)
}

func TestPersistentAPIExecutionFailurePersistsFailed(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runner := &routeRunner{response: &runnerv1.ExecuteApprovedPlanResponse{Status: "FAILED", ErrorCode: "command_failed"}}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	var execute func(http.ResponseWriter, *http.Request)
	for _, route := range a.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/v1/tasks/:id/execute" {
			execute = route.Handler
		}
	}
	rec := httptest.NewRecorder()
	req := pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"})
	execute(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "FAILED", a.tasks["task-1"].State)
	var stored storage.TaskRecord
	require.NoError(t, db.First(&stored, "id = ?", "task-1").Error)
	require.Equal(t, "FAILED", stored.State)
}

func TestPersistentAPIExecuteRequiresRunnerBeforeConsumingApproval(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := NewPersistentAPI(db)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	execute := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	rec := httptest.NewRecorder()
	execute(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"}))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "AWAITING_APPROVAL", a.tasks["task-1"].State)
	var storedTask storage.TaskRecord
	require.NoError(t, db.First(&storedTask, "id = ?", "task-1").Error)
	require.Equal(t, "AWAITING_APPROVAL", storedTask.State)
	var storedApproval storage.ApprovalRecord
	require.NoError(t, db.First(&storedApproval, "id = ?", "approval-1").Error)
	require.Nil(t, storedApproval.ConsumedAt)
}

func TestPersistentAPIRetriesExecutingTaskAfterRunnerTransportError(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runner := &routeRunner{err: errors.New("runner connection reset")}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "AWAITING_APPROVAL", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "AWAITING_APPROVAL", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)
	require.NoError(t, a.approvalRepo.Save(context.Background(), storage.ApprovalRecord{ID: "approval-1", RunID: "task-1", PlanVersion: 1, SnapshotHash: "hash", Decision: "approved", Actor: "user"}))

	execute := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		execute(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"}))
		return rec
	}

	require.Equal(t, http.StatusBadGateway, call().Code)
	require.Equal(t, "EXECUTING", a.tasks["task-1"].State)
	var storedTask storage.TaskRecord
	require.NoError(t, db.First(&storedTask, "id = ?", "task-1").Error)
	require.Equal(t, "EXECUTING", storedTask.State)
	runner.err = nil
	require.Equal(t, http.StatusOK, call().Code)
	require.Equal(t, 2, runner.calls)
	require.Equal(t, "SUCCEEDED", a.tasks["task-1"].State)
}

func TestPersistentAPIResumesPersistedExecutingTaskWithStableRequestID(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	runner := &routeRunner{}
	a := NewPersistentAPI(db).WithRunnerClient(runner)
	registerWorkspace(a, "ws")
	a.tasks["task-1"] = &task{ID: "task-1", State: "EXECUTING", WorkspaceID: "ws", PlanVersion: 1, ApprovalHash: "hash", Approved: true}
	require.NoError(t, db.Create(&storage.TaskRecord{ID: "task-1", WorkspaceID: "ws", State: "EXECUTING", PlanVersion: 1, ApprovalHash: "hash", ApprovalGranted: true}).Error)

	execute := routeHandler(t, a.Routes(), http.MethodPost, "/api/v1/tasks/:id/execute")
	rec := httptest.NewRecorder()
	execute(rec, pathvar.WithVars(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/execute", nil), map[string]string{"id": "task-1"}))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "task-1:1", runner.req.RequestId)
	require.Equal(t, "SUCCEEDED", a.tasks["task-1"].State)
}
