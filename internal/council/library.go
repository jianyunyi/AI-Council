package council

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

// This file holds standalone council steps that do not depend on Workflow.
// Workflow uses them, and the group-chat requirement pipeline (Part B) calls
// them directly. Prompts, parsing and seat semantics stay in Engine.

// ErrNoEligibleReviewers is returned by ReviewProposal when every reviewer
// seat was excluded because it authored the proposal.
var ErrNoEligibleReviewers = errors.New("no eligible reviewers")

var errNoSeats = errors.New("no seats configured")

// FailoverHook is called synchronously after each seat fails, before the next
// seat is tried. willFailover is true when another seat follows.
type FailoverHook func(failure SeatFailure, willFailover bool)

type failoverConfig struct {
	onFailure FailoverHook
}

// FailoverOption configures JudgeProposals and RedTeamDecision.
type FailoverOption func(*failoverConfig)

// OnSeatFailure registers a hook that runs after each failed seat.
func OnSeatFailure(hook FailoverHook) FailoverOption {
	return func(c *failoverConfig) { c.onFailure = hook }
}

// ReviewProposal runs a blind peer review of ONE proposal by N reviewers,
// reusing Engine.ReviewWithFailures (same anonymous prompt and schema).
// Reviewers whose seat ID equals the proposal author's seat ID are excluded.
// Failed reviewers are dropped and returned as SeatFailures; an error is
// returned only when no review survives.
// Note: the proposal is presented under alias proposalAlias(0), as it is the
// only proposal in the review set.
func ReviewProposal(ctx context.Context, engine *Engine, proposal Generated[schema.Proposal], reviewers []Seat) ([]Generated[schema.PeerReview], []SeatFailure, error) {
	if engine == nil {
		return nil, nil, errors.New("council engine is required")
	}
	if len(reviewers) == 0 {
		return nil, nil, errors.New("proposals and reviewers are required")
	}
	eligible := make([]Seat, 0, len(reviewers))
	for _, reviewer := range reviewers {
		if reviewer.ID == proposal.Seat.ID {
			continue
		}
		eligible = append(eligible, reviewer)
	}
	if len(eligible) == 0 {
		return nil, nil, ErrNoEligibleReviewers
	}
	return engine.ReviewWithFailures(ctx, []Generated[schema.Proposal]{proposal}, eligible)
}

// JudgeProposals tries each judge seat in order and returns the first
// success. This matches the Workflow judge failover semantics: when every
// seat fails, the error wraps ErrJudgeUnavailable with the last seat's error.
func JudgeProposals(ctx context.Context, engine *Engine, proposals []Generated[schema.Proposal], reviews []Generated[schema.PeerReview], judgeSeats []Seat, opts ...FailoverOption) (Generated[schema.CouncilDecision], []SeatFailure, error) {
	if engine == nil {
		return Generated[schema.CouncilDecision]{}, nil, errors.New("council engine is required")
	}
	return runFailover(judgeSeats, ErrJudgeUnavailable, opts, func(seat Seat) (Generated[schema.CouncilDecision], error) {
		return engine.Judge(ctx, proposals, reviews, seat)
	})
}

// RedTeamDecision tries each red-team seat in order and returns the first
// success. This matches the Workflow red-team failover semantics: when every
// seat fails, the error wraps ErrRedTeamUnavailable with the last error.
func RedTeamDecision(ctx context.Context, engine *Engine, decision schema.CouncilDecision, redTeamSeats []Seat, opts ...FailoverOption) (Generated[schema.RedTeamReport], []SeatFailure, error) {
	if engine == nil {
		return Generated[schema.RedTeamReport]{}, nil, errors.New("council engine is required")
	}
	return runFailover(redTeamSeats, ErrRedTeamUnavailable, opts, func(seat Seat) (Generated[schema.RedTeamReport], error) {
		return engine.RedTeam(ctx, decision, seat)
	})
}

