package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P7 service surface: review rounds. See review.go for the model.
//
// Transaction discipline matches the landed code: multi-event
// operations run inside the per-group lock + one GroupRepo.Tx, using
// tx-bound repos (NewReviewRepo(tx.DB()) / NewRequirementRepo(tx.DB())),
// and publish their events only after commit, in allocation order.
// Event seqs stay gapless: NextSeqs(false) allocates ONLY an event seq
// (its message-seq return is the current value, not a fresh one), and
// NextSeqs(true) allocates a message seq + event seq pair that must be
// used together (the system notice below does exactly that).

func (s *Service) reviewRepo() *sqlite.ReviewRepo {
	return sqlite.NewReviewRepo(s.repo.DB())
}

// reviewCommit runs fn inside the group lock + one tx and publishes the
// events fn returns, in order, after commit.
func (s *Service) reviewCommit(ctx context.Context, gid string, fn func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error)) error {
	mu := s.lock(gid)
	mu.Lock()
	defer mu.Unlock()
	at := s.now()
	var evs []Event
	err := s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		var err error
		evs, err = fn(tx, at)
		return err
	})
	if err != nil {
		return mapRepoErr(err)
	}
	for _, e := range evs {
		s.hub.Publish(gid, e)
	}
	return nil
}

// rvAppendEvent allocates ONE event seq inside tx and appends the event.
func rvAppendEvent(ctx context.Context, tx *sqlite.GroupRepo, gid, typ string, data any, at time.Time) (Event, error) {
	_, evSeq, err := tx.NextSeqs(ctx, gid, false, at)
	if err != nil {
		return Event{}, err
	}
	rec, ev, err := buildEvent(gid, evSeq, typ, data, at)
	if err != nil {
		return Event{}, err
	}
	if err := tx.AppendEvent(ctx, rec); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// rvPostSystemTx posts a system notice message inside tx; its
// message.created event uses the SAME NextSeqs(true) allocation.
func rvPostSystemTx(ctx context.Context, tx *sqlite.GroupRepo, gid, rid, body string, payloadMap map[string]any, at time.Time) (Event, error) {
	payload, _ := json.Marshal(payloadMap)
	msgSeq, evSeq, err := tx.NextSeqs(ctx, gid, true, at)
	if err != nil {
		return Event{}, err
	}
	msg := &sqlite.ChatMessageRecord{
		ID: newID("msg_"), GroupID: gid, Seq: msgSeq, AuthorType: AuthorSystem,
		Kind: KindText, BodyMD: body, Payload: payload, RequirementID: rid,
		Status: "final", CreatedAt: at,
	}
	if err := tx.CreateMessage(ctx, msg); err != nil {
		return Event{}, err
	}
	rec, ev, err := buildEvent(gid, evSeq, "message.created", messageView(*msg, nil, nil), at)
	if err != nil {
		return Event{}, err
	}
	if err := tx.AppendEvent(ctx, rec); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// reviewerIDsFromMentions returns the distinct resolved member ids a
// review message mentions (humans and AIs), excluding the author.
func reviewerIDsFromMentions(mentions []sqlite.ChatMentionRecord, selfID string) []string {
	seen := map[string]bool{}
	var out []string
	for _, mn := range mentions {
		if mn.Resolution != MentionResolved || mn.TargetMemberID == "" || mn.TargetMemberID == selfID {
			continue
		}
		if seen[mn.TargetMemberID] {
			continue
		}
		seen[mn.TargetMemberID] = true
		out = append(out, mn.TargetMemberID)
	}
	return out
}

// ---------- opening a round ----------

type reviewerSpec struct {
	MemberID   string
	MemberType string
	AIModelID  string
	Handle     string
}

type reviewStartPlan struct {
	req       *sqlite.GroupRequirementRecord
	proposal  *sqlite.RequirementProposalRecord
	reviewers []reviewerSpec
	rule      string
	quorum    int
	deadline  int    // minutes, 0 = none
	roundID   string // set by rvStartReviewTx
}

// rvPrepareReview validates everything that can fail BEFORE any write:
// the caller (endpoint or chat path) runs it ahead of its transaction.
func (s *Service) rvPrepareReview(ctx context.Context, a Actor, g *sqlite.ChatGroupRecord, me *sqlite.ChatGroupMemberRecord, rid string, reviewerIDs []string) (*reviewStartPlan, error) {
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, g.ID, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.RequestedByUserID != a.UserID && !canManage(a, me) {
		return nil, errReviewForbidden
	}
	if rec.State != ReqProposed {
		return nil, errRequirementState
	}
	if _, err := s.reviewRepo().GetOpenRound(ctx, rid); err == nil {
		return nil, errReviewAlreadyOpen
	} else if !errors.Is(err, sqlite.ErrReviewNotFound) {
		return nil, err
	}
	proposal, err := s.reqRepo.GetProposal(ctx, rec.CurrentProposalID)
	if err != nil {
		return nil, err
	}
	policy := decodePolicy(g.Policy)
	minReviewers := policy.MinReviewers
	if minReviewers < 1 {
		minReviewers = 1
	}
	maxReviewers := policy.MaxReviewers
	if maxReviewers < minReviewers {
		maxReviewers = 5
	}
	if len(reviewerIDs) == 0 {
		return nil, errNoReviewerMention
	}
	if len(reviewerIDs) < minReviewers || len(reviewerIDs) > maxReviewers {
		return nil, errReviewerCount
	}
	_, views, err := s.authorMap(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]MemberView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	plan := &reviewStartPlan{req: rec, proposal: proposal, rule: policy.ProposalReviewRule, deadline: policy.ReviewDeadlineMinutes}
	if plan.rule == "" {
		plan.rule = "unanimous"
	}
	if policy.ProposalQuorum != nil {
		plan.quorum = *policy.ProposalQuorum
	}
	for _, id := range reviewerIDs {
		v, ok := byID[id]
		if !ok || v.RemovedAt != nil {
			return nil, errReviewerNotMember
		}
		if id == rec.AssigneeMemberID {
			return nil, errSelfReview
		}
		spec := reviewerSpec{MemberID: id, MemberType: v.MemberType, Handle: v.Handle}
		if v.MemberType == MemberAI {
			if v.AIStatus != "active" {
				return nil, errReviewerUnavailable
			}
			spec.AIModelID = v.AIModelID
		} else if v.GroupRole != RoleOwner && v.GroupRole != RoleOperator {
			return nil, errReviewerRole
		}
		plan.reviewers = append(plan.reviewers, spec)
	}
	return plan, nil
}

// rvStartReviewTx performs the proposed -> in_review transition, round +
// decision creation, AI job enqueue and the three events, all inside
// the caller's transaction. Events come back in publish order.
func (s *Service) rvStartReviewTx(ctx context.Context, tx *sqlite.GroupRepo, g *sqlite.ChatGroupRecord, actorUserID, actorMemberID, requestMessageID string, plan *reviewStartPlan, at time.Time) ([]Event, error) {
	txReq := sqlite.NewRequirementRepo(tx.DB())
	txRev := sqlite.NewReviewRepo(tx.DB())
	rec := plan.req
	ok, err := txReq.UpdateRequirementState(ctx, rec.ID, []string{ReqProposed}, rec.Version, map[string]any{
		"state": ReqInReview, "state_changed_at": at, "updated_at": at,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errRequirementGone
	}
	round := &sqlite.ReviewRoundRecord{
		ID: newID("rnd_"), GroupID: g.ID, RequirementID: rec.ID,
		ProposalID: plan.proposal.ID, ProposalRound: plan.proposal.Attempt,
		PlanHash: planHashOf(plan.proposal.PlanJSON), Status: RoundOpen,
		PolicyRule: plan.rule, PolicyQuorum: plan.quorum,
		StartedByUserID: actorUserID, StartedByMember: actorMemberID,
		RequestMessageID: requestMessageID, CreatedAt: at,
	}
	if plan.deadline > 0 {
		d := at.Add(time.Duration(plan.deadline) * time.Minute)
		round.DeadlineAt = &d
	}
	if err := txRev.CreateRound(ctx, round); err != nil {
		return nil, err
	}
	plan.roundID = round.ID
	refs := make([]ReviewerRef, 0, len(plan.reviewers))
	handles := make([]string, 0, len(plan.reviewers))
	for _, rv := range plan.reviewers {
		dec := &sqlite.ReviewDecisionRecord{
			ID: newID("rdc_"), RoundID: round.ID, GroupID: g.ID, RequirementID: rec.ID,
			ReviewerMember: rv.MemberID, ReviewerType: rv.MemberType,
			ReviewerAIModel: rv.AIModelID, Status: DecisionPending,
			CreatedAt: at, UpdatedAt: at,
		}
		if err := txRev.CreateDecision(ctx, dec); err != nil {
			return nil, err
		}
		if rv.MemberType == MemberAI {
			job := &sqlite.GroupJobRecord{
				ID: newID("job_"), GroupID: g.ID, RequirementID: rec.ID,
				Kind: JobKindReview, TargetID: dec.ID, State: "queued",
				SerialKey: g.ID + ":" + rv.MemberID, Priority: 10, MaxAttempts: 3,
				AvailableAt: at, CreatedAt: at, UpdatedAt: at,
			}
			if err := txReq.EnqueueJob(ctx, job); err != nil {
				return nil, err
			}
			if err := txRev.UpdateDecisionFields(ctx, dec.ID, map[string]any{"job_id": job.ID}); err != nil {
				return nil, err
			}
		}
		refs = append(refs, ReviewerRef{MemberID: rv.MemberID, MemberType: rv.MemberType, Handle: rv.Handle})
		handles = append(handles, "@"+rv.Handle)
	}
	evs := make([]Event, 0, 3)
	ev, err := rvAppendEvent(ctx, tx, g.ID, "requirement.state_changed",
		RequirementEventData{RequirementID: rec.ID, State: ReqInReview, Version: rec.Version + 1}, at)
	if err != nil {
		return nil, err
	}
	evs = append(evs, ev)
	ev, err = rvAppendEvent(ctx, tx, g.ID, "review.round_opened",
		ReviewEventData{RequirementID: rec.ID, RoundID: round.ID, ProposalRound: round.ProposalRound, Reviewers: refs}, at)
	if err != nil {
		return nil, err
	}
	evs = append(evs, ev)
	body := fmt.Sprintf("Review round %d opened for @%s's proposal. Reviewers: %s.",
		round.ProposalRound, rec.AssigneeHandle, strings.Join(handles, ", "))
	ev, err = rvPostSystemTx(ctx, tx, g.ID, rec.ID, body,
		map[string]any{"kind": "review_notice", "requirement_id": rec.ID, "round_id": round.ID}, at)
	if err != nil {
		return nil, err
	}
	evs = append(evs, ev)
	return evs, nil
}

// ---------- closing a round ----------

// rvCloseRoundTx closes the round and moves the requirement per the
// outcome. Superseded skips the requirement transition (the caller is
// cancelling it in the same flow). Caller guarantees no decision is
// pending/running, except for superseded where that is handled by the
// caller withdrawing them first.
func rvCloseRoundTx(ctx context.Context, tx *sqlite.GroupRepo, round *sqlite.ReviewRoundRecord, outcome, closedBy string, at time.Time) ([]Event, error) {
	txReq := sqlite.NewRequirementRepo(tx.DB())
	txRev := sqlite.NewReviewRepo(tx.DB())
	if err := txRev.UpdateRound(ctx, round.ID, map[string]any{
		"status": RoundClosed, "outcome": outcome, "closed_at": at, "closed_by": closedBy,
	}); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, 2)
	if outcome != OutcomeSuperseded {
		req, err := txReq.GetRequirement(ctx, round.RequirementID)
		if err != nil {
			return nil, err
		}
		fields := map[string]any{"state_changed_at": at, "updated_at": at}
		var toState string
		switch outcome {
		case OutcomePassed:
			toState = ReqReviewPassed
		case OutcomeChangesRequested:
			toState = ReqChangesRequested
		case OutcomeRejected:
			toState = ReqRejected
			fields["failure_code"] = FailReviewRejected
			fields["failure_detail"] = fmt.Sprintf("proposal rejected by review round %d", round.ProposalRound)
			fields["terminal_at"] = at
		default: // inconclusive
			toState = ReqProposed
		}
		fields["state"] = toState
		ok, err := txReq.UpdateRequirementState(ctx, req.ID, []string{ReqInReview}, req.Version, fields)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errRequirementGone
		}
		ev, err := rvAppendEvent(ctx, tx, round.GroupID, "requirement.state_changed",
			RequirementEventData{RequirementID: req.ID, State: toState, Version: req.Version + 1}, at)
		if err != nil {
			return nil, err
		}
		evs = append(evs, ev)
	}
	ev, err := rvAppendEvent(ctx, tx, round.GroupID, "review.closed",
		ReviewEventData{RequirementID: round.RequirementID, RoundID: round.ID, Outcome: outcome}, at)
	if err != nil {
		return nil, err
	}
	evs = append(evs, ev)
	return evs, nil
}

// rvMaybeCloseTx closes the round when every decision is final.
func rvMaybeCloseTx(ctx context.Context, tx *sqlite.GroupRepo, round *sqlite.ReviewRoundRecord, closedBy string, at time.Time) ([]Event, error) {
	txRev := sqlite.NewReviewRepo(tx.DB())
	decs, err := txRev.ListDecisions(ctx, round.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range decs {
		if d.Status == DecisionPending || d.Status == DecisionRunning {
			return nil, nil
		}
	}
	outcome := aggregateRound(decs, round.PolicyRule, round.PolicyQuorum)
	return rvCloseRoundTx(ctx, tx, round, outcome, closedBy, at)
}

// ---------- endpoints' service methods ----------

type StartReviewInput struct {
	ReviewerMemberIDs []string `json:"reviewer_member_ids"`
}

func (s *Service) StartReview(ctx context.Context, a Actor, gid, rid string, in StartReviewInput) (*ReviewStateView, error) {
	g, me, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil || s.provider == nil {
		return nil, errIntentUnavailable
	}
	plan, err := s.rvPrepareReview(ctx, a, g, me, rid, in.ReviewerMemberIDs)
	if err != nil {
		return nil, err
	}
	err = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		return s.rvStartReviewTx(ctx, tx, g, a.UserID, memberIDOf(me), "", plan, at)
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, a.UserID, "group.review_started", gid, "group_requirement", rid,
		map[string]any{"round_id": plan.roundID, "reviewers": len(plan.reviewers)})
	return s.GetReview(ctx, a, gid, rid)
}

func (s *Service) GetReview(ctx context.Context, a Actor, gid, rid string) (*ReviewStateView, error) {
	g, me, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	return s.buildReviewState(ctx, a, g, me, rec)
}

type SubmitDecisionInput struct {
	Verdict   string `json:"verdict"`
	Rationale string `json:"rationale"`
}

func (s *Service) SubmitDecision(ctx context.Context, a Actor, gid, rid string, in SubmitDecisionInput) (*ReviewStateView, error) {
	_, me, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	switch in.Verdict {
	case VerdictApprove, VerdictRequestChanges, VerdictReject:
	default:
		return nil, errInvalidVerdict
	}
	if utf8.RuneCountInString(in.Rationale) > 4000 {
		return nil, invalidParams("rationale must be at most 4000 characters")
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.State != ReqInReview || me == nil {
		return nil, errNoOpenRound
	}
	var roundID string
	err = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		txRev := sqlite.NewReviewRepo(tx.DB())
		round, err := txRev.GetOpenRound(ctx, rid)
		if err != nil {
			if errors.Is(err, sqlite.ErrReviewNotFound) {
				return nil, errNoOpenRound
			}
			return nil, err
		}
		roundID = round.ID
		dec, err := txRev.FindDecision(ctx, round.ID, me.ID)
		if err != nil {
			if errors.Is(err, sqlite.ErrReviewNotFound) {
				return nil, errNotAReviewer
			}
			return nil, err
		}
		if dec.Status != DecisionPending {
			return nil, errDecisionFinal
		}
		ok, err := txRev.TransitionDecision(ctx, dec.ID, []string{DecisionPending}, map[string]any{
			"status": DecisionDecided, "verdict": in.Verdict,
			"rationale":  truncateRunes(in.Rationale, 4000),
			"decided_at": at, "updated_at": at,
		})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errDecisionFinal
		}
		ev, err := rvAppendEvent(ctx, tx, gid, "review.decision_recorded",
			ReviewEventData{RequirementID: rid, RoundID: round.ID, DecisionID: dec.ID, ReviewerMemberID: me.ID}, at)
		if err != nil {
			return nil, err
		}
		evs := []Event{ev}
		closeEvs, err := rvMaybeCloseTx(ctx, tx, round, a.UserID, at)
		if err != nil {
			return nil, err
		}
		return append(evs, closeEvs...), nil
	})
	if err != nil {
		return nil, err
	}
	// The audit trail is server-side only; the group stream stays blind.
	s.record(ctx, a.UserID, "group.review_decision", gid, "group_requirement", rid,
		map[string]any{"round_id": roundID, "verdict": in.Verdict})
	return s.GetReview(ctx, a, gid, rid)
}

