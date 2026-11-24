package council

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/app/aimodel"
	"github.com/aicouncil/aicouncil/internal/app/group"
	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/approval"
	artifactcore "github.com/aicouncil/aicouncil/internal/core/artifact"
	councilengine "github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	providerkeysvc "github.com/aicouncil/aicouncil/internal/security/providerkeys"
	workspaceaccess "github.com/aicouncil/aicouncil/internal/security/workspaceaccess"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

type API struct {
	mu              sync.Mutex
	tasks           map[string]*task
	taskExternalKeys map[string]string // P8: push-down idempotency key -> task ID
	rejections      map[string][]sqlite.TaskRejectionRecord
	nextID          int64
	workspaces      map[string]workspace
	db              *gorm.DB
	eventRepo       *sqlite.EventRepository
	approvalRepo    *sqlite.ApprovalRepository
	artifactRepo    *sqlite.ArtifactRepository
	runnerClient    runClient
	council         appTask.CouncilPort
	councilFactory  func(context.Context, string, string) (appTask.CouncilPort, error)
	councilRoles    *sqlite.CouncilRolesRepository
	aiModels        *aimodel.Service
	groups          *group.Service
	providerKeys    *providerkeysvc.Service
	signer          *approval.Signer
	workspaceAccess *workspaceaccess.Service
	metrics         *metrics.Metrics
	readinessChecks []func(context.Context) error
}
type runClient interface {
	ExecuteApprovedPlan(context.Context, *runnerv1.ExecuteApprovedPlanRequest, ...grpc.CallOption) (*runnerv1.ExecuteApprovedPlanResponse, error)
}
type workspaceRegistrar interface {
	RegisterWorkspace(context.Context, *runnerv1.RegisterWorkspaceRequest, ...grpc.CallOption) (*runnerv1.RegisterWorkspaceResponse, error)
}
type workspaceDescriber interface {
	DescribeWorkspace(context.Context, *runnerv1.DescribeWorkspaceRequest, ...grpc.CallOption) (*runnerv1.DescribeWorkspaceResponse, error)
}
type workspaceContextReader interface {
	ReadWorkspaceContext(context.Context, *runnerv1.ReadWorkspaceContextRequest, ...grpc.CallOption) (*runnerv1.ReadWorkspaceContextResponse, error)
}
type workspace struct {
	ID          string `json:"id"`
	Root        string `json:"root"`
	IsGit       bool   `json:"is_git"`
	Dirty       bool   `json:"dirty"`
	Kind        string `json:"kind"`
	Access      string `json:"access,omitempty"`
	IsOwner     bool   `json:"is_owner"`
	OwnerUserID string `json:"-"`
	RunnerPath  string `json:"-"`
}
type task struct {
	ID                     string                                `json:"id"`
	State                  string                                `json:"state"`
	WorkspaceID            string                                `json:"workspace_id"`
	Title                  string                                `json:"title,omitempty"`
	CreatedBy              string                                `json:"created_by,omitempty"`
	Origin                 string                                `json:"origin,omitempty"`
	Requirement            string                                `json:"requirement"`
	Acceptance             []string                              `json:"acceptance"`
	PlanVersion            int                                   `json:"plan_version"`
	ApprovalHash           string                                `json:"approval_hash,omitempty"`
	ApprovalSignature      string                                `json:"-"`
	ApprovalExpiresAt      int64                                 `json:"approval_expires_at,omitempty"`
	ApprovalNonce          string                                `json:"-"`
	Approved               bool                                  `json:"approved"`
	Plan                   schema.ExecutionPlan                  `json:"plan,omitempty"`
	Verification           *runnerv1.ExecuteApprovedPlanResponse `json:"verification,omitempty"`
	VerificationArtifactID string                                `json:"verification_artifact_id,omitempty"`
	Events                 []Event                               `json:"-"`
	OwnerUserID            string                                `json:"-"`
	CreatedAt              time.Time                             `json:"created_at,omitempty"`
	UpdatedAt              time.Time                             `json:"updated_at,omitempty"`
	Rejections             []taskRejectionDTO                    `json:"rejections,omitempty"`
	RejectionCount         int                                   `json:"rejection_count"`
	ExternalKey            string                                `json:"-"` // P8 push-down idempotency key
	GroupRequirementID     string                                `json:"-"` // P8 back-link to the group requirement
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
type workspaceRequest struct {
	Root string `json:"root"`
}
type approvalRequest struct {
	PlanVersion  int    `json:"plan_version"`
	ApprovalHash string `json:"approval_hash"`
}

func NewAPI() *API {
	return &API{tasks: map[string]*task{}, taskExternalKeys: map[string]string{}, rejections: map[string][]sqlite.TaskRejectionRecord{}, workspaces: map[string]workspace{}, metrics: metrics.New()}
}
func NewPersistentAPI(db *gorm.DB) *API {
	a := NewAPI()
	a.db = db
	installSQLiteMetricsLogger(db, a.metrics)
	a.WithReadinessCheck(func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	})
	a.workspaceAccess = workspaceaccess.New(db)
	a.eventRepo = sqlite.NewEventRepository(db)
	a.approvalRepo = sqlite.NewApprovalRepository(db)
	a.artifactRepo = sqlite.NewArtifactRepository(db, ".data/artifacts")
	var rows []sqlite.TaskRecord
	if err := db.Find(&rows).Error; err != nil {
		// Fail closed at startup rather than serving with an empty in-memory
		// view of a database we could not read.
		panic(fmt.Sprintf("load tasks on startup: %v", err))
	}
	var workspaceRows []sqlite.WorkspaceRecord
	if err := db.Find(&workspaceRows).Error; err != nil {
		panic(fmt.Sprintf("load workspaces on startup: %v", err))
	}
	for _, r := range workspaceRows {
		if r.Archived {
			// Archived workspaces (e.g. a disabled AI model's private
			// workspace) stay in storage but are not served.
			continue
		}
		a.workspaces[r.ID] = workspace{ID: r.ID, Root: r.CanonicalRoot, IsGit: r.IsGit, Dirty: r.Dirty, Kind: wskind.Normalize(r.Kind), OwnerUserID: r.OwnerUserID, RunnerPath: r.RunnerPath}
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
		verificationArtifactID := ""
		if v != nil {
			verificationArtifactID = verificationArtifactIDFor(r.ID, r.PlanVersion)
		}
		loaded := &task{ID: r.ID, State: state, WorkspaceID: r.WorkspaceID, Title: r.Title, CreatedBy: createdByOf(r), Origin: originOf(r), Requirement: r.Requirement, Acceptance: acceptance, PlanVersion: r.PlanVersion, ApprovalHash: r.ApprovalHash, Approved: r.ApprovalGranted, Plan: plan, Verification: v, VerificationArtifactID: verificationArtifactID, OwnerUserID: r.OwnerUserID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, GroupRequirementID: r.GroupRequirementID}
		if r.ExternalKey != nil && *r.ExternalKey != "" {
			loaded.ExternalKey = *r.ExternalKey
			a.taskExternalKeys[*r.ExternalKey] = r.ID
		}
		a.tasks[r.ID] = loaded
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
func (a *API) Metrics() *metrics.Metrics                    { return a.metrics }
func (a *API) WithRunnerClient(client runClient) *API       { a.runnerClient = client; return a }
func (a *API) WithCouncil(council appTask.CouncilPort) *API { a.council = council; return a }
func (a *API) WithReadinessCheck(check func(context.Context) error) *API {
	if check != nil {
		a.readinessChecks = append(a.readinessChecks, check)
	}
	return a
}
func (a *API) WithCouncilFactory(factory func(context.Context, string, string) (appTask.CouncilPort, error)) *API {
	a.councilFactory = factory
	return a
}

// WithCouncilRoles installs the role-assignment store used by the council
// roles endpoints and for saving run-start configuration snapshots.
func (a *API) WithCouncilRoles(repo *sqlite.CouncilRolesRepository) *API {
	a.councilRoles = repo
	return a
}
func (a *API) WithProviderKeys(service *providerkeysvc.Service) *API {
	a.providerKeys = service
	return a
}

// WithAIModelService installs the AI model registry service backing the
// /api/v1/ai-models endpoints.
func (a *API) WithAIModelService(service *aimodel.Service) *API {
	a.aiModels = service
	return a
}

// WithGroupService installs the group-chat service backing the
// /api/v1/groups endpoints (Part B P5). A nil service disables the routes.
func (a *API) WithGroupService(service *group.Service) *API {
	a.groups = service
	return a
}

// WithApprovalSigner installs the shared HMAC key used to sign Runner approvals.
func (a *API) WithApprovalSigner(signer *approval.Signer) *API { a.signer = signer; return a }

func newApprovalNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func createdByOf(r sqlite.TaskRecord) string {
	if r.CreatedBy != "" {
		return r.CreatedBy
	}
	return r.OwnerUserID
}

func originOf(r sqlite.TaskRecord) string {
	if r.Origin != "" {
		return r.Origin
	}
	return "direct"
}

func (a *API) Routes() []rest.Route {
	routes := []rest.Route{{Method: http.MethodPost, Path: "/api/v1/workspaces", Handler: a.createWorkspace}, {Method: http.MethodGet, Path: "/api/v1/workspaces", Handler: a.listWorkspaces}, {Method: http.MethodGet, Path: "/api/v1/workspaces/:id", Handler: a.getWorkspace}, {Method: http.MethodPost, Path: "/api/v1/tasks", Handler: a.createTask}, {Method: http.MethodGet, Path: "/api/v1/tasks", Handler: a.listTasks}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id", Handler: a.getTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/start", Handler: a.startTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/approve", Handler: a.approveTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/reject", Handler: a.rejectTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/rejections", Handler: a.listTaskRejections}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/execute", Handler: a.executeTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/cancel", Handler: a.cancelTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/artifacts/:artifactId", Handler: a.getArtifact}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/events", Handler: a.events}, {Method: http.MethodGet, Path: "/api/v1/council/roles", Handler: a.getCouncilRoles}, {Method: http.MethodPut, Path: "/api/v1/council/roles", Handler: a.putCouncilRoles}, {Method: http.MethodDelete, Path: "/api/v1/council/roles", Handler: a.deleteCouncilRoles}, {Method: http.MethodPost, Path: "/api/v1/council/roles/validate", Handler: a.validateCouncilRoles}, {Method: http.MethodGet, Path: "/api/v1/ai-models", Handler: a.listAIModels}, {Method: http.MethodPost, Path: "/api/v1/ai-models", Handler: a.createAIModel}, {Method: http.MethodGet, Path: "/api/v1/ai-models/:id", Handler: a.getAIModel}, {Method: http.MethodPatch, Path: "/api/v1/ai-models/:id", Handler: a.patchAIModel}, {Method: http.MethodDelete, Path: "/api/v1/ai-models/:id", Handler: a.deleteAIModel}, {Method: http.MethodGet, Path: "/api/v1/ai-models/:id/workspace", Handler: a.getAIModelWorkspace}, {Method: http.MethodPost, Path: "/api/v1/ai-models/:id/workspace/retry", Handler: a.retryAIModelWorkspace}}
	if a.groups != nil {
		// Part B P5 group chat. The events route is registered the same way
		// as /api/v1/tasks/:id/events; go-zero's timeout handler exempts
		// requests carrying Accept: text/event-stream, which EventSource
		// always sends.
		routes = append(routes,
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups", Handler: a.listGroups},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups", Handler: a.createGroup},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id", Handler: a.getGroup},
			rest.Route{Method: http.MethodPatch, Path: "/api/v1/groups/:id", Handler: a.patchGroup},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/archive", Handler: a.archiveGroup},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/members", Handler: a.listGroupMembers},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/members", Handler: a.addGroupMember},
			rest.Route{Method: http.MethodPatch, Path: "/api/v1/groups/:id/members/:memberId", Handler: a.patchGroupMember},
			rest.Route{Method: http.MethodDelete, Path: "/api/v1/groups/:id/members/:memberId", Handler: a.removeGroupMember},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/messages", Handler: a.listGroupMessages},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/messages", Handler: a.postGroupMessage},
			rest.Route{Method: http.MethodPatch, Path: "/api/v1/groups/:id/messages/:mid", Handler: a.patchGroupMessage},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/mentions/resolve", Handler: a.resolveGroupMentions},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/requirements", Handler: a.listGroupRequirements},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/requirements/:rid", Handler: a.getGroupRequirement},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/retry", Handler: a.retryGroupRequirement},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/cancel", Handler: a.cancelGroupRequirement},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/review", Handler: a.startGroupReview},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/requirements/:rid/review", Handler: a.getGroupReview},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/review/decision", Handler: a.submitGroupReviewDecision},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/review/force-close", Handler: a.forceCloseGroupReview},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/revise", Handler: a.reviseGroupRequirement},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/approve", Handler: a.approveGroupRequirement},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/pushdown/retry", Handler: a.retryGroupPushdown},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/redteam/retry", Handler: a.retryGroupRedTeam},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/requirements/:rid/results", Handler: a.listRequirementResults},
			rest.Route{Method: http.MethodPost, Path: "/api/v1/groups/:id/requirements/:rid/result-review", Handler: a.startResultReview},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/requirements/:rid/artifacts/:aid", Handler: a.downloadGroupArtifact},
			rest.Route{Method: http.MethodGet, Path: "/api/v1/groups/:id/events", Handler: a.groupEvents},
		)
	}
	return routes
}

