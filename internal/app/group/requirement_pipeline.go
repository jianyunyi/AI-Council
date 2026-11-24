package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// The proposal pipeline (P6 §3 of the plan): a worker claims a "propose"
// job, transitions its requirement dispatched -> proposing, asks the
// assignee's model for a plan (budget pre-flight first), validates it with
// council.ValidatePlan, and commits proposal + proposal card +
// requirement.state_changed in one transaction. Every failure path lands
// the requirement in a terminal state with a stable failure code and a
// system card in the stream — a requirement never strands in "proposing"
// once its job is done.
//
// Corrections vs the Quick draft, found against the landed code:
//   - Event sequence numbers must be gapless (P5's SSE replay test
//     asserts it). The draft allocated a message seq via NextSeqs(true),
//     discarded its event seq, then allocated ANOTHER event seq for the
//     card's message.created event — leaving holes. Here the evSeq that
//     comes WITH the message seq is the card event's seq.
//   - The draft's retry path was dead: after a provider failure the
//     requirement sits in "proposing", and its startRequirement treated
//     any non-"dispatched" state as done, so a re-queued job never called
//     the provider again. "proposing" is a resumable state here (a fresh
//     job after a crash or a retry re-enters generation).

// runJob dispatches one claimed job by kind (propose in P6, review in
// P7). Unknown kinds drain quietly so a stale binary never wedges the
// queue.
func (s *Service) runJob(ctx context.Context, workerID string, job *sqlite.GroupJobRecord) error {
	switch job.Kind {
	case JobKindReview:
		return s.runReviewJob(ctx, workerID, job)
	case JobKindRedTeam:
		return s.runRedTeamJob(ctx, workerID, job)
	default:
		return s.runProposalJob(ctx, workerID, job)
	}
}

// runProposalJob executes one claimed job to completion (or requeue).
func (s *Service) runProposalJob(ctx context.Context, workerID string, job *sqlite.GroupJobRecord) error {
	req, err := s.reqRepo.GetRequirement(ctx, job.RequirementID)
	if err != nil {
		return err
	}
	switch req.State {
	case ReqCancelled, ReqProposed, ReqProposalFailed, ReqFailed:
		// Terminal or already satisfied (e.g. a duplicate job after a
		// crash between commit and job completion): drain quietly.
		return s.completeJob(ctx, workerID, job)
	}
	if err := s.startRequirement(ctx, req); err != nil {
		return err
	}
	req, err = s.reqRepo.GetRequirement(ctx, job.RequirementID)
	if err != nil {
		return err
	}
	if req.State != ReqProposing {
		// Cancelled concurrently, or the CAS was lost to another worker.
		return s.completeJob(ctx, workerID, job)
	}
	return s.generateProposal(ctx, workerID, job, req)
}

// startRequirement moves dispatched -> proposing. "proposing" is left
// as-is: it means a previous attempt (crashed worker, re-queued job)
// already started, and generation is idempotent to re-enter.
func (s *Service) startRequirement(ctx context.Context, req *sqlite.GroupRequirementRecord) error {
	if req.State == ReqProposing {
		return nil
	}
	if req.State != ReqDispatched {
		return nil
	}
	_, err := s.transitionState(ctx, req, ReqProposing, map[string]any{})
	return err
}