func (s *Service) ForceCloseReview(ctx context.Context, a Actor, gid, rid string) (*ReviewStateView, error) {
	_, me, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	if !canManage(a, me) {
		return nil, errReviewForbidden
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.State != ReqInReview {
		return nil, errNoOpenRound
	}
	err = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		txRev := sqlite.NewReviewRepo(tx.DB())
		round, err := txRev.GetOpenRound(ctx, rid)
		if err != nil {
			if errors.Is(err, sqlite.ErrReviewNotFound) {
				return nil, errNoOpenRound
			}
			return nil, err
		}
		decs, err := txRev.ListDecisions(ctx, round.ID)
		if err != nil {
			return nil, err
		}
		for i := range decs {
			d := &decs[i]
			if d.Status == DecisionPending || d.Status == DecisionRunning {
				ok, err := txRev.TransitionDecision(ctx, d.ID, []string{DecisionPending, DecisionRunning},
					map[string]any{"status": DecisionWithdrawn, "updated_at": at})
				if err != nil {
					return nil, err
				}
				if ok {
					d.Status = DecisionWithdrawn
				}
			}
		}
		outcome := aggregateRound(decs, round.PolicyRule, round.PolicyQuorum)
		return rvCloseRoundTx(ctx, tx, round, outcome, a.UserID, at)
	})
	if err != nil {
		return nil, err
	}
	s.record(ctx, a.UserID, "group.review_force_closed", gid, "group_requirement", rid, nil)
	return s.GetReview(ctx, a, gid, rid)
}

