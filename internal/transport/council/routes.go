package council

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
)

type API struct {
	mu     sync.Mutex
	tasks  map[string]*task
	nextID int64
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
type approvalRequest struct {
	PlanVersion  int    `json:"plan_version"`
	ApprovalHash string `json:"approval_hash"`
}

func NewAPI() *API { return &API{tasks: map[string]*task{}} }
func (a *API) Routes() []rest.Route {
	return []rest.Route{{Method: http.MethodPost, Path: "/api/v1/tasks", Handler: a.createTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id", Handler: a.getTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/start", Handler: a.startTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/approve", Handler: a.approveTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/execute", Handler: a.executeTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/cancel", Handler: a.cancelTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/events", Handler: a.events}}
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
	a.append(t, "approval.created", in)
	writeData(w, 200, t)
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
	t.State = "SUCCEEDED"
	a.append(t, "state.changed", map[string]string{"state": t.State})
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
	a.append(t, "state.changed", map[string]string{"state": t.State})
	writeData(w, 200, t)
}
func (a *API) append(t *task, typ string, data any) {
	t.Events = append(t.Events, Event{ID: int64(len(t.Events) + 1), Type: typ, Data: data, CreatedAt: time.Now()})
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