// generateProposal runs the budget check, the invocation (with one repair
// round-trip for unparseable replies) and the commit.
func (s *Service) generateProposal(ctx context.Context, workerID string, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord) error {
	assignee, err := s.repo.GetMember(ctx, req.GroupID, req.AssigneeMemberID)
	if err != nil || assignee.RemovedAt != nil {
		return s.failAndComplete(ctx, workerID, job, req, FailAssigneeGone, "assignee is no longer a group member")
	}
	info, err := s.models.GetAIModel(ctx, req.AssigneeAIModelID)
	if err != nil || info.Status != "active" {
		return s.failAndComplete(ctx, workerID, job, req, FailAssigneeGone, "assignee AI model is not active")
	}
	g, err := s.repo.GetGroup(ctx, req.GroupID)
	if err != nil {
		return err
	}
	policy := decodePolicy(g.Policy)
	contextLines := s.buildContext(ctx, g, req, policy.ContextMessages)
	var acceptance []string
	if len(req.AcceptanceJSON) > 0 {
		_ = json.Unmarshal(req.AcceptanceJSON, &acceptance)
	}

	priorFeedback := s.priorFeedback(ctx, req)
	var repair *repairContext
	for attempt := 0; attempt < 2; attempt++ {
		msgs := buildProposalMessages(req.Title, req.BodyMD, acceptance, contextLines, repair, priorFeedback)
		estInput := estimateTokens(msgs)
		if err := s.checkBudget(ctx, g, req.ID, info.Model, estInput); err != nil {
			if strings.HasPrefix(err.Error(), FailBudgetExhausted) {
				// Zero additional spend: the check runs before the call.
				return s.failAndComplete(ctx, workerID, job, req, FailBudgetExhausted, err.Error())
			}
			return err
		}
		purpose := PurposePropose
		if attempt > 0 {
			purpose = PurposeProposeRepair
		}
		res, invID, invErr := s.invoke(ctx, job, req, info, msgs, purpose)
		if invErr != nil {
			return s.providerFailure(ctx, workerID, job, req, invErr)
		}
		plan, issues, perr := parsePlan(res.Content)
		if perr != nil {
			if attempt == 0 {
				repair = &repairContext{badReply: truncateRunes(res.Content, 4000), problem: perr.Error()}
				continue
			}
			return s.failAndComplete(ctx, workerID, job, req, FailInvalidPlan,
				"model did not return a valid plan after one repair attempt: "+perr.Error())
		}
		if len(issues) > 0 {
			return s.failAndComplete(ctx, workerID, job, req, FailInvalidPlan, summarizeIssues(issues))
		}
		return s.commitProposal(ctx, workerID, job, req, plan, res, invID)
	}
	return nil // unreachable: the loop always returns
}

// invoke performs one provider call and writes its ledger row either way.
// The call is attributed to the requirement's assignee model.
func (s *Service) invoke(ctx context.Context, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord, info AIModelInfo, msgs []InvokeMessage, purpose string) (InvokeResult, string, error) {
	return s.invokeAs(ctx, job, req, req.AssigneeAIModelID, info, msgs, purpose)
}

// invokeAs is invoke with an explicit calling model — P7 review jobs
// invoke the REVIEWER's model, not the requirement assignee's.
func (s *Service) invokeAs(ctx context.Context, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord, aiModelID string, info AIModelInfo, msgs []InvokeMessage, purpose string) (InvokeResult, string, error) {
	start := s.now()
	res, err := s.provider.Generate(ctx, InvokeRequest{
		AIModelID: aiModelID, Model: info.Model,
		Messages: msgs, JSONMode: true, Temperature: 0.2,
	})
	latency := s.now().Sub(start).Milliseconds()
	inv := &sqlite.GroupModelInvocationRecord{
		ID: newID("inv_"), GroupID: req.GroupID, RequirementID: req.ID, JobID: job.ID,
		AIModelID: aiModelID, Model: info.Model, Purpose: purpose,
		Status: "ok", LatencyMS: latency, CreatedAt: s.now(),
	}
	if err != nil {
		inv.Status = "error"
		inv.Error = truncate(err.Error(), 2000)
	} else {
		inv.InputTokens = res.InputTokens
		inv.OutputTokens = res.OutputTokens
		inv.EstimatedCostMicros = costMicros(info.Model, res.InputTokens, res.OutputTokens)
	}
	if lerr := s.reqRepo.CreateInvocation(ctx, inv); lerr != nil {
		// The ledger is the budget source of truth; failing to write it is
		// an internal error, surfaced instead of the provider result.
		return InvokeResult{}, "", lerr
	}
	if err != nil {
		return InvokeResult{}, inv.ID, err
	}
	return res, inv.ID, nil
}

// providerFailure requeues the job with backoff, or — attempts exhausted —
// fails the requirement as provider_unavailable.
func (s *Service) providerFailure(ctx context.Context, workerID string, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord, cause error) error {
	state, err := s.reqRepo.FailOrRetryJob(ctx, job.ID, workerID, s.now(), jobBackoff(job.Attempts), cause)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil // job was cancelled underneath us; nothing to do
		}
		return err
	}
	if state == "failed" {
		return s.failRequirement(ctx, req, FailProviderError, truncate(cause.Error(), 2000))
	}
	return nil // re-queued; the requirement stays in "proposing"
}

