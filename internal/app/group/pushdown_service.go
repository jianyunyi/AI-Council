package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P8: human approval + push-down (decision D1, locked: human
// approval stays). Approve is the ONLY exit from awaiting_approval
// (canAct never lists approve; canApprove gates on an active human
// owner/operator member — AI members can never approve). The approval
// re-verifies the plan four ways — the hash the client sent, the
// passing round's, the clear red-team check's and the live proposal
// bytes — then creates the pushdown record and runs the push in the
// same request. Push-down failures are left retryable (state approved,
// record present): RetryPushdown / ResumeMirrors pick them up. The
// idempotency key pd:<rid>:<attempt> plus the task's external_key make
// a double create impossible.

// PushdownEventData is the payload of pushdown.created / pushdown.failed
// events. Never carries task internals.
type PushdownEventData struct {
	RequirementID string `json:"requirement_id"`
	PushdownID    string `json:"pushdown_id"`
	TaskID        string `json:"task_id,omitempty"`
}

// Approve is the D1 gate and the push-down trigger.
func (s *Service) Approve(ctx context.Context, a Actor, gid, rid string, in ApproveInput) (*RequirementView, error) {
	if !s.pushdownEnabled() {
		return nil, errPushdownDisabled
	}
	g, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if g.Status != StatusActive {
		return nil, errArchived
	}
	if !canApprove(a, m) {
		return nil, errApproveForbidden
	}
	req, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if req.State != ReqAwaitingApproval {
		return nil, errNotApprovable
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != req.Version {
		return nil, errVersionConflict
	}
	prop, err := s.reqRepo.GetProposal(ctx, req.CurrentProposalID)
	if err != nil {
		return nil, err
	}
	round, err := s.passingRound(ctx, req)
	if err != nil {
		return nil, err
	}
	if round == nil {
		return nil, errRedTeamNotClear
	}
	check, err := s.pd().LatestCheck(ctx, rid, prop.Attempt)
	if err != nil {
		return nil, err
	}
	if check == nil || check.Status != RTClear {
		return nil, errRedTeamNotClear
	}
	planHash := planHashOf(prop.PlanJSON)
	if planHash != round.PlanHash || planHash != check.PlanHash {
		return nil, errPlanMismatch
	}
	if in.PlanHash != "" && in.PlanHash != planHash {
		return nil, errPlanMismatch
	}
	assignee, err := s.repo.GetMember(ctx, gid, req.AssigneeMemberID)
	if err != nil {
		return nil, err
	}
	wsID, status, err := s.modelWS.ModelWorkspace(ctx, assignee.AIModelID)
	if err != nil {
		return nil, err
	}
	if wsID == "" || status != "ready" {
		return nil, errWorkspaceNotReady
	}
	pd := &sqlite.RequirementPushdownRecord{
		ID: newID("pd_"), GroupID: gid, RequirementID: rid, ProposalID: prop.ID,
		ProposalAttempt: prop.Attempt, ReviewRoundID: round.ID, RedTeamCheckID: check.ID,
		IdempotencyKey: fmt.Sprintf("pd:%s:%d", rid, prop.Attempt),
		PlanHash:       planHash, ApprovedBy: a.UserID, ModelWorkspaceID: wsID, Status: PDApproved,
		ApprovedAt: s.now(), UpdatedAt: s.now(),
	}
	approved := false
	err = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		ev, won, err := s.p8TransitionTx(ctx, tx, req, []string{ReqAwaitingApproval}, ReqApproved, nil, at)
		if err != nil {
			return nil, err
		}
		if !won {
			return nil, errVersionConflict // a concurrent approve won
		}
		approved = true
		if err := sqlite.NewPushdownRepo(tx.DB()).CreatePushdown(ctx, pd); err != nil {
			return nil, err // unique (requirement, attempt) is the backstop
		}
		handle := s.memberHandle(ctx, gid, m.ID)
		ev2, err := s.p8SystemMessage(ctx, tx, gid, rid, KindApprovalNotice,
			"已批准，开始下推执行。批准人："+handle,
			map[string]any{"requirement_id": rid, "approved_by": a.UserID, "plan_hash": planHash}, at)
		if err != nil {
			return nil, err
		}
		return []Event{ev, ev2}, nil
	})
	if err != nil {
		return nil, err
	}
	if !approved {
		return nil, errVersionConflict
	}
	s.record(ctx, a.UserID, "group.requirement_approved", gid, "group_requirement", rid,
		map[string]any{"plan_hash": planHash, "workspace_id": wsID})
	// Push down in the same request. A failure here is not an approval
	// failure: the requirement stays approved and the push is retryable.
	if err := s.pushDown(ctx, a.UserID, gid, rid); err != nil {
		log.Printf("group: pushdown %s: %v", rid, err)
	}
	return s.GetRequirement(ctx, a, gid, rid)
}

