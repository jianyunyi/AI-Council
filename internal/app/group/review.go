package group

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P7: review rounds over a proposal.
//
// A round is opened over the requirement's CURRENT proposal (state
// proposed -> in_review). Reviewers are group members chosen at open
// time: humans vote through the decision endpoint, AI members are
// invoked by the worker as kind="review" jobs whose TargetID is their
// decision row. Verdicts are BLIND while the round is open — neither
// the review view nor any event payload carries a verdict until the
// round closes (buildReviewState enforces the view side; the event
// payloads below are verdict-free by construction). When every decision
// is final (decided | error | withdrawn) the round closes and a FIXED
// aggregation rule (the group's proposal_review_rule / proposal_quorum
// policy snapshot) maps the verdicts to an outcome:
//
//	passed            -> review_passed   (P8 picks it up: red team,
//	                                     human approval, handoff)
//	changes_requested -> changes_requested (ReviseRequirement re-enters
//	                                     the proposal pipeline with the
//	                                     round's feedback in the prompt)
//	rejected          -> rejected (terminal, reason review_rejected)
//	inconclusive      -> back to proposed (e.g. reviewers errored or
//	                     withdrew with nobody objecting)
//	superseded        -> the requirement was cancelled mid-review; the
//	                     round closes WITHOUT revealing verdicts
//
// Deliberately NOT used here: council.JudgeProposals picks the best of
// several competing plans (a model call); a review round is a vote over
// ONE plan, so counting is deterministic and free. Two rules from the
// council library DO carry over: the proposer never reviews their own
// proposal, and every outcome is persisted.

const (
	RoundOpen   = "open"
	RoundClosed = "closed"

	OutcomePassed            = "passed"
	OutcomeChangesRequested  = "changes_requested"
	OutcomeRejected          = "rejected"
	OutcomeInconclusive      = "inconclusive"
	OutcomeSuperseded        = "superseded"
	OutcomeResultPassed      = "result_passed"       // P8
	OutcomeResultChanges     = "result_changes"      // P8
	OutcomeResultRejected    = "result_rejected"     // P8
	OutcomeResultInconclusiv = "result_inconclusive" // P8

	DecisionPending   = "pending"
	DecisionRunning   = "running"
	DecisionDecided   = "decided"
	DecisionError     = "error"
	DecisionWithdrawn = "withdrawn"

	VerdictApprove        = "approve"
	VerdictRequestChanges = "request_changes"
	VerdictReject         = "reject"
)

var (
	errRequirementRequired = newErr(http.StatusUnprocessableEntity, "requirement_required", "intent \"review\" requires requirement_id")
	errReviewForbidden     = newErr(http.StatusForbidden, "review_forbidden", "only the requester or a group manager can do this")
	errNoOpenRound         = newErr(http.StatusConflict, "no_open_review", "this requirement has no open review round")
	errReviewAlreadyOpen   = newErr(http.StatusConflict, "review_already_open", "a review round is already open for this requirement")
	errNotAReviewer        = newErr(http.StatusForbidden, "not_a_reviewer", "you are not a reviewer in the open round")
	errDecisionFinal       = newErr(http.StatusConflict, "decision_already_final", "your decision is already final")
	errReviewerCount       = newErr(http.StatusUnprocessableEntity, "reviewer_count", "reviewer count is outside the group policy bounds")
	errReviewerNotMember   = newErr(http.StatusUnprocessableEntity, "reviewer_not_member", "every reviewer must be an active group member")
	errReviewerUnavailable = newErr(http.StatusUnprocessableEntity, "reviewer_unavailable", "an AI reviewer is not active")
	errReviewerRole        = newErr(http.StatusUnprocessableEntity, "reviewer_role", "human reviewers must be owners or operators")
	errSelfReview          = newErr(http.StatusUnprocessableEntity, "self_review", "the assignee cannot review their own proposal")
	errInvalidVerdict      = newErr(http.StatusUnprocessableEntity, "invalid_verdict", "verdict must be approve|request_changes|reject")
	errRoundLimit          = newErr(http.StatusConflict, "round_limit_reached", "the proposal round limit for this requirement is exhausted")
	errNoReviewerMention   = newErr(http.StatusUnprocessableEntity, "no_reviewer_mention", "mention at least one reviewer")
)