func jobBackoff(attempts int) time.Duration {
	if attempts <= 1 {
		return 5 * time.Second
	}
	return 30 * time.Second
}

// commitProposal writes the proposal, transitions the requirement to
// "proposed", posts the proposal card and emits state_changed — one
// transaction, events published after commit in seq order.
func (s *Service) commitProposal(ctx context.Context, workerID string, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord, plan schema.ExecutionPlan, res InvokeResult, invocationID string) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	rawJSON, _ := json.Marshal(res.Content)
	summary := planSummary(plan)
	now := s.now()
	aborted := false
	var published []Event

	mu := s.lock(req.GroupID)
	mu.Lock()
	err = s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		txReq := sqlite.NewRequirementRepo(tx.DB())
		cur, err := txReq.GetRequirement(ctx, req.ID)
		if err != nil {
			return err
		}
		if cur.State != ReqProposing {
			aborted = true // cancelled while the model was thinking
			return nil
		}
		count, err := txReq.CountProposals(ctx, req.ID)
		if err != nil {
			return err
		}
		prop := &sqlite.RequirementProposalRecord{
			ID: newID("prop_"), RequirementID: req.ID, GroupID: req.GroupID,
			Attempt: int(count) + 1, PlanJSON: planJSON, Summary: summary,
			RawResponseJSON: rawJSON, ModelInvocationID: invocationID,
			CreatedByMemberID: req.AssigneeMemberID, CreatedAt: now,
		}
		if err := txReq.CreateProposal(ctx, prop); err != nil {
			return err
		}
		ok, err := txReq.UpdateRequirementState(ctx, req.ID, []string{ReqProposing}, cur.Version, map[string]any{
			"state": ReqProposed, "current_proposal_id": prop.ID,
			"proposed_at": now, "state_changed_at": now, "updated_at": now,
		})
		if err != nil {
			return err
		}
		if !ok {
			aborted = true
			return nil
		}
		// Proposal card (system message) + its message.created event. The
		// evSeq allocated WITH the message seq is the event's seq — event
		// seqs must stay gapless for SSE replay.
		payload, _ := json.Marshal(map[string]any{
			"requirement_id": req.ID, "proposal_id": prop.ID, "state": ReqProposed,
			"summary": summary, "issues": []string{},
		})
		msgSeq, evSeq, err := tx.NextSeqs(ctx, req.GroupID, true, now)
		if err != nil {
			return err
		}
		card := &sqlite.ChatMessageRecord{
			ID: newID("msg_"), GroupID: req.GroupID, Seq: msgSeq, AuthorType: AuthorSystem,
			Kind: KindProposalCard, BodyMD: fmt.Sprintf("Proposal from @%s: %s", req.AssigneeHandle, summary),
			Payload: payload, RequirementID: req.ID, Status: "final", CreatedAt: now,
		}
		if err := tx.CreateMessage(ctx, card); err != nil {
			return err
		}
		authors, _, err := s.authorMap(ctx, req.GroupID)
		if err != nil {
			return err
		}
		view := messageView(*card, nil, authors)
		evRec, ev, err := buildEvent(req.GroupID, evSeq, "message.created", view, now)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, evRec); err != nil {
			return err
		}
		published = append(published, ev)
		// State event.
		_, evSeq2, err := tx.NextSeqs(ctx, req.GroupID, false, now)
		if err != nil {
			return err
		}
		evRec2, ev2, err := buildEvent(req.GroupID, evSeq2, "requirement.state_changed",
			RequirementEventData{RequirementID: req.ID, State: ReqProposed, Version: cur.Version + 1}, now)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, evRec2); err != nil {
			return err
		}
		published = append(published, ev2)
		return nil
	})
	mu.Unlock()
	if err != nil {
		return err
	}
	for _, ev := range published {
		s.hub.Publish(req.GroupID, ev)
	}
	if !aborted {
		s.record(ctx, req.AssigneeAIModelID, "group.requirement_proposed", req.GroupID, "group_requirement", req.ID,
			map[string]any{"summary": truncate(summary, 200)})
	}
	return s.completeJob(ctx, workerID, job)
}