// RetryPushdown resumes a push that stopped at a retryable step.
func (s *Service) RetryPushdown(ctx context.Context, a Actor, gid, rid string) (*RequirementView, error) {
	if !s.pushdownEnabled() {
		return nil, errPushdownDisabled
	}
	_, m, err := s.access(ctx, a, gid)
	if err != nil {
		return nil, err
	}
	if !canApprove(a, m) {
		return nil, errApproveForbidden
	}
	req, err := s.reqRepo.GetRequirementInGroup(ctx, gid, rid)
	if err != nil {
		if errors.Is(err, sqlite.ErrRequirementNotFound) {
			return nil, errRequirementMissing
		}
		return nil, err
	}
	if req.State != ReqApproved {
		return nil, errNotRetryablePD
	}
	s.record(ctx, a.UserID, "group.requirement_pushdown_retried", gid, "group_requirement", rid, nil)
	if err := s.pushDown(ctx, a.UserID, gid, rid); err != nil {
		log.Printf("group: pushdown retry %s: %v", rid, err)
	}
	return s.GetRequirement(ctx, a, gid, rid)
}

// pushDown is resumable: every step re-reads the record and skips what
// is already done, so it is safe to call from Approve, RetryPushdown
// and the startup resume.
func (s *Service) pushDown(ctx context.Context, actorID, gid, rid string) error {
	req, err := s.reqRepo.GetRequirement(ctx, rid)
	if err != nil {
		return err
	}
	if req.State != ReqApproved && req.State != ReqExecuting {
		return nil // cancelled or already final
	}
	prop, err := s.reqRepo.GetProposal(ctx, req.CurrentProposalID)
	if err != nil {
		return err
	}
	rec, err := s.pd().GetPushdownForAttempt(ctx, rid, prop.Attempt)
	if err != nil {
		return err
	}
	switch rec.Status {
	case PDSucceeded, PDFailed, PDCancelled:
		return nil
	}
	if planHashOf(prop.PlanJSON) != rec.PlanHash {
		return s.pdFailPermanent(ctx, req, rec, "plan_changed", errPlanMismatch)
	}
	step := func(fields map[string]any) {
		_ = s.pd().UpdatePushdown(ctx, rec.ID, fields, s.now())
	}
	step(map[string]any{"attempts": rec.Attempts + 1})

	taskID := ""
	if rec.TaskID != nil {
		taskID = *rec.TaskID
	}
	if taskID == "" {
		var acc []string
		if len(req.AcceptanceJSON) > 0 {
			_ = json.Unmarshal(req.AcceptanceJSON, &acc)
		}
		taskID, err = s.tasks.CreatePushdownTask(ctx, PushdownTaskInput{
			IdempotencyKey: rec.IdempotencyKey, WorkspaceID: rec.ModelWorkspaceID,
			OwnerID: rec.ApprovedBy, Requirement: req.BodyMD, Acceptance: acc,
			PlanJSON: prop.PlanJSON, GroupRequirementID: rid,
		})
		if err != nil {
			return s.pdStepFailed(ctx, req, rec, "create_task", err)
		}
		step(map[string]any{"task_id": taskID, "status": PDTaskCreated, "last_error": ""})
	}
	st, err := s.tasks.TaskStatus(ctx, taskID)
	if err != nil {
		return s.pdStepFailed(ctx, req, rec, "task_status", err)
	}
	if st.PlanHash != rec.PlanHash {
		return s.pdFailPermanent(ctx, req, rec, "plan_changed", errPlanMismatch)
	}
	switch {
	case st.Phase == "pending":
		if rec.ApprovalID == nil || *rec.ApprovalID == "" {
			apID, aerr := s.tasks.ApproveTask(ctx, taskID, rec.ApprovedBy)
			if aerr != nil {
				return s.pdStepFailed(ctx, req, rec, "approve_task", aerr)
			}
			step(map[string]any{"approval_id": apID, "status": PDTaskApproved, "last_error": ""})
		}
		if xerr := s.tasks.ExecuteTask(ctx, taskID, actorID); xerr != nil {
			return s.pdStepFailed(ctx, req, rec, "execute_task", xerr)
		}
	case st.Phase == "running" && rec.Status != PDExecuting:
		// A previous execute attempt was interrupted mid-flight (the
		// task is EXECUTING in the council engine): resume it through
		// the core. ExecuteTask surfaces a task that actually FAILED as
		// a permanent error.
		if xerr := s.tasks.ExecuteTask(ctx, taskID, actorID); xerr != nil {
			return s.pdStepFailed(ctx, req, rec, "execute_task", xerr)
		}
	}
	if err := s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		if err := sqlite.NewPushdownRepo(tx.DB()).UpdatePushdown(ctx, rec.ID,
			map[string]any{"status": PDExecuting, "last_error": ""}, at); err != nil {
			return nil, err
		}
		var evs []Event
		ev, won, err := s.p8TransitionTx(ctx, tx, req, []string{ReqApproved}, ReqExecuting, nil, at)
		if err != nil {
			return nil, err
		}
		if won {
			evs = append(evs, ev)
		}
		ev, err = rvAppendEvent(ctx, tx, gid, "pushdown.created",
			PushdownEventData{RequirementID: rid, PushdownID: rec.ID, TaskID: taskID}, at)
		if err != nil {
			return nil, err
		}
		return append(evs, ev), nil
	}); err != nil {
		return err
	}
	s.record(ctx, actorID, "group.requirement_pushed_down", gid, "group_requirement", rid,
		map[string]any{"task_id": taskID, "workspace_id": rec.ModelWorkspaceID})
	s.startMirror(gid, rid, rec.ID, taskID)
	return nil
}

