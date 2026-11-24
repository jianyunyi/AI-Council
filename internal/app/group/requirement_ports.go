package group

import (
	"context"
	"net/http"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

// Part B P6: dispatch -> requirement -> proposal. A human posts a message
// with intent "dispatch" and @mentions of AI members; each resolved AI
// mention becomes one Requirement in state "dispatched". A worker drains
// the persistent job queue and asks the assignee's model for an execution
// plan (a "proposal"), validated server-side with council.ValidatePlan.
// P6 stops at "proposed": review, red team, human approval, handoff and
// execution are P7+ (see the P6 section of the Part B plan).
//
// Adaptation note (vs the Quick draft): the draft invented a second
// AIModelDirectory port ("invokeDir"); the landed P5 ports.go already
// defines AIModelDirectory + AIModelInfo, and Service.models is exactly
// that directory, so the pipeline reads model status/workspace through
// s.models. Only the provider invocation port is new here.

const (
	// Requirement states (P6 subset; review states arrive in P7).
	ReqDispatched     = "dispatched"
	ReqProposing      = "proposing"
	ReqProposed       = "proposed"
	ReqProposalFailed = "proposal_failed"
	ReqFailed         = "failed"
	ReqCancelled      = "cancelled"

	// Failure codes (stable strings; the UI switches on them).
	FailBudgetExhausted = "budget_exhausted"
	FailInvalidPlan     = "invalid_plan"
	FailProviderError   = "provider_unavailable"
	FailAssigneeGone    = "assignee_unavailable"
	FailInternal        = "internal"

	// Message kinds for requirement cards (P5 kinds: text; P7 adds result).
	KindDispatchCard = "dispatch_card"
	KindProposalCard = "proposal"

	// Job kinds.
	JobKindPropose = "propose"

	// Invocation purposes (budget ledger).
	PurposePropose       = "propose"
	PurposeProposeRepair = "propose_repair"
)

var (
	errDispatchForbidden  = newErr(http.StatusForbidden, "dispatch_forbidden", "only the group owner or an operator can dispatch requirements")
	errNoCallMentions     = newErr(http.StatusUnprocessableEntity, "no_ai_mention", "a dispatch message must @mention at least one AI member of this group")
	errAssigneeNotActive  = newErr(http.StatusUnprocessableEntity, "assignee_not_active", "the mentioned AI model is not active")
	errAssigneeWorkspace  = newErr(http.StatusUnprocessableEntity, "assignee_workspace_not_ready", "the AI model's workspace is not ready")
	errRequirementMissing = newErr(http.StatusNotFound, "requirement_not_found", "requirement not found")
	errRequirementState   = newErr(http.StatusConflict, "requirement_state_conflict", "requirement state does not allow this operation")
	errRequirementGone    = newErr(http.StatusGone, "requirement_gone", "requirement was already handled concurrently; reload")
)

// InvokeMessage is one chat message for a provider call.
type InvokeMessage struct {
	Role    string // system|user|assistant
	Content string
}

// InvokeRequest is a single model invocation on behalf of a group.
type InvokeRequest struct {
	AIModelID string
	Model     string
	Messages  []InvokeMessage
	// JSONMode asks the adapter to request structured JSON output (the
	// execution-plan schema) when the provider supports it.
	JSONMode    bool
	Temperature float64
}

// InvokeResult carries the reply plus accounting data for the ledger.
type InvokeResult struct {
	Content           string
	InputTokens       int64
	OutputTokens      int64
	ProviderRequestID string
}

// ProviderInvoker resolves an AI model to its provider profile and performs
// one invocation. Implemented in the transport layer over the P4 registry
// + providerkeys service (the same profile->provider mechanism the council
// factory in cmd/council-server uses). A nil ProviderInvoker on the
// Service disables dispatch (422 intent_not_available).
type ProviderInvoker interface {
	Generate(ctx context.Context, req InvokeRequest) (InvokeResult, error)
}

// RequirementView is the API DTO for one requirement. It embeds the current
// proposal (when proposed) so a dispatch card renders without a second
// fetch; the messages of kind dispatch_card/proposal carry only the id.
type RequirementView struct {
	ID                string        `json:"id"`
	GroupID           string        `json:"group_id"`
	AnchorMessageID   string        `json:"anchor_message_id"`
	RequestedBy       string        `json:"requested_by"`
	RequestedByMember string        `json:"requested_by_member_id"`
	AssigneeMemberID  string        `json:"assignee_member_id"`
	AssigneeAIModelID string        `json:"assignee_ai_model_id"`
	AssigneeHandle    string        `json:"assignee_handle"`
	Title             string        `json:"title"`
	BodyMD            string        `json:"body_md"`
	Acceptance        []string      `json:"acceptance,omitempty"`
	State             string        `json:"state"`
	FailureCode       string        `json:"failure_code,omitempty"`
	FailureDetail     string        `json:"failure_detail,omitempty"`
	Version           int           `json:"version"`
	Proposal          *ProposalView `json:"proposal,omitempty"`
	Job               *JobView      `json:"job,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	StateChangedAt    time.Time     `json:"state_changed_at"`
	ProposedAt        *time.Time    `json:"proposed_at,omitempty"`
	TerminalAt        *time.Time    `json:"terminal_at,omitempty"`
}

type ProposalView struct {
	ID        string               `json:"id"`
	Attempt   int                  `json:"attempt"`
	Plan      schema.ExecutionPlan `json:"plan"`
	Summary   string               `json:"summary"`
	CreatedAt time.Time            `json:"created_at"`
}

type JobView struct {
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
}

type RequirementPage struct {
	Items      []RequirementView `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// RequirementEventData is the payload of requirement.state_changed events.
type RequirementEventData struct {
	RequirementID string `json:"requirement_id"`
	State         string `json:"state"`
	Version       int    `json:"version"`
	FailureCode   string `json:"failure_code,omitempty"`
}

type ListRequirementsInput struct {
	State            string
	AssigneeMemberID string
	Cursor           string
	Limit            int
}