// Failure codes recorded on review decisions that end in status error.
const (
	FailReviewPlanChanged  = "plan_changed"
	FailReviewInvalidReply = "invalid_verdict_format"
	FailReviewerModelGone  = "reviewer_unavailable"
	FailReviewJobExhausted = "job_attempts_exhausted"
)

// ---------- views ----------

type ReviewDecisionView struct {
	ID               string     `json:"id"`
	ReviewerMemberID string     `json:"reviewer_member_id"`
	ReviewerType     string     `json:"reviewer_type"`
	ReviewerHandle   string     `json:"reviewer_handle,omitempty"`
	Status           string     `json:"status"`
	Verdict          string     `json:"verdict,omitempty"` // revealed only after close
	Rationale        string     `json:"rationale,omitempty"`
	Error            string     `json:"error,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
}

type ReviewRoundView struct {
	ID              string               `json:"id"`
	ProposalRound   int                  `json:"proposal_round"`
	Status          string               `json:"status"`
	Outcome         string               `json:"outcome,omitempty"`
	Rule            string               `json:"rule"`
	Quorum          int                  `json:"quorum,omitempty"`
	StartedByMember string               `json:"started_by_member_id"`
	DeadlineAt      *time.Time           `json:"deadline_at,omitempty"`
	RevisionNote    string               `json:"revision_note,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	ClosedAt        *time.Time           `json:"closed_at,omitempty"`
	Decisions       []ReviewDecisionView `json:"decisions"`
	// P9: the round's stage (proposal|result); SubmissionID names the
	// reviewed result submission for stage=result rounds.
	Stage        string `json:"stage"`
	SubmissionID string `json:"submission_id,omitempty"`
}

type ReviewStateView struct {
	RequirementID string            `json:"requirement_id"`
	State         string            `json:"state"`
	Round         int               `json:"round"` // current proposal attempt
	CanDecide     bool              `json:"can_decide"`
	CanForceClose bool              `json:"can_force_close"`
	CanRevise     bool              `json:"can_revise"`
	Rounds        []ReviewRoundView `json:"rounds"`
}

// ReviewEventData is the payload of review.* events. It NEVER carries a
// verdict or rationale: decision_recorded announces THAT a decision
// landed, and review.closed fires only once verdicts are public in the
// review view anyway.
type ReviewEventData struct {
	RequirementID    string        `json:"requirement_id"`
	RoundID          string        `json:"round_id"`
	DecisionID       string        `json:"decision_id,omitempty"`
	ReviewerMemberID string        `json:"reviewer_member_id,omitempty"`
	ProposalRound    int           `json:"proposal_round,omitempty"`
	Outcome          string        `json:"outcome,omitempty"`
	State            string        `json:"state,omitempty"`
	Reviewers        []ReviewerRef `json:"reviewers,omitempty"`
	Stage            string        `json:"stage,omitempty"` // P9: proposal|result
}

type ReviewerRef struct {
	MemberID   string `json:"member_id"`
	MemberType string `json:"member_type"`
	Handle     string `json:"handle,omitempty"`
}

// planHashOf fingerprints a proposal's plan; a review round binds to
// the hash at open time so a swapped plan is detectable. The hash is
// over the CANONICAL form (parse into schema.ExecutionPlan, re-marshal):
// proposal PlanJSON is raw model output whose byte layout (key order,
// whitespace, omitted empty fields) is not stable, while the P8 task
// bridge can only recompute the hash from the parsed plan a task
// carries. Canonicalizing keeps both sides in agreement; unparseable
// input falls back to the raw bytes.
func planHashOf(planJSON []byte) string {
	if canonical, err := canonicalPlanJSON(planJSON); err == nil {
		planJSON = canonical
	}
	sum := sha256.Sum256(planJSON)
	return hex.EncodeToString(sum[:])
}