type ReviseInput struct {
	Note string `json:"note"`
}

// ReviseRequirement restarts the proposal pipeline after a
// changes_requested round: the closed round's revealed feedback (plus
// the requester's note) is fed into the next proposal prompt by
// priorReviewFeedback.
func (s *Service) ReviseRequirement(ctx context.Context, a Actor, gid, rid string, in ReviseInput) (*RequirementView, error) {
	g, me, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if s.reqRepo == nil {
		return nil, errIntentUnavailable
	}
	if utf8.RuneCountInString(in.Note) > 2000 {
		return nil, invalidParams("note must be at most 2000 characters")
	}
	rec, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if rec.RequestedByUserID != a.UserID && !canManage(a, me) {
		return nil, errReviewForbidden
	}
	if rec.State != ReqChangesRequested {
		return nil, errRequirementState
	}
	round, err := s.reviewRepo().GetLatestRound(ctx, rid)
	if err != nil || round.Status != RoundClosed || round.Outcome != OutcomeChangesRequested {
		return nil, errRequirementState
	}
	count, err := s.reqRepo.CountProposals(ctx, rid)
	if err != nil {
		return nil, err
	}
	maxRounds := decodePolicy(g.Policy).MaxProposalRounds
	if maxRounds < 1 {
		maxRounds = 3
	}
	if int(count) >= maxRounds {
		return nil, errRoundLimit
	}
	if err := s.reviewRepo().UpdateRound(ctx, round.ID, map[string]any{"revision_note": in.Note}); err != nil {
		return nil, err
	}
	now := s.now()
	_, err = s.mutate(ctx, gid, "requirement.state_changed", func(tx *sqlite.GroupRepo, at time.Time) (any, error) {
		txReq := sqlite.NewRequirementRepo(tx.DB())
		ok, err := txReq.UpdateRequirementState(ctx, rid, []string{ReqChangesRequested}, rec.Version, map[string]any{
			"state": ReqDispatched, "failure_code": "", "failure_detail": "",
			"current_proposal_id": "", "terminal_at": nil,
			"state_changed_at": now, "updated_at": now,
		})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errRequirementGone
		}
		job := &sqlite.GroupJobRecord{
			ID: newID("job_"), GroupID: gid, RequirementID: rid, Kind: JobKindPropose,
			State: "queued", SerialKey: gid + ":" + rec.AssigneeMemberID, MaxAttempts: 3,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := txReq.EnqueueJob(ctx, job); err != nil {
			return nil, err
		}
		return RequirementEventData{RequirementID: rid, State: ReqDispatched, Version: rec.Version + 1}, nil
	})
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("Revision requested for @%s's proposal (round %d).", rec.AssigneeHandle, count+1)
	if note := strings.TrimSpace(in.Note); note != "" {
		body += " Note: " + truncateRunes(note, 500)
	}
	_ = s.postSystemCard(ctx, rec, "review_notice", body, map[string]any{
		"kind": "review_notice", "requirement_id": rid, "round": count + 1,
	})
	s.record(ctx, a.UserID, "group.requirement_revised", gid, "group_requirement", rid,
		map[string]any{"round": count + 1})
	return s.GetRequirement(ctx, a, gid, rid)
}