func (s *Service) pdStepFailed(ctx context.Context, req *sqlite.GroupRequirementRecord, rec *sqlite.RequirementPushdownRecord, step string, err error) error {
	if cat, ok := permanentCategory(err); ok {
		code := cat
		if code == "" {
			code = "pushdown_refused_" + step
		}
		return s.pdFailPermanent(ctx, req, rec, code, err)
	}
	_ = s.pd().UpdatePushdown(ctx, rec.ID, map[string]any{
		"last_error": truncate(step+": "+err.Error(), 2000)}, s.now())
	s.p8Notice(ctx, req.GroupID, req.ID, KindExecFailure,
		fmt.Sprintf("下推未完成（%s），可重试。", step),
		map[string]any{"requirement_id": req.ID, "retryable": true, "step": step})
	return err
}

// pdFailPermanent: the push is dead. The requirement moves to
// execution_failed with a non-retryable failure card.
func (s *Service) pdFailPermanent(ctx context.Context, req *sqlite.GroupRequirementRecord, rec *sqlite.RequirementPushdownRecord, code string, cause error) error {
	err := s.reviewCommit(ctx, req.GroupID, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		if err := sqlite.NewPushdownRepo(tx.DB()).UpdatePushdown(ctx, rec.ID, map[string]any{
			"status": PDFailed, "failure_category": code, "last_error": truncate(cause.Error(), 2000),
			"finished_at": at}, at); err != nil {
			return nil, err
		}
		var evs []Event
		ev, won, err := s.p8TransitionTx(ctx, tx, req, []string{ReqApproved, ReqExecuting}, ReqExecutionFailed,
			map[string]any{"failure_code": code}, at)
		if err != nil {
			return nil, err
		}
		if won {
			evs = append(evs, ev)
			ev, err = s.p8SystemMessage(ctx, tx, req.GroupID, req.ID, KindExecFailure,
				"执行被拒绝："+failureLabelZH(code),
				map[string]any{"requirement_id": req.ID, "retryable": false, "category": code}, at)
			if err != nil {
				return nil, err
			}
			evs = append(evs, ev)
			// P9 (§2.8a): even a refused push-down gets a result
			// submission (outcome not_executed when no task ever
			// existed) — enqueued in this same transaction.
			rsEv, err := s.rsEnqueueTx(ctx, tx, req, rec, PDFailed, code, true, at)
			if err != nil {
				return nil, err
			}
			if rsEv.Seq != 0 {
				evs = append(evs, rsEv)
			}
		}
		ev, err = rvAppendEvent(ctx, tx, req.GroupID, "pushdown.failed",
			PushdownEventData{RequirementID: req.ID, PushdownID: rec.ID}, at)
		if err != nil {
			return nil, err
		}
		return append(evs, ev), nil
	})
	if err != nil {
		return err
	}
	s.record(ctx, "system", "group.requirement_pushdown_failed", req.GroupID, "group_requirement", req.ID,
		map[string]any{"category": code})
	return cause
}