func (a *API) getWorkspace(w http.ResponseWriter, r *http.Request) {
	id := pathvar.Vars(r)["id"]
	if !a.canReadWorkspace(r.Context(), id, requestOwnerID(r)) {
		writeErr(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	a.mu.Lock()
	ws, ok := a.workspaces[id]
	a.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	access, _ := a.effectiveWorkspaceAccess(r.Context(), id, requestOwnerID(r))
	ws.Access = string(access)
	ws.IsOwner = ws.OwnerUserID == requestOwnerID(r)
	writeData(w, http.StatusOK, ws)
}

func (a *API) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var in workspaceRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Root) == "" {
		writeErr(w, 400, "invalid_workspace", "root is required")
		return
	}
	root := strings.TrimSpace(in.Root)
	if !validRunnerRelativePath(root) {
		writeErr(w, http.StatusBadRequest, "invalid_workspace", "root must be a relative directory beneath the Runner workspace root")
		return
	}
	registrar, ok := a.runnerClient.(workspaceRegistrar)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "runner_unavailable", "Runner workspace registration is required")
		return
	}
	owner := requestOwnerID(r)
	a.mu.Lock()
	for _, existing := range a.workspaces {
		if existing.RunnerPath == root && existing.OwnerUserID == owner {
			a.mu.Unlock()
			writeData(w, http.StatusOK, existing)
			return
		}
	}
	a.nextID++
	id := "workspace-" + strconv.FormatInt(a.nextID, 10)
	registered, err := registrar.RegisterWorkspace(r.Context(), &runnerv1.RegisterWorkspaceRequest{WorkspaceId: id, RelativePath: root})
	if err != nil || registered == nil {
		a.nextID--
		a.mu.Unlock()
		writeErr(w, http.StatusBadGateway, "runner_registration_failed", "Runner rejected workspace registration")
		return
	}
	// Client-created workspaces are always standard; model_private and
	// group_shared are created only by internal services.
	ws := workspace{ID: id, Root: registered.Root, IsGit: registered.IsGit, Dirty: registered.Dirty, Kind: wskind.Standard, Access: string(workspaceaccess.Editor), IsOwner: true, OwnerUserID: owner, RunnerPath: root}
	a.workspaces[id] = ws
	if a.db != nil {
		if err := a.db.Create(&sqlite.WorkspaceRecord{ID: id, OwnerUserID: owner, CanonicalRoot: ws.Root, RunnerPath: root, IsGit: ws.IsGit, Dirty: ws.Dirty, Kind: wskind.Standard}).Error; err != nil {
			delete(a.workspaces, id)
			a.nextID--
			a.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "workspace_persistence_failed", "workspace could not be saved")
			return
		}
	}
	a.mu.Unlock()
	writeData(w, 201, ws)
}