// failAndComplete fails the requirement (terminal) and completes the job.
func (s *Service) failAndComplete(ctx context.Context, workerID string, job *sqlite.GroupJobRecord, req *sqlite.GroupRequirementRecord, code, detail string) error {
	if err := s.failRequirement(ctx, req, code, detail); err != nil {
		return err
	}
	return s.completeJob(ctx, workerID, job)
}

// failRequirement transitions a requirement to its failure state
// (proposal_failed for invalid plans, failed otherwise), emits
// state_changed and posts a system card. A lost CAS (already terminal or
// cancelled) is not an error.
func (s *Service) failRequirement(ctx context.Context, req *sqlite.GroupRequirementRecord, code, detail string) error {
	toState := ReqFailed
	if code == FailInvalidPlan {
		toState = ReqProposalFailed
	}
	ok, err := s.transitionState(ctx, req, toState, map[string]any{
		"failure_code": code, "failure_detail": truncate(detail, 2000),
	})
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	cardBody := fmt.Sprintf("Requirement failed (%s): %s", code, truncateRunes(detail, 300))
	return s.postSystemCard(ctx, req, KindProposalCard, cardBody, map[string]any{
		"requirement_id": req.ID, "state": toState, "failure_code": code,
	})
}

// transitionState CASes a requirement into toState and appends the
// requirement.state_changed event in the same transaction, publishing
// while holding the group lock (live order = seq order, as in mutate).
func (s *Service) transitionState(ctx context.Context, req *sqlite.GroupRequirementRecord, toState string, fields map[string]any) (bool, error) {
	now := s.now()
	updates := make(map[string]any, len(fields)+3)
	for k, v := range fields {
		updates[k] = v
	}
	updates["state"] = toState
	updates["state_changed_at"] = now
	updates["updated_at"] = now
	if toState == ReqFailed || toState == ReqProposalFailed || toState == ReqCancelled {
		updates["terminal_at"] = now
	}
	mu := s.lock(req.GroupID)
	mu.Lock()
	defer mu.Unlock()
	var ev Event
	ok := false
	err := s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		txReq := sqlite.NewRequirementRepo(tx.DB())
		cur, err := txReq.GetRequirement(ctx, req.ID)
		if err != nil {
			return err
		}
		applied, err := txReq.UpdateRequirementState(ctx, req.ID, []string{cur.State}, cur.Version, updates)
		if err != nil {
			return err
		}
		if !applied {
			return nil
		}
		ok = true
		_, evSeq, err := tx.NextSeqs(ctx, req.GroupID, false, now)
		if err != nil {
			return err
		}
		failureCode, _ := fields["failure_code"].(string)
		evRec, e, err := buildEvent(req.GroupID, evSeq, "requirement.state_changed",
			RequirementEventData{RequirementID: req.ID, State: toState, Version: cur.Version + 1, FailureCode: failureCode}, now)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, evRec); err != nil {
			return err
		}
		ev = e
		return nil
	})
	if err != nil {
		return false, err
	}
	if ok {
		s.hub.Publish(req.GroupID, ev)
	}
	return ok, nil
}

// postSystemCard appends a system card message for a requirement and
// publishes its message.created event (evSeq from the same NextSeqs call
// that allocated the message seq — gapless event seqs).
func (s *Service) postSystemCard(ctx context.Context, req *sqlite.GroupRequirementRecord, kind, body string, payloadMap map[string]any) error {
	now := s.now()
	payload, _ := json.Marshal(payloadMap)
	mu := s.lock(req.GroupID)
	mu.Lock()
	defer mu.Unlock()
	var ev Event
	err := s.repo.Tx(ctx, func(tx *sqlite.GroupRepo) error {
		msgSeq, evSeq, err := tx.NextSeqs(ctx, req.GroupID, true, now)
		if err != nil {
			return err
		}
		card := &sqlite.ChatMessageRecord{
			ID: newID("msg_"), GroupID: req.GroupID, Seq: msgSeq, AuthorType: AuthorSystem,
			Kind: kind, BodyMD: body, Payload: payload, RequirementID: req.ID,
			Status: "final", CreatedAt: now,
		}
		if err := tx.CreateMessage(ctx, card); err != nil {
			return err
		}
		authors, _, err := s.authorMap(ctx, req.GroupID)
		if err != nil {
			return err
		}
		view := messageView(*card, nil, authors)
		evRec, e, err := buildEvent(req.GroupID, evSeq, "message.created", view, now)
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, evRec); err != nil {
			return err
		}
		ev = e
		return nil
	})
	if err != nil {
		return err
	}
	s.hub.Publish(req.GroupID, ev)
	return nil
}

