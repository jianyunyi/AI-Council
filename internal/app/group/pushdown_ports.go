package group

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Part B P8: red-team gate, D1 human approval, push-down into the
// assignee's model workspace, and task mirroring.
//
// The group service creates NO approval of its own. A human approval is
// turned into an ordinary council approval on a council task in the
// assignee's model workspace, through the landed task engine
// (approveTaskCore + executeTaskCore in transport/council: kind guard,
// approval.Signer/ApprovalRepository single-use record, Runner). All
// calls into that engine sit behind the TaskBridge port below, and the
// only task information that crosses back is TaskStatus — state, stage,
// an adapter-written summary (<= 300 chars) and a failure category.
// Never logs, command output, diffs or file contents.

const (
	// P8 requirement states. ReqExecuted is NOT terminal: P9 continues
	// from it (result submission + result review).
	ReqAwaitingApproval = "awaiting_approval"
	ReqApproved         = "approved"
	ReqExecuting        = "executing"
	ReqExecuted         = "executed"
	ReqExecutionFailed  = "execution_failed"

	// Red-team check statuses (redteam_checks.status).
	RTQueued     = "queued"
	RTRunning    = "running"
	RTClear      = "clear"
	RTFlagged    = "flagged"
	RTError      = "error"
	RTSuperseded = "superseded"

	// Push-down statuses (requirement_pushdowns.status).
	PDApproved     = "approved"
	PDTaskCreated  = "task_created"
	PDTaskApproved = "task_approved"
	PDExecuting    = "executing"
	PDSucceeded    = "succeeded"
	PDFailed       = "failed"
	PDCancelled    = "cancelled"

	// Message kinds for P8 system cards.
	KindRedTeamResult  = "redteam_result"
	KindApprovalNotice = "approval_notice"
	KindTaskStatus     = "task_status"
	KindExecFailure    = "execution_failure"
)

var (
	errPushdownDisabled  = newErr(http.StatusServiceUnavailable, "pushdown_unavailable", "push-down is not configured on this server")
	errNotApprovable     = newErr(http.StatusConflict, "not_approvable", "approval is only possible from awaiting_approval")
	errApproveForbidden  = newErr(http.StatusForbidden, "approve_forbidden", "only a human owner or operator of the group can approve")
	errPlanMismatch      = newErr(http.StatusConflict, "plan_changed", "the plan changed after review; review it again")
	errRedTeamNotClear   = newErr(http.StatusConflict, "redteam_not_clear", "the red-team check has not passed for this plan")
	errNotRetryableRT    = newErr(http.StatusConflict, "redteam_not_retryable", "red team can only be retried after an error")
	errNotRetryablePD    = newErr(http.StatusConflict, "pushdown_not_retryable", "push-down can only be retried from approved")
	errWorkspaceNotReady = newErr(http.StatusConflict, "workspace_not_ready", "the assignee's model workspace is not ready")
)

// PhaseOf normalizes the landed council task runstate values
// (internal/core/runstate: DRAFT, ANALYZING, PROPOSING, REVIEWING,
// JUDGING, RED_TEAM, AWAITING_APPROVAL, EXECUTING, VERIFYING,
// REPLANNING, SUCCEEDED, FAILED, CANCELLED) into the phases the group
// pipeline reasons about. Unknown states map to running, so they can
// never finish a requirement by mistake.
func PhaseOf(state string) string {
	switch state {
	case "DRAFT", "AWAITING_APPROVAL":
		return "pending"
	case "SUCCEEDED":
		return "succeeded"
	case "FAILED":
		return "failed"
	case "CANCELLED":
		return "cancelled"
	default:
		return "running"
	}
}

// RedTeamSeat is one resolved red_team seat (Part A role assignment):
// the provider profile and model to invoke, in failover order.
type RedTeamSeat struct {
	ProfileID   string
	Model       string
	Ordinal     int
	Temperature *float64
}

// RedTeamSeatSource resolves the red_team seats for a group (wraps the
// Part A roles.Resolver over the group's owner + shared workspace).
type RedTeamSeatSource interface {
	RedTeamSeats(ctx context.Context, groupID string) ([]RedTeamSeat, error)
}

// PushdownTaskInput creates a council task with the approved plan
// pre-filled. WorkspaceID is the assignee's model_private workspace
// (server-resolved, never client input).
type PushdownTaskInput struct {
	IdempotencyKey     string // "pd:<requirement>:<attempt>"; repeated calls return the same task
	WorkspaceID        string
	OwnerID            string // the approving human
	Requirement        string
	Acceptance         []string
	PlanJSON           []byte
	GroupRequirementID string
}

