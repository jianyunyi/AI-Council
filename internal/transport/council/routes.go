package council

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

type API struct {
	mu           sync.Mutex
	tasks        map[string]*task
	nextID       int64
	workspaces   map[string]workspace
	db           *gorm.DB
	eventRepo    *sqlite.EventRepository
	approvalRepo *sqlite.ApprovalRepository
	artifactRepo *sqlite.ArtifactRepository
	runnerClient runClient
	council      appTask.CouncilPort
	metrics      *metrics.Metrics
}
type runClient interface {
	ExecuteApprovedPlan(context.Context, *runnerv1.ExecuteApprovedPlanRequest, ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error)
}
type workspaceDescriber interface {
	DescribeWorkspace(context.Context, *runnerv1.DescribeWorkspaceRequest, ...grpc.CallOption) (*runnerv1.DescribeWorkspaceResponse, error)
}
type workspace struct {
	ID    string `json:"id"`
	Root  string `json:"root"`
	IsGit bool   `json:"is_git"`
	Dirty bool   `json:"dirty"`
}
type task struct {
	ID           string                                `json:"id"`
	State        string                                `json:"state"`
	WorkspaceID  string                                `json:"workspace_id"`
	Requirement  string                                `json:"requirement"`
	Acceptance   []string                              `json:"acceptance"`
	PlanVersion  int                                   `json:"plan_version"`
	ApprovalHash string                                `json:"approval_hash,omitempty"`
	Approved     bool                                  `json:"approved"`
	Plan         schema.ExecutionPlan                  `json:"plan,omitempty"`
	Verification *runnerv1.ExecuteApprovedPlanResponse `json:"verification,omitempty"`
	Events       []Event                               `json:"-"`
}
type Event struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Data      any       `json:"data"`
	CreatedAt time.Time `json:"created_at"`
}
type responseEnvelope struct {
	Data      any       `json:"data"`
	Error     *apiError `json:"error,omitempty"`
	RequestID string    `json:"request_id"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type createTaskRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	Requirement string   `json:"requirement"`
	Acceptance  []string `json:"acceptance"`
}
type providerTestRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}
type workspaceRequest struct {
	Root string `json:"root"`
}
type approvalRequest struct {
	PlanVersion  int    `json:"plan_version"`
	ApprovalHash string `json:"approval_hash"`
}

func NewAPI() *API {
	return &API{tasks: map[string]*task{}, workspaces: map[string]workspace{}, metrics: metrics.New()}
}
func NewPersistentAPI(db *gorm.DB) *API {
	a := NewAPI()
	a.db = db
	a.eventRepo = sqlite.NewEventRepository(db)
	a.approvalRepo = sqlite.NewApprovalRepository(db)
	a.artifactRepo = sqlite.NewArtifactRepository(db, ".data/artifacts")
	var rows []sqlite.TaskRecord
	_ = db.Find(&rows).Error
	var workspaceRows []sqlite.WorkspaceRecord
	_ = db.Find(&workspaceRows).Error
	for _, r := range workspaceRows {
		a.workspaces[r.ID] = workspace{ID: r.ID, Root: r.CanonicalRoot, IsGit: r.IsGit, Dirty: r.Dirty}
	}
	for _, r := range rows {
		var acceptance []string
		_ = json.Unmarshal(r.AcceptanceJSON, &acceptance)
		state := r.State
		if state == "" {
			state = "DRAFT"
		}
		var plan schema.ExecutionPlan
		_ = json.Unmarshal(r.PlanJSON, &plan)
		var verification runnerv1.ExecuteApprovedPlanResponse
		_ = json.Unmarshal(r.VerificationJSON, &verification)
		var v *runnerv1.ExecuteApprovedPlanResponse
		if verification.RequestId != "" {
			v = &verification
		}
		a.tasks[r.ID] = &task{ID: r.ID, State: state, WorkspaceID: r.WorkspaceID, Requirement: r.Requirement, Acceptance: acceptance, PlanVersion: r.PlanVersion, ApprovalHash: r.ApprovalHash, Approved: r.ApprovalGranted, Plan: plan, Verification: v}
	}
	for id := range a.tasks {
		if n, err := strconv.ParseInt(strings.TrimPrefix(id, "task-"), 10, 64); err == nil && n > a.nextID {
			a.nextID = n
		}
	}
	for id := range a.workspaces {
		if n, err := strconv.ParseInt(strings.TrimPrefix(id, "workspace-"), 10, 64); err == nil && n > a.nextID {
			a.nextID = n
		}
	}
	return a
}
func (a *API) WithArtifactRoot(root string) *API {
	if a.db != nil {
		a.artifactRepo = sqlite.NewArtifactRepository(a.db, root)
	}
	return a
}
func (a *API) WithRunnerClient(client runClient) *API       { a.runnerClient = client; return a }
func (a *API) WithCouncil(council appTask.CouncilPort) *API { a.council = council; return a }
func (a *API) Routes() []rest.Route {
	return []rest.Route{{Method: http.MethodPost, Path: "/api/v1/providers/test", Handler: a.testProvider}, {Method: http.MethodPost, Path: "/api/v1/workspaces", Handler: a.createWorkspace}, {Method: http.MethodGet, Path: "/api/v1/workspaces", Handler: a.listWorkspaces}, {Method: http.MethodPost, Path: "/api/v1/tasks", Handler: a.createTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id", Handler: a.getTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/start", Handler: a.startTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/approve", Handler: a.approveTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/reject", Handler: a.rejectTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/execute", Handler: a.executeTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/cancel", Handler: a.cancelTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/artifacts/:artifactId", Handler: a.getArtifact}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/events", Handler: a.events}}
}

func (a *API) testProvider(w http.ResponseWriter, r *http.Request) {
	var in providerTestRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Provider == "" || in.Model == "" {
		writeErr(w, 400, "invalid_provider", "provider and model are required")
		return
	}
	writeData(w, 200, map[string]string{"profile_id": "profile-" + strconv.FormatInt(time.Now().UnixNano(), 10), "provider": in.Provider, "model": in.Model, "status": "ok"})
}
func (a *API) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in workspaceRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Root) == "" {
		writeErr(w, 400, "invalid_workspace", "root is required")
		return
	}
	root := filepath.Clean(strings.TrimSpace(in.Root))
	a.mu.Lock()
	for _, existing := range a.workspaces {
		if existing.Root == root {
			a.mu.Unlock()
			writeData(w, http.StatusOK, existing)
			return
		}
	}
	a.nextID++
	id := "workspace-" + strconv.FormatInt(a.nextID, 10)
	ws := workspace{ID: id, Root: root, IsGit: false}
	if describer, ok := a.runnerClient.(workspaceDescriber); ok {
		if info, err := describer.DescribeWorkspace(r.Context(), &runnerv1.DescribeWorkspaceRequest{WorkspaceId: id}); err == nil {
			ws.Root, ws.IsGit, ws.Dirty = info.Root, info.IsGit, info.Dirty
		}
	}
	a.workspaces[id] = ws
	if a.db != nil {
		_ = a.db.Save(&sqlite.WorkspaceRecord{ID: id, CanonicalRoot: ws.Root, IsGit: ws.IsGit, Dirty: ws.Dirty}).Error
	}
	a.mu.Unlock()
	writeData(w, 201, ws)
}
func (a *API) listWorkspaces(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]workspace, 0, len(a.workspaces))
	for _, ws := range a.workspaces {
		out = append(out, ws)
	}
	writeData(w, 200, out)
}
func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	var in createTaskRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Requirement) == "" || len(in.Acceptance) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_task", "requirement and acceptance are required")
		return
	}
	a.mu.Lock()
	if _, ok := a.workspaces[in.WorkspaceID]; !ok {
		a.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "unknown_workspace", "workspace must be registered before creating a task")
		return
	}
	a.nextID++
	id := "task-" + strconv.FormatInt(a.nextID, 10)
	t := &task{ID: id, State: "DRAFT", WorkspaceID: in.WorkspaceID, Requirement: in.Requirement, Acceptance: in.Acceptance, PlanVersion: 1}
	t.Plan.Version = t.PlanVersion
	a.tasks[id] = t
	a.metrics.TasksCreated.Add(1)
	if a.db != nil {
		raw, _ := json.Marshal(in.Acceptance)
		_ = a.db.Save(&sqlite.TaskRecord{ID: id, WorkspaceID: in.WorkspaceID, Requirement: in.Requirement, AcceptanceJSON: raw, State: t.State, PlanVersion: t.PlanVersion}).Error
	}
	a.append(t, "task.created", t)
	a.mu.Unlock()
	writeData(w, http.StatusCreated, t)
}
func taskID(r *http.Request) string { return pathvar.Vars(r)["id"] }
func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	writeData(w, http.StatusOK, t)
}
func (a *API) startTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	t := a.tasks[taskID(r)]
	if t == nil {
		a.mu.Unlock()
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if t.State != "DRAFT" {
		a.mu.Unlock()
		writeErr(w, 409, "invalid_state", "task already started")
		return
	}
	requirement := t.Requirement
	a.mu.Unlock()
	var plan schema.ExecutionPlan
	if a.council != nil {
		a.metrics.CouncilRuns.Add(1)
		if err := a.council.Analyze(r.Context(), requirement); err != nil {
			a.metrics.CouncilFailures.Add(1)
			writeErr(w, http.StatusBadGateway, "council_unavailable", err.Error())
			return
		}
		var err error
		plan, err = a.council.Deliberate(r.Context(), requirement)
		if err != nil {
			a.metrics.CouncilFailures.Add(1)
			writeErr(w, http.StatusBadGateway, "council_failed", err.Error())
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.State != "DRAFT" {
		writeErr(w, 409, "invalid_state", "task already started")
		return
	}
	if a.council != nil {
		t.Plan = plan
		if t.Plan.Version == 0 {
			t.Plan.Version = t.PlanVersion
		} else {
			t.PlanVersion = t.Plan.Version
		}
		if len(t.Plan.Acceptance) == 0 {
			t.Plan.Acceptance = append([]string(nil), t.Acceptance...)
		}
	}
	hash, err := approval.Hash(t.ID, t.WorkspaceID, t.Plan)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plan_hash_failed", err.Error())
		return
	}
	t.ApprovalHash = hash
	for _, s := range []string{"ANALYZING", "PROPOSING", "REVIEWING", "JUDGING", "REDTEAM", "AWAITING_APPROVAL"} {
		t.State = s
		a.append(t, "state.changed", map[string]string{"state": s})
		a.persistTask(t)
	}
	writeData(w, 200, t)
}
func (a *API) approveTask(w http.ResponseWriter, r *http.Request) {
	var in approvalRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ApprovalHash == "" {
		writeErr(w, 400, "invalid_approval", "approval_hash is required")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if t.State != "AWAITING_APPROVAL" || in.PlanVersion != t.PlanVersion {
		writeErr(w, 409, "invalid_state", "task is not awaiting matching approval")
		return
	}
	if in.ApprovalHash != t.ApprovalHash {
		writeErr(w, http.StatusConflict, "approval_mismatch", "approval hash does not match immutable plan")
		return
	}
	t.ApprovalHash = in.ApprovalHash
	t.Approved = true
	if a.approvalRepo != nil {
		_ = a.approvalRepo.Invalidate(r.Context(), t.ID)
		_ = a.approvalRepo.Save(r.Context(), sqlite.ApprovalRecord{ID: strconv.FormatInt(time.Now().UnixNano(), 10), RunID: t.ID, PlanVersion: in.PlanVersion, SnapshotHash: in.ApprovalHash, Decision: "approved", Actor: "user", CreatedAt: time.Now().UTC()})
	}
	a.persistTask(t)
	a.append(t, "approval.created", in)
	writeData(w, 200, t)
}
func (a *API) rejectTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	t.State = "CANCELLED"
	t.Approved = false
	a.persistTask(t)
	a.append(t, "approval.rejected", map[string]string{"state": t.State})
	writeData(w, 200, t)
}
func (a *API) getArtifact(w http.ResponseWriter, r *http.Request) {
	if a.artifactRepo == nil {
		writeErr(w, 404, "not_found", "artifact not found")
		return
	}
	env, err := a.artifactRepo.Load(r.Context(), pathvar.Vars(r)["artifactId"])
	if err != nil {
		writeErr(w, 404, "not_found", "artifact not found")
		return
	}
	writeData(w, 200, env)
}
func (a *API) executeTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if !t.Approved {
		writeErr(w, 403, "approval_required", "manual approval required")
		return
	}
	if a.runnerClient != nil {
		a.metrics.Executions.Add(1)
		req := &runnerv1.ExecuteApprovedPlanRequest{RequestId: "rest-" + strconv.FormatInt(time.Now().UnixNano(), 10), RunId: t.ID, WorkspaceId: t.WorkspaceID, PlanVersion: int32(t.PlanVersion), ApprovalHash: t.ApprovalHash, Acceptance: append([]string(nil), t.Plan.Acceptance...)}
		for _, p := range t.Plan.Patches {
			req.Patches = append(req.Patches, &runnerv1.ApprovedPatch{Path: p.Path, UnifiedDiff: p.UnifiedDiff, BeforeHash: p.BeforeHash})
		}
		for _, c := range t.Plan.Commands {
			req.Commands = append(req.Commands, &runnerv1.ApprovedCommand{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int32(c.TimeoutSeconds), Purpose: c.Purpose})
		}
		resp, err := a.runnerClient.ExecuteApprovedPlan(r.Context(), req)
		if err != nil {
			a.metrics.ExecutionFailures.Add(1)
			writeErr(w, 502, "runner_unavailable", err.Error())
			return
		}
		if resp.Status != "SUCCEEDED" {
			a.metrics.ExecutionFailures.Add(1)
			writeErr(w, 409, "execution_failed", resp.ErrorCode)
			return
		}
		t.Verification = resp
	}
	t.State = "EXECUTING"
	a.append(t, "state.changed", map[string]string{"state": t.State})
	a.persistTask(t)
	t.State = "SUCCEEDED"
	a.append(t, "state.changed", map[string]string{"state": t.State})
	a.persistTask(t)
	writeData(w, 200, t)
}
func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	t.State = "CANCELLED"
	t.Approved = false
	a.persistTask(t)
	a.append(t, "state.changed", map[string]string{"state": t.State})
	writeData(w, 200, t)
}
func (a *API) append(t *task, typ string, data any) {
	e := Event{ID: int64(len(t.Events) + 1), Type: typ, Data: data, CreatedAt: time.Now().UTC()}
	if a.eventRepo != nil {
		if saved, err := a.eventRepo.Append(context.Background(), t.ID, typ, data); err == nil {
			e.ID = saved.Sequence
			e.CreatedAt = saved.CreatedAt
		}
	}
	t.Events = append(t.Events, e)
}
func (a *API) persistTask(t *task) {
	if a.db == nil {
		return
	}
	raw, _ := json.Marshal(t.Acceptance)
	plan, _ := json.Marshal(t.Plan)
	verification, _ := json.Marshal(t.Verification)
	_ = a.db.Model(&sqlite.TaskRecord{}).Where("id = ?", t.ID).Updates(map[string]any{"state": t.State, "plan_version": t.PlanVersion, "approval_hash": t.ApprovalHash, "approval_granted": t.Approved, "acceptance_json": raw, "plan_json": plan, "verification_json": verification})
}
func writeData(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{Data: v, RequestID: strconv.FormatInt(time.Now().UnixNano(), 10)})
}
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{Error: &apiError{Code: code, Message: msg}, RequestID: strconv.FormatInt(time.Now().UnixNano(), 10)})
}