func canonicalPlanJSON(planJSON []byte) ([]byte, error) {
	var plan schema.ExecutionPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return nil, err
	}
	return json.Marshal(plan)
}

// PlanHashOf is planHashOf exported for the council task bridge (P8,
// W3): the bridge recomputes the group plan hash over a task's plan so
// push-down can prove the task still carries the reviewed plan. It MUST
// stay the same function that stamps review_rounds.plan_hash.
func PlanHashOf(planJSON []byte) string { return planHashOf(planJSON) }

// aggregateRound maps a CLOSED round's decisions to an outcome.
// error/withdrawn decisions never count as approval: under "unanimous"
// they make the round inconclusive unless someone objected; under
// "quorum" they simply do not contribute to the quorum.
func aggregateRound(decisions []sqlite.ReviewDecisionRecord, rule string, quorum int) string {
	total := len(decisions)
	decided, approves := 0, 0
	for _, d := range decisions {
		if d.Status != DecisionDecided {
			continue
		}
		decided++
		switch d.Verdict {
		case VerdictReject:
			return OutcomeRejected
		case VerdictRequestChanges:
			return OutcomeChangesRequested
		case VerdictApprove:
			approves++
		}
	}
	if decided == 0 || approves != decided {
		return OutcomeInconclusive
	}
	// Everyone who decided approved.
	if rule == "quorum" {
		if quorum > 0 && decided >= quorum {
			return OutcomePassed
		}
		return OutcomeInconclusive
	}
	// unanimous (default): every reviewer must have decided approve —
	// an errored or withdrawn reviewer blocks the pass.
	if decided == total {
		return OutcomePassed
	}
	return OutcomeInconclusive
}

// ---------- AI reviewer prompt ----------

const reviewSystemPrompt = "You are an AI reviewer in a shared planning group. " +
	"Another AI proposed an execution plan for a requirement. Judge ONLY the plan as written: " +
	"does it satisfy the requirement and its acceptance criteria, is it safe, is anything essential missing? " +
	"Respond with a single JSON object: {\"verdict\": \"approve\" | \"request_changes\" | \"reject\", \"rationale\": \"...\"}. " +
	"Use request_changes for fixable gaps, reject only for plans that should not proceed at all. " +
	"Do not add prose outside the JSON object."

func buildReviewPrompt(reqTitle string, planJSON string, acceptance []string) (system, user string) {
	var b strings.Builder
	b.WriteString("Requirement: ")
	b.WriteString(reqTitle)
	if len(acceptance) > 0 {
		b.WriteString("\n\nAcceptance criteria:")
		for _, a := range acceptance {
			b.WriteString("\n- ")
			b.WriteString(a)
		}
	}
	b.WriteString("\n\nProposed execution plan (JSON):\n")
	b.WriteString(planJSON)
	b.WriteString("\n\nReply with the verdict JSON only.")
	return reviewSystemPrompt, b.String()
}

