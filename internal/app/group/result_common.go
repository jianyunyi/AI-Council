package group

import (
	"net/http"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P9 shared constants and helpers: result states, review
// stages, submission statuses, execution outcomes and the caps that
// bound the result revision loop.

const (
	ReqResultSubmitted = "result_submitted"
	ReqResultInReview  = "result_in_review"
	ReqCompleted       = "completed" // terminal

	StageProposal = "proposal"
	StageResult   = "result"

	JobKindResult  = "result"
	PurposeResult  = "result_summary"
	KindResultCard = "result_submission"

	SubCollecting  = "collecting"
	SubSummarizing = "summarizing"
	SubSubmitted   = "submitted"

	ExecSucceeded   = "succeeded"
	ExecFailed      = "failed"
	ExecCancelled   = "cancelled"
	ExecNotExecuted = "not_executed"

	CompletionAccepted       = "accepted"
	CompletionAcceptedFailed = "accepted_failed"

	RevisionFromProposal = "proposal"
	RevisionFromResult   = "result"

	defaultMaxCycles         = 2 // fallback when policy.MaxResultRounds <= 0
	defaultMaxProposalRounds = 3 // fallback when policy.MaxProposalRounds <= 0

	maxArtifactsPerResult = 50
	maxArtifactBytes      = 10 << 20
	maxResultTotalBytes   = 50 << 20
	maxExcerptBytes       = 32 << 10
	maxExcerptTotalBytes  = 96 << 10
)

var allowedArtifactKinds = map[string]bool{"file": true, "patch": true, "diff": true, "report": true} // W9

var (
	errResultsDisabled     = newErr(http.StatusServiceUnavailable, "results_unavailable", "result handling is not configured on this server")
	errNotResultReviewable = newErr(http.StatusConflict, "not_result_reviewable", "a result review can only start from result_submitted")
	errCycleLimit          = newErr(http.StatusConflict, "cycle_limit_reached", "the group's max_result_rounds has been reached")
	errArtifactNotFound    = newErr(http.StatusNotFound, "artifact_not_found", "artifact not found")
)

func (s *Service) results() *sqlite.ResultRepo { return sqlite.NewResultRepo(s.repo.DB()) }

// isTerminal is the Part B terminal-state set (C1): execution_failed
// is NOT terminal in P9 — a result submission (and possibly a whole
// new execution cycle) still follows it. executed is not terminal
// either; result_submitted/result_in_review continue from it.
func isTerminal(state string) bool {
	switch state {
	case ReqCancelled, ReqFailed, ReqProposalFailed, ReqRejected, ReqCompleted:
		return true
	}
	return false
}

func maxCycles(p Policy) int {
	if p.MaxResultRounds > 0 {
		return p.MaxResultRounds
	}
	return defaultMaxCycles
}

func maxAttemptsPerCycle(p Policy) int {
	if p.MaxProposalRounds > 0 {
		return p.MaxProposalRounds
	}
	return defaultMaxProposalRounds
}

// execOutcomeOf classifies a finished push-down for its submission:
// a permanent refusal before a task existed (or a plan change) means
// nothing ever ran; a cancelled task is its own outcome.
func execOutcomeOf(pdStatus, failureCode string) string {
	switch {
	case pdStatus == PDSucceeded:
		return ExecSucceeded
	case pdStatus == PDCancelled || failureCode == "cancelled":
		return ExecCancelled
	case failureCode == "plan_changed" || len(failureCode) > 17 && failureCode[:17] == "pushdown_refused_":
		return ExecNotExecuted
	default:
		return ExecFailed
	}
}