func validRunnerRelativePath(value string) bool {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "/") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || strings.Contains(value, `\`) || strings.Contains(value, ":") {
		return false
	}
	clean := filepath.Clean(value)
	// Reject the workspace root itself (".") so no single user can claim the
	// entire shared /workspace tree; a workspace must be a named subdirectory.
	if clean == "." {
		return false
	}
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
func (a *API) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	kindFilter := r.URL.Query().Get("kind")
	if kindFilter != "" && !wskind.Valid(kindFilter) {
		writeErr(w, http.StatusBadRequest, "invalid_kind", "kind must be standard, model_private or group_shared")
		return
	}
	owner := requestOwnerID(r)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]workspace, 0, len(a.workspaces))
	for _, ws := range a.workspaces {
		if kindFilter != "" && wskind.Normalize(ws.Kind) != kindFilter {
			continue
		}
		access, err := a.effectiveWorkspaceAccess(r.Context(), ws.ID, owner)
		if err == nil && access != workspaceaccess.AccessNone {
			ws.Access = string(access)
			ws.IsOwner = ws.OwnerUserID == owner
			out = append(out, ws)
		}
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
	owner := requestOwnerID(r)
	_, ok := a.workspaces[in.WorkspaceID]
	access, accessErr := a.effectiveWorkspaceAccess(r.Context(), in.WorkspaceID, owner)
	if !ok || accessErr != nil || access != workspaceaccess.Editor {
		a.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "unknown_workspace", "workspace must be registered before creating a task")
		return
	}
	a.nextID++
	id := "task-" + strconv.FormatInt(a.nextID, 10)
	now := time.Now().UTC()
	t := &task{ID: id, State: "DRAFT", WorkspaceID: in.WorkspaceID, Title: taskTitleOrFallback("", in.Requirement), CreatedBy: owner, Origin: "direct", Requirement: in.Requirement, Acceptance: in.Acceptance, PlanVersion: 1, OwnerUserID: owner, CreatedAt: now, UpdatedAt: now}
	t.Plan.Version = t.PlanVersion
	a.tasks[id] = t
	a.metrics.TasksCreated.Add(1)
	if a.db != nil {
		raw, _ := json.Marshal(in.Acceptance)
		if err := a.db.Save(&sqlite.TaskRecord{ID: id, OwnerUserID: owner, WorkspaceID: in.WorkspaceID, Title: t.Title, CreatedBy: owner, Origin: "direct", Requirement: in.Requirement, AcceptanceJSON: raw, State: t.State, PlanVersion: t.PlanVersion, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			delete(a.tasks, id)
			a.nextID--
			a.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "task_persistence_failed", "task could not be saved")
			return
		}
	}
	if err := a.append(t, "task.created", t); err != nil {
		a.mu.Unlock()
		writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
		return
	}
	a.mu.Unlock()
	writeData(w, http.StatusCreated, t)
}
func taskID(r *http.Request) string { return pathvar.Vars(r)["id"] }
func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canReadWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	a.attachRejections(r.Context(), t)
	writeData(w, http.StatusOK, t)
}
func (a *API) startTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
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
	workspaceID := t.WorkspaceID
	ownerID := t.OwnerUserID
	council := a.council
	a.mu.Unlock()
	if a.councilFactory != nil {
		var err error
		council, err = a.councilFactory(r.Context(), ownerID, workspaceID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "council_unavailable", "could not configure the account's model providers")
			return
		}
	}
	// A workflow built from role assignments carries the resolved-config
	// snapshot: persist it on the run row and route its slot events into
	// the task's event log.
	if snapshotter, ok := council.(interface{ CouncilSnapshot() []byte }); ok && a.councilRoles != nil {
		if snapshot := snapshotter.CouncilSnapshot(); len(snapshot) > 0 {
			_ = a.councilRoles.SaveCouncilConfig(r.Context(), taskID(r), "DRAFT", snapshot)
		}
	}
	if setter, ok := council.(interface {
		SetEventHook(councilengine.EventHook)
	}); ok {
		setter.SetEventHook(func(eventType string, data map[string]any) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if current := a.tasks[taskID(r)]; current != nil {
				_ = a.append(current, eventType, data)
			}
		})
	}
	var workspaceFiles []schema.WorkspaceFile
	if _, ok := council.(appTask.WorkspaceCouncilPort); ok {
		reader, ok := a.runnerClient.(workspaceContextReader)
		if !ok {
			writeErr(w, http.StatusServiceUnavailable, "runner_unavailable", "runner workspace context is required")
			return
		}
		contextResponse, err := reader.ReadWorkspaceContext(r.Context(), &runnerv1.ReadWorkspaceContextRequest{WorkspaceId: workspaceID})
		if err != nil {
			writeErr(w, http.StatusBadGateway, "runner_unavailable", err.Error())
			return
		}
		for _, file := range contextResponse.Files {
			if file != nil {
				workspaceFiles = append(workspaceFiles, schema.WorkspaceFile{Path: file.Path, Content: file.Content})
			}
		}
	}
	var plan schema.ExecutionPlan
	if council != nil {
		a.metrics.CouncilRuns.Add(1)
		if err := council.Analyze(r.Context(), requirement); err != nil {
			a.metrics.CouncilFailures.Add(1)
			writeErr(w, http.StatusBadGateway, "council_unavailable", err.Error())
			return
		}
		a.mu.Lock()
		if t.State != "DRAFT" {
			a.mu.Unlock()
			writeErr(w, 409, "invalid_state", "task already started")
			return
		}
		if err := a.transitionTask(t, "ANALYZING"); err != nil {
			a.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "task_persistence_failed", err.Error())
			return
		}
		a.mu.Unlock()
		var err error
		if progressCouncil, ok := council.(appTask.ProgressingWorkspaceCouncilPort); ok {
			plan, err = progressCouncil.DeliberateWithWorkspaceProgress(r.Context(), requirement, workspaceFiles, func(state string) error {
				if state == "ANALYZING" {
					return nil
				}
				a.mu.Lock()
				defer a.mu.Unlock()
				return a.transitionTask(t, state)
			})
		} else if workspaceCouncil, ok := council.(appTask.WorkspaceCouncilPort); ok {
			plan, err = workspaceCouncil.DeliberateWithWorkspace(r.Context(), requirement, workspaceFiles)
		} else {
			plan, err = council.Deliberate(r.Context(), requirement)
		}
		if err != nil {
			a.metrics.CouncilFailures.Add(1)
			a.mu.Lock()
			if t.State != "FAILED" {
				_ = a.transitionTask(t, "FAILED")
			}
			a.mu.Unlock()
			writeErr(w, http.StatusBadGateway, "council_failed", err.Error())
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.State != "DRAFT" && t.State != "ANALYZING" {
		writeErr(w, 409, "invalid_state", "task already started")
		return
	}
	if council != nil {
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
	if err := a.transitionTask(t, "AWAITING_APPROVAL"); err != nil {
		writeErr(w, http.StatusInternalServerError, "task_persistence_failed", err.Error())
		return
	}
	writeData(w, 200, t)
}
// execFailure is a typed core failure carrying the exact HTTP status,
// code and message the handlers have always produced. The cores below
// return it; handlers map it back with writeErr, so behavior is
// unchanged, and the group push-down bridge (taskbridge.go) maps the
// same codes onto its own permanence rules.
type execFailure struct {
	Status int
	Code   string
	Msg    string
}

func (f *execFailure) Error() string { return f.Code + ": " + f.Msg }

func (a *API) approveTask(w http.ResponseWriter, r *http.Request) {
	var in approvalRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ApprovalHash == "" {
		writeErr(w, 400, "invalid_approval", "approval_hash is required")
		return
	}
	owner := requestOwnerID(r)
	t, _, fail := a.approveTaskCore(r.Context(), taskID(r), in.PlanVersion, in.ApprovalHash, "user", func(t *task) *execFailure {
		if !a.canEditWorkspace(r.Context(), t.WorkspaceID, owner) {
			return &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
		}
		return nil
	})
	if fail != nil {
		writeErr(w, fail.Status, fail.Code, fail.Msg)
		return
	}
	writeData(w, 200, t)
}

// approveTaskCore is the approveTask handler body, extracted unchanged
// (P8): state + plan-version check, client hash check, Runner-facing
// HMAC signature with one-time nonce, ApprovalRepository record. The
// check closure carries the caller's access rule (the HTTP handler
// passes the workspace ACL check; the group push-down bridge passes
// nil — group approval is its authorization, W19). actor lands on the
// approval record: "user" for the HTTP path, the approving human's ID
// for push-down. Returns the task and the approval record ID.
func (a *API) approveTaskCore(ctx context.Context, id string, planVersion int, approvalHash, actor string, check func(*task) *execFailure) (*task, string, *execFailure) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[id]
	if t == nil {
		return nil, "", &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
	}
	if check != nil {
		if f := check(t); f != nil {
			return nil, "", f
		}
	}
	if t.State != "AWAITING_APPROVAL" || planVersion != t.PlanVersion {
		return nil, "", &execFailure{Status: 409, Code: "invalid_state", Msg: "task is not awaiting matching approval"}
	}
	if approvalHash != t.ApprovalHash {
		return nil, "", &execFailure{Status: http.StatusConflict, Code: "approval_mismatch", Msg: "approval hash does not match immutable plan"}
	}
	t.ApprovalHash = approvalHash
	// The client-facing approval_hash binds the UI confirmation to the immutable
	// plan and keys the Council-side approval record, so it stays as-is. The
	// Runner-facing credential is separate: a keyed HMAC over the plan with a
	// short expiry and a one-time nonce, generated here at approval time.
	if a.signer == nil {
		return nil, "", &execFailure{Status: http.StatusServiceUnavailable, Code: "signing_unavailable", Msg: "approval signing key is not configured"}
	}
	nonce, nonceErr := newApprovalNonce()
	if nonceErr != nil {
		return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "signing_failed", Msg: "could not generate approval nonce"}
	}
	expiresAt := time.Now().Add(10 * time.Minute).Unix()
	signature, signErr := a.signer.Sign(t.ID, t.WorkspaceID, t.PlanVersion, t.Plan, expiresAt, nonce)
	if signErr != nil {
		return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "signing_failed", Msg: "could not sign approval"}
	}
	t.ApprovalSignature = signature
	t.ApprovalExpiresAt = expiresAt
	t.ApprovalNonce = nonce
	approvalID := ""
	if a.approvalRepo != nil {
		if err := a.approvalRepo.Invalidate(ctx, t.ID); err != nil {
			return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "approval_persistence_failed", Msg: err.Error()}
		}
		approvalID = strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := a.approvalRepo.Save(ctx, sqlite.ApprovalRecord{ID: approvalID, RunID: t.ID, PlanVersion: planVersion, SnapshotHash: approvalHash, Decision: "approved", Actor: actor, CreatedAt: time.Now().UTC()}); err != nil {
			return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "approval_persistence_failed", Msg: err.Error()}
		}
	}
	t.Approved = true
	if err := a.persistTask(t); err != nil {
		return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "task_persistence_failed", Msg: err.Error()}
	}
	if err := a.append(t, "approval.created", approvalRequest{PlanVersion: planVersion, ApprovalHash: approvalHash}); err != nil {
		return nil, "", &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: err.Error()}
	}
	a.metrics.ApprovalsGranted.Add(1)
	return t, approvalID, nil
}
func (a *API) rejectTask(w http.ResponseWriter, r *http.Request) {
	in, parseErr := parseRejectRequest(r)
	if parseErr != nil {
		writeErr(w, http.StatusBadRequest, "invalid_rejection", "request body must be valid JSON")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	if in.PlanVersion != nil && *in.PlanVersion != t.PlanVersion {
		writeErr(w, http.StatusConflict, "stale_plan_version", "plan_version does not match the current plan")
		return
	}
	reason, category, normErr := normalizeRejection(in)
	if normErr != nil {
		writeErr(w, http.StatusBadRequest, "invalid_rejection", normErr.Error())
		return
	}
	nonce, nonceErr := newApprovalNonce()
	if nonceErr != nil {
		writeErr(w, http.StatusInternalServerError, "rejection_failed", "could not allocate rejection id")
		return
	}
	rejectedBy := requestOwnerID(r)
	if rejectedBy == "" {
		rejectedBy = "user"
	}
	var noncePtr *string
	if t.ApprovalNonce != "" {
		nonceCopy := t.ApprovalNonce
		noncePtr = &nonceCopy
	}
	record := sqlite.TaskRejectionRecord{
		ID: "rej-" + nonce, TaskID: t.ID, RunID: t.ID, PlanVersion: t.PlanVersion,
		ApprovalNonceID: noncePtr, PlanHash: t.ApprovalHash, RejectedBy: rejectedBy,
		Category: category, Reason: reason, Source: "task", CreatedAt: time.Now().UTC(),
	}
	eventData := map[string]any{
		"rejection_id":   record.ID,
		"category":       category,
		"reason_excerpt": truncateRunes(reason, 200),
		"plan_version":   t.PlanVersion,
	}
	if a.db != nil {
		event, err := sqlite.NewRejectionRepository(a.db).RejectAtomically(r.Context(), record, "CANCELLED", eventData)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "rejection_failed", err.Error())
			return
		}
		t.Events = append(t.Events, Event{ID: event.Sequence, Type: "approval.rejected", Data: eventData, CreatedAt: event.CreatedAt})
	} else {
		a.rejections[t.ID] = append(a.rejections[t.ID], record)
		if err := a.append(t, "approval.rejected", eventData); err != nil {
			writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
			return
		}
	}
	t.State = "CANCELLED"
	t.Approved = false
	t.ApprovalNonce = ""
	t.ApprovalSignature = ""
	t.ApprovalExpiresAt = 0
	t.UpdatedAt = time.Now().UTC()
	a.attachRejections(r.Context(), t)
	a.metrics.ApprovalsRejected.Add(1)
	writeData(w, 200, t)
}
func (a *API) getArtifact(w http.ResponseWriter, r *http.Request) {
	if a.artifactRepo == nil {
		writeErr(w, 404, "not_found", "artifact not found")
		return
	}
	a.mu.Lock()
	t := a.tasks[taskID(r)]
	allowed := t != nil && a.canReadWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r))
	a.mu.Unlock()
	if !allowed {
		writeErr(w, http.StatusNotFound, "not_found", "artifact not found")
		return
	}
	env, err := a.artifactRepo.LoadForRun(r.Context(), pathvar.Vars(r)["artifactId"], taskID(r), "")
	if err != nil {
		writeErr(w, 404, "not_found", "artifact not found")
		return
	}
	writeData(w, 200, env)
}
func (a *API) executeTask(w http.ResponseWriter, r *http.Request) {
	owner := requestOwnerID(r)
	t, fail := a.executeTaskCore(r.Context(), taskID(r), func(t *task) *execFailure {
		if !a.canEditWorkspace(r.Context(), t.WorkspaceID, owner) {
			return &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
		}
		return nil
	})
	if fail != nil {
		writeErr(w, fail.Status, fail.Code, fail.Msg)
		return
	}
	writeData(w, 200, t)
}

// executeTaskCore is the executeTask handler body, extracted unchanged
// (P8, W23): load task -> workspace-kind guard -> consume the single-use
// approval -> synchronous Runner call -> VERIFYING -> SUCCEEDED. The
// check closure carries the caller's access rule (HTTP handler: the
// workspace ACL check; group push-down bridge: nil, per W19). The lock
// is released around the Runner call exactly as the handler did.
func (a *API) executeTaskCore(ctx context.Context, id string, check func(*task) *execFailure) (*task, *execFailure) {
	a.mu.Lock()
	t := a.tasks[id]
	if t == nil {
		a.mu.Unlock()
		return nil, &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
	}
	if check != nil {
		if f := check(t); f != nil {
			a.mu.Unlock()
			return nil, f
		}
	}
	resuming := t.State == "EXECUTING"
	if t.State != "AWAITING_APPROVAL" && !resuming {
		if !t.Approved {
			a.mu.Unlock()
			return nil, &execFailure{Status: 403, Code: "approval_required", Msg: "manual approval required"}
		}
		a.mu.Unlock()
		return nil, &execFailure{Status: http.StatusConflict, Code: "invalid_state", Msg: "task is not awaiting approval"}
	}
	if !t.Approved && !resuming {
		a.mu.Unlock()
		return nil, &execFailure{Status: 403, Code: "approval_required", Msg: "manual approval required"}
	}
	if a.runnerClient == nil {
		a.mu.Unlock()
		return nil, &execFailure{Status: http.StatusServiceUnavailable, Code: "runner_unavailable", Msg: "runner client is not configured"}
	}
	// Kind guard: group_shared workspaces can never execute. This runs before
	// the approval record is consumed so a rejected execution leaves the
	// one-time approval untouched.
	if a.db != nil {
		if err := wskind.GuardExecution(ctx, sqlite.NewWorkspaceKindStore(a.db), t.WorkspaceID); err != nil {
			a.mu.Unlock()
			if errors.Is(err, wskind.ErrNotExecutable) || errors.Is(err, wskind.ErrUnknownKind) {
				return nil, &execFailure{Status: http.StatusConflict, Code: "workspace_not_executable", Msg: err.Error()}
			}
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "workspace_kind_failed", Msg: err.Error()}
		}
	}
	if a.approvalRepo != nil && !resuming {
		consumed, err := a.approvalRepo.ConsumeAndMarkExecuting(ctx, t.ID, t.PlanVersion, t.ApprovalHash)
		if err != nil {
			a.mu.Unlock()
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "approval_persistence_failed", Msg: err.Error()}
		}
		if !consumed {
			a.mu.Unlock()
			return nil, &execFailure{Status: http.StatusConflict, Code: "approval_consumed", Msg: "matching approval is no longer active"}
		}
	}
	if !resuming {
		t.State = "EXECUTING"
		if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
			a.mu.Unlock()
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: err.Error()}
		}
	}
	req := &runnerv1.ExecuteApprovedPlanRequest{RequestId: t.ID + ":" + strconv.Itoa(t.PlanVersion), RunId: t.ID, WorkspaceId: t.WorkspaceID, PlanVersion: int32(t.PlanVersion), ApprovalHash: t.ApprovalSignature, ExpiresAt: t.ApprovalExpiresAt, Nonce: t.ApprovalNonce, Acceptance: append([]string(nil), t.Plan.Acceptance...)}
	for _, p := range t.Plan.Patches {
		req.Patches = append(req.Patches, &runnerv1.ApprovedPatch{Path: p.Path, UnifiedDiff: p.UnifiedDiff, BeforeHash: p.BeforeHash})
	}
	for _, c := range t.Plan.Commands {
		req.Commands = append(req.Commands, &runnerv1.ApprovedCommand{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int32(c.TimeoutSeconds), Purpose: c.Purpose})
	}
	for _, c := range t.Plan.VerificationCommands {
		req.VerificationCommands = append(req.VerificationCommands, &runnerv1.ApprovedCommand{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int32(c.TimeoutSeconds), Purpose: c.Purpose})
	}
	// The request is fully snapshotted above; release the lock so the
	// potentially multi-minute synchronous Runner call does not block every
	// other API request. State write-back below re-acquires it.
	a.mu.Unlock()
	runnerStarted := time.Now()
	resp, err := a.runnerClient.ExecuteApprovedPlan(ctx, req)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.metrics.RecordRunnerExecution(false, time.Since(runnerStarted))
		return nil, &execFailure{Status: http.StatusBadGateway, Code: "runner_unavailable", Msg: err.Error()}
	}
	if resp == nil || resp.Status != "SUCCEEDED" {
		a.metrics.RecordRunnerExecution(false, time.Since(runnerStarted))
		t.State = "FAILED"
		if persistErr := a.persistTask(t); persistErr != nil {
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "task_persistence_failed", Msg: persistErr.Error()}
		}
		if appendErr := a.append(t, "state.changed", map[string]string{"state": t.State}); appendErr != nil {
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: appendErr.Error()}
		}
		errorCode := "runner returned no result"
		if resp != nil {
			errorCode = resp.ErrorCode
		}
		return nil, &execFailure{Status: 409, Code: "execution_failed", Msg: errorCode}
	}
	a.metrics.RecordRunnerExecution(true, time.Since(runnerStarted))
	t.Verification = resp
	if a.artifactRepo != nil {
		artifactID := verificationArtifactIDFor(t.ID, t.PlanVersion)
		verificationArtifact, artifactErr := artifactcore.New("runner.verification", artifactID, t.ID, resp)
		if artifactErr != nil {
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "artifact_persistence_failed", Msg: "Runner verification artifact could not be encoded"}
		}
		if artifactErr = a.artifactRepo.SaveOwned(ctx, t.OwnerUserID, t.ID, verificationArtifact); artifactErr != nil {
			return nil, &execFailure{Status: http.StatusInternalServerError, Code: "artifact_persistence_failed", Msg: "Runner verification artifact could not be stored"}
		}
		t.VerificationArtifactID = artifactID
	}
	t.State = "VERIFYING"
	if err := a.persistTask(t); err != nil {
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "task_persistence_failed", Msg: err.Error()}
	}
	if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: err.Error()}
	}
	t.State = "SUCCEEDED"
	if err := a.persistTask(t); err != nil {
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "task_persistence_failed", Msg: err.Error()}
	}
	if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: err.Error()}
	}
	return t, nil
}

func verificationArtifactIDFor(taskID string, planVersion int) string {
	return taskID + "-verification-v" + strconv.Itoa(planVersion)
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	owner := requestOwnerID(r)
	t, fail := a.cancelTaskCore(taskID(r), func(t *task) *execFailure {
		if !a.canEditWorkspace(r.Context(), t.WorkspaceID, owner) {
			return &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
		}
		return nil
	})
	if fail != nil {
		writeErr(w, fail.Status, fail.Code, fail.Msg)
		return
	}
	writeData(w, 200, t)
}

// cancelTaskCore is the cancelTask handler body, extracted unchanged
// (P8): the group push-down calls it best-effort when a requirement is
// cancelled mid-flight.
func (a *API) cancelTaskCore(id string, check func(*task) *execFailure) (*task, *execFailure) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[id]
	if t == nil {
		return nil, &execFailure{Status: 404, Code: "not_found", Msg: "task not found"}
	}
	if check != nil {
		if f := check(t); f != nil {
			return nil, f
		}
	}
	// A synchronous Runner execution has no cancel RPC; refuse rather than mark
	// the task CANCELLED while the Runner keeps running (state/reality mismatch).
	if t.State == "EXECUTING" {
		return nil, &execFailure{Status: http.StatusConflict, Code: "execution_in_progress", Msg: "task is executing and cannot be cancelled"}
	}
	prevState, prevApproved := t.State, t.Approved
	t.State = "CANCELLED"
	t.Approved = false
	if err := a.persistTask(t); err != nil {
		t.State, t.Approved = prevState, prevApproved
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "task_persistence_failed", Msg: err.Error()}
	}
	if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
		return nil, &execFailure{Status: http.StatusInternalServerError, Code: "event_persistence_failed", Msg: err.Error()}
	}
	return t, nil
}
func (a *API) append(t *task, typ string, data any) error {
	e := Event{ID: int64(len(t.Events) + 1), Type: typ, Data: data, CreatedAt: time.Now().UTC()}
	if a.eventRepo != nil {
		saved, err := a.eventRepo.Append(context.Background(), t.ID, typ, data)
		if err != nil {
			return err
		}
		e.ID = saved.Sequence
		e.CreatedAt = saved.CreatedAt
	}
	t.Events = append(t.Events, e)
	return nil
}
func (a *API) persistTask(t *task) error {
	t.UpdatedAt = time.Now().UTC()
	if a.db == nil {
		return nil
	}
	raw, _ := json.Marshal(t.Acceptance)
	plan, _ := json.Marshal(t.Plan)
	verification, _ := json.Marshal(t.Verification)
	return a.db.Model(&sqlite.TaskRecord{}).Where("id = ?", t.ID).Updates(map[string]any{"state": t.State, "plan_version": t.PlanVersion, "approval_hash": t.ApprovalHash, "approval_granted": t.Approved, "acceptance_json": raw, "plan_json": plan, "verification_json": verification, "updated_at": t.UpdatedAt}).Error
}

func (a *API) transitionTask(t *task, state string) error {
	t.State = state
	if err := a.persistTask(t); err != nil {
		return err
	}
	return a.append(t, "state.changed", map[string]string{"state": state})
}

func requestOwnerID(r *http.Request) string {
	identity, ok := IdentityFromRequest(r)
	if !ok {
		return ""
	}
	return identity.UserID
}

func ownsOwner(resourceOwner, requestOwner string) bool {
	return requestOwner == "" || resourceOwner == requestOwner
}

func (a *API) effectiveWorkspaceAccess(ctx context.Context, workspaceID, userID string) (workspaceaccess.Access, error) {
	if a.workspaceAccess != nil {
		return a.workspaceAccess.EffectiveAccess(ctx, workspaceID, userID)
	}
	if userID == "" {
		return workspaceaccess.Editor, nil
	}
	ws, ok := a.workspaces[workspaceID]
	if !ok {
		return workspaceaccess.AccessNone, workspaceaccess.ErrWorkspaceNotFound
	}
	if ws.OwnerUserID == userID {
		return workspaceaccess.Editor, nil
	}
	return workspaceaccess.AccessNone, nil
}

func (a *API) canReadWorkspace(ctx context.Context, workspaceID, userID string) bool {
	access, err := a.effectiveWorkspaceAccess(ctx, workspaceID, userID)
	return err == nil && access != workspaceaccess.AccessNone
}

func (a *API) canEditWorkspace(ctx context.Context, workspaceID, userID string) bool {
	access, err := a.effectiveWorkspaceAccess(ctx, workspaceID, userID)
	return err == nil && access == workspaceaccess.Editor
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