// p8Notice posts a system card outside any transaction (best-effort).
func (s *Service) p8Notice(ctx context.Context, gid, rid, kind, body string, payload map[string]any) {
	_ = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		ev, err := s.p8SystemMessage(ctx, tx, gid, rid, kind, body, payload, at)
		if err != nil {
			return nil, err
		}
		return []Event{ev}, nil
	})
}

func failureLabelZH(code string) string {
	switch code {
	case "plan_changed":
		return "计划已变化，需要重新评审"
	case "verification_failed":
		return "验证未通过"
	case "timeout":
		return "执行超时"
	case "cancelled":
		return "已取消"
	case "runner_error":
		return "执行器错误"
	default:
		return code
	}
}

func redTeamView(c *sqlite.RedTeamCheckRecord) *RedTeamView {
	v := &RedTeamView{
		CheckID: c.ID, Status: c.Status, Independence: c.Independence,
		SeatModel: c.SeatModel, PlanHash: c.PlanHash, FinishedAt: c.FinishedAt,
	}
	if c.FinishedAt != nil { // summary only once the check has run
		var report schema.RedTeamReport
		_ = json.Unmarshal(c.Report, &report)
		var issues []council.PlanIssue
		_ = json.Unmarshal(c.Issues, &issues)
		v.Summary = rtBody(c.Status, c.Independence, &report, issues, c.ErrorMsg)
	}
	return v
}

func pushdownView(rec *sqlite.RequirementPushdownRecord) *PushdownView {
	v := &PushdownView{
		ID: rec.ID, Status: rec.Status, WorkspaceID: rec.ModelWorkspaceID,
		PlanHash: rec.PlanHash, ApprovedBy: rec.ApprovedBy, ApprovedAt: rec.ApprovedAt,
		LastError: rec.LastError, FailureCategory: rec.FailureCategory,
		FinishedAt: rec.FinishedAt,
	}
	if rec.TaskID != nil {
		v.TaskID = *rec.TaskID
	}
	if rec.LastMirroredState != "" {
		state := rec.LastMirroredState
		for i := 0; i < len(state); i++ {
			if state[i] == '/' {
				state = state[:i]
				break
			}
		}
		v.LastPhase = PhaseOf(state)
	}
	return v
}

// approvalFlags evaluates the D1 rule for the requesting actor and
// explains a disabled approve button. Reason order: role, state,
// red-team status, plan freshness, workspace readiness.
func (s *Service) approvalFlags(ctx context.Context, a Actor, me *sqlite.ChatGroupMemberRecord, rec *sqlite.GroupRequirementRecord, v *RequirementView) (bool, string) {
	if !s.pushdownEnabled() {
		return false, ""
	}
	if !canApprove(a, me) {
		return false, "not_owner_or_operator"
	}
	if rec.State != ReqAwaitingApproval {
		return false, "state_" + rec.State
	}
	if v.RedTeam != nil && v.RedTeam.Status != RTClear {
		return false, "redteam_" + v.RedTeam.Status
	}
	if v.RedTeam == nil {
		return false, "redteam_missing"
	}
	if rec.CurrentProposalID != "" {
		if p, err := s.reqRepo.GetProposal(ctx, rec.CurrentProposalID); err == nil {
			if planHashOf(p.PlanJSON) != v.RedTeam.PlanHash {
				return false, "plan_changed"
			}
		}
	}
	if _, status, err := s.modelWS.ModelWorkspace(ctx, rec.AssigneeAIModelID); err == nil && status != "ready" {
		return false, "workspace_not_ready"
	}
	return true, ""
}