// buildContext renders the most recent text messages (oldest first) as
// "@handle: body" lines, bounded by count and the policy token budget.
func (s *Service) buildContext(ctx context.Context, g *sqlite.ChatGroupRecord, req *sqlite.GroupRequirementRecord, maxMessages int) []string {
	if maxMessages <= 0 {
		maxMessages = 20
	}
	recs, _, err := s.repo.ListMessages(ctx, sqlite.MessageListFilter{GroupID: g.ID, Limit: maxMessages})
	if err != nil {
		return nil
	}
	authors, _, err := s.authorMap(ctx, g.ID)
	if err != nil {
		return nil
	}
	policy := decodePolicy(g.Policy)
	maxChars := 16000
	if policy.ContextTokenBudget > 0 {
		maxChars = policy.ContextTokenBudget * 4 // ~4 chars per token
	}
	lines := make([]string, 0, len(recs))
	used := 0
	for _, rec := range recs {
		if rec.ID == req.AnchorMessageID || rec.Kind != KindText {
			continue
		}
		handle := "system"
		if a, ok := authors[rec.AuthorMemberID]; ok && a.Handle != "" {
			handle = a.Handle
		}
		line := "@" + handle + ": " + truncateRunes(rec.BodyMD, 500)
		used += len(line)
		if used > maxChars {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

func estimateTokens(msgs []InvokeMessage) int64 {
	var chars int
	for _, m := range msgs {
		chars += len(m.Content)
	}
	return int64(chars/4) + 256
}

func (s *Service) completeJob(ctx context.Context, workerID string, job *sqlite.GroupJobRecord) error {
	if err := s.reqRepo.CompleteJob(ctx, job.ID, workerID, s.now()); err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil // cancelled underneath us
		}
		return err
	}
	return nil
}

// claimNext claims the head queued job for workerID. Returns (nil, nil)
// when nothing is claimable right now (empty queue or the head job is
// serial-blocked by a running sibling).
func (s *Service) claimNext(ctx context.Context, workerID string) (*sqlite.GroupJobRecord, error) {
	now := s.now()
	job, err := s.reqRepo.NextQueuedJob(ctx, now)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, nil
		}
		return nil, err
	}
	claimed, err := s.reqRepo.ClaimJob(ctx, job.ID, workerID, now, now.Add(jobLeaseDuration))
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, nil
	}
	job.State = "running"
	job.Attempts++
	return job, nil
}

// requeueExpired returns lapsed-lease jobs to the queue; jobs that
// exhausted their attempts while running fail their requirement as
// provider_unavailable (the worker holding them is gone).
func (s *Service) requeueExpired(ctx context.Context) error {
	rows, err := s.reqRepo.RequeueExpiredLeases(ctx, s.now())
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].State != "failed" {
			continue
		}
		if rows[i].Kind == JobKindReview {
			// P7: an exhausted review job errors its decision so the
			// round can still close; the requirement itself is untouched.
			_ = s.expireReviewJob(ctx, &rows[i])
			continue
		}
		if rows[i].Kind == JobKindRedTeam {
			// P8: an exhausted red-team job errors its check so a
			// manager can retry it; the requirement stays review_passed.
			_ = s.expireRedTeamJob(ctx, &rows[i])
			continue
		}
		req, err := s.reqRepo.GetRequirement(ctx, rows[i].RequirementID)
		if err != nil {
			continue
		}
		if req.State == ReqDispatched || req.State == ReqProposing {
			_ = s.failRequirement(ctx, req, FailProviderError, "worker lease expired; attempts exhausted")
		}
	}
	return nil
}