// CloseOpenReviewAfterCancel closes a still-open round as superseded
// when its requirement is cancelled: outstanding decisions are
// withdrawn and verdicts are NEVER revealed for a superseded round.
func (s *Service) CloseOpenReviewAfterCancel(ctx context.Context, gid, rid string) error {
	if s.reqRepo == nil {
		return nil
	}
	return s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		txRev := sqlite.NewReviewRepo(tx.DB())
		round, err := txRev.GetOpenRound(ctx, rid)
		if err != nil {
			if errors.Is(err, sqlite.ErrReviewNotFound) {
				return nil, nil
			}
			return nil, err
		}
		decs, err := txRev.ListDecisions(ctx, round.ID)
		if err != nil {
			return nil, err
		}
		for i := range decs {
			d := &decs[i]
			if d.Status == DecisionPending || d.Status == DecisionRunning {
				if _, err := txRev.TransitionDecision(ctx, d.ID, []string{DecisionPending, DecisionRunning},
					map[string]any{"status": DecisionWithdrawn, "updated_at": at}); err != nil {
					return nil, err
				}
			}
		}
		return rvCloseRoundTx(ctx, tx, round, OutcomeSuperseded, "system", at)
	})
}

// ---------- allowed actions (per-actor, for RequirementView) ----------