// parseReviewVerdict extracts {verdict, rationale} from a model reply,
// tolerating fences/prose the same way parsePlan does.
func parseReviewVerdict(content string) (verdict, rationale string, ok bool) {
	raw := extractJSONObject(content)
	if raw == "" {
		return "", "", false
	}
	var v struct {
		Verdict   string `json:"verdict"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", "", false
	}
	switch v.Verdict {
	case VerdictApprove, VerdictRequestChanges, VerdictReject:
		return v.Verdict, v.Rationale, true
	}
	return "", "", false
}

// ---------- state view (blind until close) ----------

// buildReviewState renders the review view for one requirement. THE
// blind rule lives here: while a round is open its decisions expose
// only identity + status — verdict, rationale and error stay hidden
// until the round closes, and a superseded round never reveals them.
func (s *Service) buildReviewState(ctx context.Context, a Actor, g *sqlite.ChatGroupRecord, me *sqlite.ChatGroupMemberRecord, rec *sqlite.GroupRequirementRecord) (*ReviewStateView, error) {
	authors, _, err := s.authorMap(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	rounds, err := s.reviewRepo().ListRounds(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	out := &ReviewStateView{
		RequirementID: rec.ID, State: rec.State, Rounds: make([]ReviewRoundView, 0, len(rounds)),
		CanRevise: rec.State == ReqChangesRequested &&
			(rec.RequestedByUserID == a.UserID || canManage(a, me)),
	}
	if len(rounds) > 0 {
		out.Round = rounds[len(rounds)-1].ProposalRound
	} else if rec.CurrentProposalID != "" {
		if p, err := s.reqRepo.GetProposal(ctx, rec.CurrentProposalID); err == nil {
			out.Round = p.Attempt
		}
	}
	for i := range rounds {
		r := &rounds[i]
		decs, err := s.reviewRepo().ListDecisions(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		rv := ReviewRoundView{
			ID: r.ID, ProposalRound: r.ProposalRound, Status: r.Status, Outcome: r.Outcome,
			Rule: r.PolicyRule, Quorum: r.PolicyQuorum, StartedByMember: r.StartedByMember,
			DeadlineAt: r.DeadlineAt, RevisionNote: r.RevisionNote,
			CreatedAt: r.CreatedAt, ClosedAt: r.ClosedAt,
			Decisions: make([]ReviewDecisionView, 0, len(decs)),
			Stage:     r.Stage, SubmissionID: r.SubmissionID,
		}
		if rv.Stage == "" {
			rv.Stage = StageProposal // pre-P9 rows
		}
		revealed := r.Status == RoundClosed && r.Outcome != OutcomeSuperseded
		for j := range decs {
			d := &decs[j]
			dv := ReviewDecisionView{
				ID: d.ID, ReviewerMemberID: d.ReviewerMember, ReviewerType: d.ReviewerType,
				Status: d.Status,
			}
			if av, ok := authors[d.ReviewerMember]; ok {
				dv.ReviewerHandle = av.Handle
			}
			if revealed {
				dv.Verdict, dv.Rationale, dv.Error, dv.DecidedAt = d.Verdict, d.Rationale, d.Error, d.DecidedAt
			}
			rv.Decisions = append(rv.Decisions, dv)
			if r.Status == RoundOpen && me != nil && d.ReviewerMember == me.ID && d.Status == DecisionPending {
				out.CanDecide = true
			}
		}
		if r.Status == RoundOpen && canManage(a, me) {
			out.CanForceClose = true
		}
		out.Rounds = append(out.Rounds, rv)
	}
	return out, nil
}

// formatPriorFeedback renders a closed changes_requested round's
// revealed decisions (+ the requester's revision note) for the next
// proposal prompt.
func formatPriorFeedback(round *sqlite.ReviewRoundRecord, decisions []sqlite.ReviewDecisionRecord, authors map[string]MemberView) string {
	var b strings.Builder
	for _, d := range decisions {
		if d.Status != DecisionDecided {
			continue
		}
		if d.Verdict == VerdictApprove && strings.TrimSpace(d.Rationale) == "" {
			continue
		}
		who := d.ReviewerMember
		if av, ok := authors[d.ReviewerMember]; ok && av.Handle != "" {
			who = "@" + av.Handle
		}
		fmt.Fprintf(&b, "- %s voted %s: %s\n", who, d.Verdict, truncateRunes(strings.TrimSpace(d.Rationale), 500))
	}
	if note := strings.TrimSpace(round.RevisionNote); note != "" {
		fmt.Fprintf(&b, "- Requester note: %s\n", truncateRunes(note, 1000))
	}
	return strings.TrimSpace(b.String())
}