func runFailover[T any](seats []Seat, unavailable error, opts []FailoverOption, call func(Seat) (Generated[T], error)) (Generated[T], []SeatFailure, error) {
	var cfg failoverConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if len(seats) == 0 {
		return Generated[T]{}, nil, fmt.Errorf("%w: %v", unavailable, errNoSeats)
	}
	var failures []SeatFailure
	var lastErr error
	for i, seat := range seats {
		result, err := call(seat)
		if err == nil {
			return result, failures, nil
		}
		lastErr = err
		failure := SeatFailure{Seat: seat, Err: err}
		failures = append(failures, failure)
		if cfg.onFailure != nil {
			cfg.onFailure(failure, i+1 < len(seats))
		}
	}
	return Generated[T]{}, failures, fmt.Errorf("%w: %v", unavailable, lastErr)
}

// Plan issue codes. Message texts are the exact BuildExecutionPlan error
// strings.
const (
	PlanIssueBlockingFindings = "blocking_findings"
	PlanIssueEmptyPlan        = "empty_plan"
	PlanIssueInvalidVersion   = "invalid_version"
	PlanIssueUnsafePatchPath  = "unsafe_patch_path"
	PlanIssueInvalidCommand   = "invalid_command"
	PlanIssueUnsafeWorkDir    = "unsafe_workdir"
)

const (
	msgBlockingFindings = "red-team report contains blocking findings"
	msgEmptyPlan        = "execution plan must include patches or commands"
	msgInvalidVersion   = "execution plan version must be positive"
	msgUnsafePatchPath  = "execution plan patch path must be a nonempty relative path"
	msgInvalidCommand   = "execution plan commands require an executable and positive timeout"
	msgUnsafeWorkDir    = "execution plan command work directory must be empty, dot, or a relative path"
)

// PlanIssue is one validation finding. Field locates it: "", "version",
// "patches[i]", "commands[i]" or "verification_commands[i]". Index is -1
// when it does not apply, otherwise the index within the located slice.
type PlanIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Index   int    `json:"index"`
}

func (i PlanIssue) Error() string { return i.Message }

// ValidatePlan returns every issue in the order BuildExecutionPlan checks
// for them, so issues[0] is always the error BuildExecutionPlan would
// return. An empty result means the plan is acceptable.
func ValidatePlan(plan schema.ExecutionPlan, report schema.RedTeamReport) []PlanIssue {
	var issues []PlanIssue
	if len(report.Blocking) > 0 {
		issues = append(issues, PlanIssue{Code: PlanIssueBlockingFindings, Message: msgBlockingFindings, Index: -1})
	}
	if len(plan.Patches) == 0 && len(plan.Commands) == 0 {
		issues = append(issues, PlanIssue{Code: PlanIssueEmptyPlan, Message: msgEmptyPlan, Index: -1})
	}
	if plan.Version <= 0 {
		issues = append(issues, PlanIssue{Code: PlanIssueInvalidVersion, Message: msgInvalidVersion, Field: "version", Index: -1})
	}
	for i, patch := range plan.Patches {
		if !isSafeRelativePath(patch.Path) {
			issues = append(issues, PlanIssue{Code: PlanIssueUnsafePatchPath, Message: msgUnsafePatchPath, Field: fmt.Sprintf("patches[%d]", i), Index: i})
		}
	}
	for i, command := range plan.Commands {
		issues = append(issues, validatePlanCommand(command, fmt.Sprintf("commands[%d]", i), i)...)
	}
	for i, command := range plan.VerificationCommands {
		issues = append(issues, validatePlanCommand(command, fmt.Sprintf("verification_commands[%d]", i), i)...)
	}
	return issues
}

func validatePlanCommand(command schema.Command, field string, index int) []PlanIssue {
	var issues []PlanIssue
	if strings.TrimSpace(command.Executable) == "" || command.TimeoutSeconds <= 0 {
		issues = append(issues, PlanIssue{Code: PlanIssueInvalidCommand, Message: msgInvalidCommand, Field: field, Index: index})
	}
	if !isSafeWorkDir(command.WorkDir) {
		issues = append(issues, PlanIssue{Code: PlanIssueUnsafeWorkDir, Message: msgUnsafeWorkDir, Field: field, Index: index})
	}
	return issues
}