// TaskStatus is the ONLY task information that crosses into group code.
// Summary is adapter-written from state and step counts (no logs or
// command output). PlanHash is the group plan hash (PlanHashOf) of the
// task's plan.
type TaskStatus struct {
	TaskID          string
	State           string // the task's runstate value, verbatim
	Stage           string
	Phase           string // normalized: pending|running|succeeded|failed|cancelled
	Summary         string // <= 300 runes
	FailureCategory string // e.g. runner_error, verification_failed, timeout, cancelled
	PlanHash        string
}

// ErrPushdownPermanent marks a council-side refusal a retry cannot fix
// (approval consumed or invalid, kind guard, plan mismatch).
var ErrPushdownPermanent = errors.New("pushdown permanently refused")

// PermanentError is a permanent bridge failure carrying the failure
// category the requirement's failure card should show.
type PermanentError struct {
	Category string
	Err      error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// permanentCategory reports whether err is a permanent bridge refusal,
// and the failure category to record ("" when the bridge gave none).
func permanentCategory(err error) (string, bool) {
	var pe *PermanentError
	if errors.As(err, &pe) {
		return pe.Category, true
	}
	if errors.Is(err, ErrPushdownPermanent) {
		return "", true
	}
	return "", false
}

// TaskBridge is implemented in transport/council over the landed task
// engine and the extracted approve/execute/cancel cores. Group code
// never touches approvals or the Runner directly.
type TaskBridge interface {
	CreatePushdownTask(ctx context.Context, in PushdownTaskInput) (taskID string, err error)
	// ApproveTask runs the council approval (approval.Signer +
	// ApprovalRepository). It returns the approval record ID.
	ApproveTask(ctx context.Context, taskID, approverID string) (approvalID string, err error)
	// ExecuteTask runs executeTaskCore: wskind guard -> consume the
	// single-use approval -> Runner.
	ExecuteTask(ctx context.Context, taskID, actorID string) error
	CancelTask(ctx context.Context, taskID, actorID string) error
	TaskStatus(ctx context.Context, taskID string) (TaskStatus, error)
	// WatchTask delivers status changes until ctx ends or the task
	// reaches a final phase.
	WatchTask(ctx context.Context, taskID string) (<-chan TaskStatus, error)
}

// ModelWorkspaceLookup resolves an AI model's private workspace (P4
// aimodel.Service). Status is the aimodel workspace status ("ready"
// when execution may target it).
type ModelWorkspaceLookup interface {
	ModelWorkspace(ctx context.Context, aiModelID string) (workspaceID, status string, err error)
}

// ProfileProviderInvoker is an optional extension of ProviderInvoker:
// a red-team seat names a provider PROFILE + model (Part A role
// assignment), which may not be a registered AI model, so the seat
// cannot be invoked through the AIModelID-keyed Generate. Adapters
// that can invoke an arbitrary profile implement this; the transport
// adapter does (same profile -> secret -> provider construction as
// group dispatch).
type ProfileProviderInvoker interface {
	GenerateForProfile(ctx context.Context, profileID, model string, req InvokeRequest) (InvokeResult, error)
}

// ---- views ----

type RedTeamView struct {
	CheckID      string     `json:"check_id"`
	Status       string     `json:"status"`
	Independence string     `json:"independence"`
	SeatModel    string     `json:"seat_model,omitempty"`
	PlanHash     string     `json:"plan_hash"`
	Summary      string     `json:"summary,omitempty"` // same text as the redteam_result card
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type PushdownView struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	TaskID          string     `json:"task_id,omitempty"`
	WorkspaceID     string     `json:"workspace_id"`
	PlanHash        string     `json:"plan_hash"`
	ApprovedBy      string     `json:"approved_by"`
	ApprovedAt      time.Time  `json:"approved_at"`
	LastPhase       string     `json:"last_phase,omitempty"`
	LastError       string     `json:"last_error,omitempty"` // step + error text only; never task output
	FailureCategory string     `json:"failure_category,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// ApproveInput is the approve endpoint body. PlanHash is what the human
// saw; it must match the passing round's, the red-team check's and the
// current proposal's hash. ExpectedVersion is optional (0 = skip).
type ApproveInput struct {
	PlanHash        string `json:"plan_hash"`
	ExpectedVersion int    `json:"expected_version"`
}

// EnablePushdown installs the P8 ports. It is a setter (not a
// NewService parameter) so the landed constructor signature — and every
// P5–P7 call site — stays unchanged. Without it the service keeps the
// exact P7 behavior and Approve answers 503 pushdown_unavailable.
func (s *Service) EnablePushdown(seats RedTeamSeatSource, tasks TaskBridge, modelWS ModelWorkspaceLookup) {
	s.redTeamSeats, s.tasks, s.modelWS = seats, tasks, modelWS
	s.mirrors = newMirrorRegistry()
}

func (s *Service) pushdownEnabled() bool {
	return s.redTeamSeats != nil && s.tasks != nil && s.modelWS != nil
}