func (s *Service) allowedActions(ctx context.Context, a Actor, me *sqlite.ChatGroupMemberRecord, rec *sqlite.GroupRequirementRecord) []string {
	canAct := rec.RequestedByUserID == a.UserID || canManage(a, me)
	var out []string
	switch rec.State {
	case ReqProposed:
		if canAct {
			out = append(out, "start_review")
		}
	case ReqInReview:
		if me != nil {
			if round, err := s.reviewRepo().GetOpenRound(ctx, rec.ID); err == nil {
				if d, err := s.reviewRepo().FindDecision(ctx, round.ID, me.ID); err == nil && d.Status == DecisionPending {
					out = append(out, "submit_decision")
				}
			}
		}
		if canManage(a, me) {
			out = append(out, "force_close")
		}
	case ReqChangesRequested:
		if canAct {
			out = append(out, "revise")
		}
	case ReqProposalFailed, ReqFailed:
		if canAct {
			out = append(out, "retry")
		}
	}
	switch rec.State {
	case ReqDispatched, ReqProposing, ReqProposed, ReqInReview, ReqChangesRequested, ReqReviewPassed:
		if canAct {
			out = append(out, "cancel")
		}
	}
	return out
}

// ---------- revision feedback for the next proposal ----------

// priorReviewFeedback returns the revealed feedback of the closed
// changes_requested round over the CURRENT latest proposal, or "" when
// the next proposal is not a revision.
func (s *Service) priorReviewFeedback(ctx context.Context, req *sqlite.GroupRequirementRecord) string {
	if s.reqRepo == nil {
		return ""
	}
	count, err := s.reqRepo.CountProposals(ctx, req.ID)
	if err != nil || count == 0 {
		return ""
	}
	rounds, err := s.reviewRepo().ListRounds(ctx, req.ID)
	if err != nil {
		return ""
	}
	for i := len(rounds) - 1; i >= 0; i-- {
		r := &rounds[i]
		if r.Status != RoundClosed || r.Outcome != OutcomeChangesRequested || r.ProposalRound != int(count) {
			continue
		}
		decs, err := s.reviewRepo().ListDecisions(ctx, r.ID)
		if err != nil {
			return ""
		}
		authors, _, _ := s.authorMap(ctx, req.GroupID)
		return formatPriorFeedback(r, decs, authors)
	}
	return ""
}

