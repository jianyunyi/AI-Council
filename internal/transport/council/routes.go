package council

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
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
}
type workspace struct {
	ID    string `json:"id"`
	Root  string `json:"root"`
	IsGit bool   `json:"is_git"`
	Dirty bool   `json:"dirty"`
}
type task struct {
	ID           string   `json:"id"`
	State        string   `json:"state"`
	WorkspaceID  string   `json:"workspace_id"`
	Requirement  string   `json:"requirement"`
	Acceptance   []string `json:"acceptance"`
	PlanVersion  int      `json:"plan_version"`
	ApprovalHash string   `json:"approval_hash,omitempty"`
	Events       []Event  `json:"-"`
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

func NewAPI() *API { return &API{tasks: map[string]*task{}, workspaces: map[string]workspace{}} }
func NewPersistentAPI(db *gorm.DB) *API {
	a := NewAPI()
	a.db = db
	a.eventRepo = sqlite.NewEventRepository(db)
	a.approvalRepo = sqlite.NewApprovalRepository(db)
	var rows []sqlite.TaskRecord
	_ = db.Find(&rows).Error
	for _, r := range rows {
		var acceptance []string
		_ = json.Unmarshal(r.AcceptanceJSON, &acceptance)
		state := r.State
		if state == "" {
			state = "DRAFT"
		}
		a.tasks[r.ID] = &task{ID: r.ID, State: state, WorkspaceID: r.WorkspaceID, Requirement: r.Requirement, Acceptance: acceptance, PlanVersion: r.PlanVersion, ApprovalHash: r.ApprovalHash}
	}
	return a
}
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
	a.mu.Lock()
	a.nextID++
	id := "workspace-" + strconv.FormatInt(a.nextID, 10)
	ws := workspace{ID: id, Root: in.Root, IsGit: false}
	a.workspaces[id] = ws
	if a.db != nil {
		_ = a.db.Save(&sqlite.WorkspaceRecord{ID: id, CanonicalRoot: ws.Root}).Error
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
	a.nextID++
	id := "task-" + strconv.FormatInt(a.nextID, 10)
	t := &task{ID: id, State: "DRAFT", WorkspaceID: in.WorkspaceID, Requirement: in.Requirement, Acceptance: in.Acceptance, PlanVersion: 1}
	a.tasks[id] = t
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
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if t.State != "DRAFT" {
		writeErr(w, 409, "invalid_state", "task already started")
		return
	}
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
	t.ApprovalHash = in.ApprovalHash
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
	a.persistTask(t)
	a.append(t, "approval.rejected", map[string]string{"state": t.State})
	writeData(w, 200, t)
}
func (a *API) getArtifact(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, 404, "not_found", "artifact not found")
}
func (a *API) executeTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if t.ApprovalHash == "" {
		writeErr(w, 403, "approval_required", "manual approval required")
		return
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
	_ = a.db.Model(&sqlite.TaskRecord{}).Where("id = ?", t.ID).Updates(map[string]any{"state": t.State, "plan_version": t.PlanVersion, "approval_hash": t.ApprovalHash, "acceptance_json": raw})
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
