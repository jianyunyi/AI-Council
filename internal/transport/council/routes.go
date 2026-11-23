package council

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	"github.com/aicouncil/aicouncil/internal/approval"
	artifactcore "github.com/aicouncil/aicouncil/internal/core/artifact"
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
	nextID          int64
	workspaces      map[string]workspace
	db              *gorm.DB
	eventRepo       *sqlite.EventRepository
	approvalRepo    *sqlite.ApprovalRepository
	artifactRepo    *sqlite.ArtifactRepository
	runnerClient    runClient
	council         appTask.CouncilPort
	councilFactory  func(context.Context, string) (appTask.CouncilPort, error)
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
	Access      string `json:"access,omitempty"`
	IsOwner     bool   `json:"is_owner"`
	OwnerUserID string `json:"-"`
	RunnerPath  string `json:"-"`
}
type task struct {
	ID                     string                                `json:"id"`
	State                  string                                `json:"state"`
	WorkspaceID            string                                `json:"workspace_id"`
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
	return &API{tasks: map[string]*task{}, workspaces: map[string]workspace{}, metrics: metrics.New()}
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
	_ = db.Find(&rows).Error
	var workspaceRows []sqlite.WorkspaceRecord
	_ = db.Find(&workspaceRows).Error
	for _, r := range workspaceRows {
		a.workspaces[r.ID] = workspace{ID: r.ID, Root: r.CanonicalRoot, IsGit: r.IsGit, Dirty: r.Dirty, OwnerUserID: r.OwnerUserID, RunnerPath: r.RunnerPath}
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
		a.tasks[r.ID] = &task{ID: r.ID, State: state, WorkspaceID: r.WorkspaceID, Requirement: r.Requirement, Acceptance: acceptance, PlanVersion: r.PlanVersion, ApprovalHash: r.ApprovalHash, Approved: r.ApprovalGranted, Plan: plan, Verification: v, VerificationArtifactID: verificationArtifactID, OwnerUserID: r.OwnerUserID}
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
func (a *API) WithCouncilFactory(factory func(context.Context, string) (appTask.CouncilPort, error)) *API {
	a.councilFactory = factory
	return a
}
func (a *API) WithProviderKeys(service *providerkeysvc.Service) *API {
	a.providerKeys = service
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
func (a *API) Routes() []rest.Route {
	return []rest.Route{{Method: http.MethodPost, Path: "/api/v1/workspaces", Handler: a.createWorkspace}, {Method: http.MethodGet, Path: "/api/v1/workspaces", Handler: a.listWorkspaces}, {Method: http.MethodGet, Path: "/api/v1/workspaces/:id", Handler: a.getWorkspace}, {Method: http.MethodPost, Path: "/api/v1/tasks", Handler: a.createTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id", Handler: a.getTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/start", Handler: a.startTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/approve", Handler: a.approveTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/reject", Handler: a.rejectTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/execute", Handler: a.executeTask}, {Method: http.MethodPost, Path: "/api/v1/tasks/:id/cancel", Handler: a.cancelTask}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/artifacts/:artifactId", Handler: a.getArtifact}, {Method: http.MethodGet, Path: "/api/v1/tasks/:id/events", Handler: a.events}}
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
	ws := workspace{ID: id, Root: registered.Root, IsGit: registered.IsGit, Dirty: registered.Dirty, Access: string(workspaceaccess.Editor), IsOwner: true, OwnerUserID: owner, RunnerPath: root}
	a.workspaces[id] = ws
	if a.db != nil {
		if err := a.db.Create(&sqlite.WorkspaceRecord{ID: id, OwnerUserID: owner, CanonicalRoot: ws.Root, RunnerPath: root, IsGit: ws.IsGit, Dirty: ws.Dirty}).Error; err != nil {
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
	owner := requestOwnerID(r)
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]workspace, 0, len(a.workspaces))
	for _, ws := range a.workspaces {
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
	t := &task{ID: id, State: "DRAFT", WorkspaceID: in.WorkspaceID, Requirement: in.Requirement, Acceptance: in.Acceptance, PlanVersion: 1, OwnerUserID: owner}
	t.Plan.Version = t.PlanVersion
	a.tasks[id] = t
	a.metrics.TasksCreated.Add(1)
	if a.db != nil {
		raw, _ := json.Marshal(in.Acceptance)
		_ = a.db.Save(&sqlite.TaskRecord{ID: id, OwnerUserID: owner, WorkspaceID: in.WorkspaceID, Requirement: in.Requirement, AcceptanceJSON: raw, State: t.State, PlanVersion: t.PlanVersion}).Error
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
	if t == nil || !a.canReadWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
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
		council, err = a.councilFactory(r.Context(), ownerID)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "council_unavailable", "could not configure the account's model providers")
			return
		}
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
func (a *API) approveTask(w http.ResponseWriter, r *http.Request) {
	var in approvalRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ApprovalHash == "" {
		writeErr(w, 400, "invalid_approval", "approval_hash is required")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
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
	// The client-facing approval_hash binds the UI confirmation to the immutable
	// plan and keys the Council-side approval record, so it stays as-is. The
	// Runner-facing credential is separate: a keyed HMAC over the plan with a
	// short expiry and a one-time nonce, generated here at approval time.
	if a.signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "signing_unavailable", "approval signing key is not configured")
		return
	}
	nonce, nonceErr := newApprovalNonce()
	if nonceErr != nil {
		writeErr(w, http.StatusInternalServerError, "signing_failed", "could not generate approval nonce")
		return
	}
	expiresAt := time.Now().Add(10 * time.Minute).Unix()
	signature, signErr := a.signer.Sign(t.ID, t.WorkspaceID, t.PlanVersion, t.Plan, expiresAt, nonce)
	if signErr != nil {
		writeErr(w, http.StatusInternalServerError, "signing_failed", "could not sign approval")
		return
	}
	t.ApprovalSignature = signature
	t.ApprovalExpiresAt = expiresAt
	t.ApprovalNonce = nonce
	if a.approvalRepo != nil {
		if err := a.approvalRepo.Invalidate(r.Context(), t.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, "approval_persistence_failed", err.Error())
			return
		}
		if err := a.approvalRepo.Save(r.Context(), sqlite.ApprovalRecord{ID: strconv.FormatInt(time.Now().UnixNano(), 10), RunID: t.ID, PlanVersion: in.PlanVersion, SnapshotHash: in.ApprovalHash, Decision: "approved", Actor: "user", CreatedAt: time.Now().UTC()}); err != nil {
			writeErr(w, http.StatusInternalServerError, "approval_persistence_failed", err.Error())
			return
		}
	}
	t.Approved = true
	if err := a.persistTask(t); err != nil {
		writeErr(w, http.StatusInternalServerError, "task_persistence_failed", err.Error())
		return
	}
	if err := a.append(t, "approval.created", in); err != nil {
		writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
		return
	}
	a.metrics.ApprovalsGranted.Add(1)
	writeData(w, 200, t)
}
func (a *API) rejectTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	t.State = "CANCELLED"
	t.Approved = false
	a.persistTask(t)
	a.append(t, "approval.rejected", map[string]string{"state": t.State})
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
	a.mu.Lock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		a.mu.Unlock()
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	resuming := t.State == "EXECUTING"
	if t.State != "AWAITING_APPROVAL" && !resuming {
		if !t.Approved {
			a.mu.Unlock()
			writeErr(w, 403, "approval_required", "manual approval required")
			return
		}
		a.mu.Unlock()
		writeErr(w, http.StatusConflict, "invalid_state", "task is not awaiting approval")
		return
	}
	if !t.Approved && !resuming {
		a.mu.Unlock()
		writeErr(w, 403, "approval_required", "manual approval required")
		return
	}
	if a.runnerClient == nil {
		a.mu.Unlock()
		writeErr(w, http.StatusServiceUnavailable, "runner_unavailable", "runner client is not configured")
		return
	}
	if a.approvalRepo != nil && !resuming {
		consumed, err := a.approvalRepo.ConsumeAndMarkExecuting(r.Context(), t.ID, t.PlanVersion, t.ApprovalHash)
		if err != nil {
			a.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "approval_persistence_failed", err.Error())
			return
		}
		if !consumed {
			a.mu.Unlock()
			writeErr(w, http.StatusConflict, "approval_consumed", "matching approval is no longer active")
			return
		}
	}
	if !resuming {
		t.State = "EXECUTING"
		if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
			a.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
			return
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
	resp, err := a.runnerClient.ExecuteApprovedPlan(r.Context(), req)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.metrics.RecordRunnerExecution(false, time.Since(runnerStarted))
		writeErr(w, http.StatusBadGateway, "runner_unavailable", err.Error())
		return
	}
	if resp == nil || resp.Status != "SUCCEEDED" {
		a.metrics.RecordRunnerExecution(false, time.Since(runnerStarted))
		t.State = "FAILED"
		if persistErr := a.persistTask(t); persistErr != nil {
			writeErr(w, http.StatusInternalServerError, "task_persistence_failed", persistErr.Error())
			return
		}
		if appendErr := a.append(t, "state.changed", map[string]string{"state": t.State}); appendErr != nil {
			writeErr(w, http.StatusInternalServerError, "event_persistence_failed", appendErr.Error())
			return
		}
		errorCode := "runner returned no result"
		if resp != nil {
			errorCode = resp.ErrorCode
		}
		writeErr(w, 409, "execution_failed", errorCode)
		return
	}
	a.metrics.RecordRunnerExecution(true, time.Since(runnerStarted))
	t.Verification = resp
	if a.artifactRepo != nil {
		artifactID := verificationArtifactIDFor(t.ID, t.PlanVersion)
		verificationArtifact, artifactErr := artifactcore.New("runner.verification", artifactID, t.ID, resp)
		if artifactErr != nil {
			writeErr(w, http.StatusInternalServerError, "artifact_persistence_failed", "Runner verification artifact could not be encoded")
			return
		}
		if artifactErr = a.artifactRepo.SaveOwned(r.Context(), t.OwnerUserID, t.ID, verificationArtifact); artifactErr != nil {
			writeErr(w, http.StatusInternalServerError, "artifact_persistence_failed", "Runner verification artifact could not be stored")
			return
		}
		t.VerificationArtifactID = artifactID
	}
	t.State = "VERIFYING"
	if err := a.persistTask(t); err != nil {
		writeErr(w, http.StatusInternalServerError, "task_persistence_failed", err.Error())
		return
	}
	if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
		writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
		return
	}
	t.State = "SUCCEEDED"
	if err := a.persistTask(t); err != nil {
		writeErr(w, http.StatusInternalServerError, "task_persistence_failed", err.Error())
		return
	}
	if err := a.append(t, "state.changed", map[string]string{"state": t.State}); err != nil {
		writeErr(w, http.StatusInternalServerError, "event_persistence_failed", err.Error())
		return
	}
	writeData(w, 200, t)
}

func verificationArtifactIDFor(taskID string, planVersion int) string {
	return taskID + "-verification-v" + strconv.Itoa(planVersion)
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.tasks[taskID(r)]
	if t == nil || !a.canEditWorkspace(r.Context(), t.WorkspaceID, requestOwnerID(r)) {
		writeErr(w, 404, "not_found", "task not found")
		return
	}
	t.State = "CANCELLED"
	t.Approved = false
	a.persistTask(t)
	a.append(t, "state.changed", map[string]string{"state": t.State})
	writeData(w, 200, t)
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
	if a.db == nil {
		return nil
	}
	raw, _ := json.Marshal(t.Acceptance)
	plan, _ := json.Marshal(t.Plan)
	verification, _ := json.Marshal(t.Verification)
	return a.db.Model(&sqlite.TaskRecord{}).Where("id = ?", t.ID).Updates(map[string]any{"state": t.State, "plan_version": t.PlanVersion, "approval_hash": t.ApprovalHash, "approval_granted": t.Approved, "acceptance_json": raw, "plan_json": plan, "verification_json": verification}).Error
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