// ---------- the review worker ----------

// runReviewJob executes one claimed kind="review" job: it invokes the
// reviewer's model for a verdict on the round's bound plan and records
// the decision. Safe to re-run after a crash: a decision already final
// (or a round already closed/superseded) drains the job quietly, and a
// decision left "running" by a dead worker is simply re-executed — the
// final TransitionDecision CAS keeps the write single.
func (s *Service) runReviewJob(ctx context.Context, workerID string, job *sqlite.GroupJobRecord) error {
	if job.TargetID == "" {
		return s.completeJob(ctx, workerID, job)
	}
	dec, err := s.reviewRepo().GetDecision(ctx, job.TargetID)
	if err != nil {
		return s.completeJob(ctx, workerID, job) // decision gone; nothing to do
	}
	if dec.Status != DecisionPending && dec.Status != DecisionRunning {
		return s.completeJob(ctx, workerID, job) // already final
	}
	req, err := s.reqRepo.GetRequirement(ctx, job.RequirementID)
	if err != nil {
		return err
	}
	if req.State != ReqInReview {
		return s.completeJob(ctx, workerID, job) // cancelled / superseded meanwhile
	}
	round, err := s.reviewRepo().GetRound(ctx, dec.RoundID)
	if err != nil || round.Status != RoundOpen {
		return s.completeJob(ctx, workerID, job)
	}
	if dec.Status == DecisionPending {
		ok, err := s.reviewRepo().TransitionDecision(ctx, dec.ID, []string{DecisionPending},
			map[string]any{"status": DecisionRunning, "updated_at": s.now()})
		if err != nil {
			return err
		}
		if !ok {
			return s.completeJob(ctx, workerID, job) // another worker owns it
		}
	}
	proposal, err := s.reqRepo.GetProposal(ctx, round.ProposalID)
	if err != nil {
		return s.failReviewDecision(ctx, workerID, job, dec, FailInternal)
	}
	if planHashOf(proposal.PlanJSON) != round.PlanHash {
		return s.failReviewDecision(ctx, workerID, job, dec, FailReviewPlanChanged)
	}
	info, err := s.models.GetAIModel(ctx, dec.ReviewerAIModel)
	if err != nil || info.Status != "active" {
		return s.failReviewDecision(ctx, workerID, job, dec, FailReviewerModelGone)
	}
	g, err := s.repo.GetGroup(ctx, job.GroupID)
	if err != nil {
		return err
	}
	var acceptance []string
	if len(req.AcceptanceJSON) > 0 {
		_ = json.Unmarshal(req.AcceptanceJSON, &acceptance)
	}
	system, user := buildReviewPrompt(req.Title, string(proposal.PlanJSON), acceptance)
	msgs := []InvokeMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}
	if err := s.checkBudget(ctx, g, req.ID, info.Model, estimateTokens(msgs)); err != nil {
		if strings.HasPrefix(err.Error(), FailBudgetExhausted) {
			return s.failReviewDecision(ctx, workerID, job, dec, FailBudgetExhausted)
		}
		return err
	}
	res, invID, invErr := s.invokeAs(ctx, job, req, dec.ReviewerAIModel, info, msgs, PurposeReview)
	if invErr != nil {
		// Same retry discipline as proposals: requeue with backoff;
		// attempts exhausted -> the decision errors out.
		state, err := s.reqRepo.FailOrRetryJob(ctx, job.ID, workerID, s.now(), jobBackoff(job.Attempts), invErr)
		if err != nil {
			return err
		}
		if state == "failed" {
			return s.failReviewDecisionOnly(ctx, dec, FailProviderError)
		}
		return nil
	}
	verdict, rationale, ok := parseReviewVerdict(res.Content)
	if !ok {
		return s.failReviewDecision(ctx, workerID, job, dec, FailReviewInvalidReply)
	}
	err = s.reviewCommit(ctx, job.GroupID, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		txRev := sqlite.NewReviewRepo(tx.DB())
		okT, err := txRev.TransitionDecision(ctx, dec.ID, []string{DecisionPending, DecisionRunning}, map[string]any{
			"status": DecisionDecided, "verdict": verdict,
			"rationale": truncateRunes(rationale, 4000), "invocation_id": invID,
			"decided_at": at, "updated_at": at,
		})
		if err != nil {
			return nil, err
		}
		if !okT {
			return nil, nil // force-closed / superseded while the model ran
		}
		ev, err := rvAppendEvent(ctx, tx, job.GroupID, "review.decision_recorded",
			ReviewEventData{RequirementID: req.ID, RoundID: round.ID, DecisionID: dec.ID, ReviewerMemberID: dec.ReviewerMember}, at)
		if err != nil {
			return nil, err
		}
		evs := []Event{ev}
		roundNow, err := txRev.GetRound(ctx, round.ID)
		if err != nil {
			return nil, err
		}
		if roundNow.Status == RoundOpen {
			closeEvs, err := rvMaybeCloseTx(ctx, tx, roundNow, "system", at)
			if err != nil {
				return nil, err
			}
			evs = append(evs, closeEvs...)
		}
		return evs, nil
	})
	if err != nil {
		return err
	}
	return s.completeJob(ctx, workerID, job)
}

// failReviewDecision marks a decision errored (terminal for the job),
// closes the round if it was the last outstanding decision, and
// completes the job.
func (s *Service) failReviewDecision(ctx context.Context, workerID string, job *sqlite.GroupJobRecord, dec *sqlite.ReviewDecisionRecord, code string) error {
	if err := s.failReviewDecisionOnly(ctx, dec, code); err != nil {
		return err
	}
	return s.completeJob(ctx, workerID, job)
}

func (s *Service) failReviewDecisionOnly(ctx context.Context, dec *sqlite.ReviewDecisionRecord, code string) error {
	return s.reviewCommit(ctx, dec.GroupID, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		txRev := sqlite.NewReviewRepo(tx.DB())
		ok, err := txRev.TransitionDecision(ctx, dec.ID, []string{DecisionPending, DecisionRunning}, map[string]any{
			"status": DecisionError, "error": code, "updated_at": at,
		})
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		round, err := txRev.GetRound(ctx, dec.RoundID)
		if err != nil || round.Status != RoundOpen {
			return nil, nil
		}
		return rvMaybeCloseTx(ctx, tx, round, "system", at)
	})
}

// expireReviewJob runs when a review job's lease died for the last
// time: its decision errors out so the round can still close.
func (s *Service) expireReviewJob(ctx context.Context, job *sqlite.GroupJobRecord) error {
	if job.TargetID == "" {
		return nil
	}
	dec, err := s.reviewRepo().GetDecision(ctx, job.TargetID)
	if err != nil {
		return nil
	}
	if dec.Status != DecisionPending && dec.Status != DecisionRunning {
		return nil
	}
	return s.failReviewDecisionOnly(ctx, dec, FailReviewJobExhausted)
}
