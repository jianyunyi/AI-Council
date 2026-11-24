package group

import (
	"context"
	"log"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// Part B P8: the task mirror. One goroutine per executing pushdown
// watches the council task and projects its lifecycle into the group
// stream as sanitized task_status cards — phase/state/stage, the
// adapter's bounded summary and, on failure, a category. Nothing else
// crosses: no logs, no command output, no diffs, no file contents (the
// only content source is TaskStatus, which is itself bounded).
//
// The mirror is resumable: state lives on the pushdown record
// (LastMirroredState CAS), so a restart re-attaches with ResumeMirrors
// and a re-attached mirror never double-posts a card.

func (s *Service) mirrorCtx() context.Context {
	if s.mirrors != nil && s.mirrors.root != nil {
		return s.mirrors.root
	}
	return context.Background()
}

func (s *Service) startMirror(gid, rid, pdID, taskID string) {
	if s.mirrors == nil || !s.mirrors.start(pdID) {
		return
	}
	go func() {
		defer s.mirrors.done(pdID)
		s.mirrorLoop(s.mirrorCtx(), gid, rid, pdID, taskID)
	}()
}

func (s *Service) mirrorLoop(ctx context.Context, gid, rid, pdID, taskID string) {
	// Reconcile first: the task may already be final (fast run, or a
	// restart after the run finished while we were down).
	if st, err := s.tasks.TaskStatus(ctx, taskID); err == nil {
		if s.mirrorApply(ctx, gid, rid, pdID, st) {
			return
		}
	}
	ch, err := s.tasks.WatchTask(ctx, taskID)
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case st, ok := <-ch:
			if !ok {
				if st2, err := s.tasks.TaskStatus(ctx, taskID); err == nil {
					s.mirrorApply(ctx, gid, rid, pdID, st2)
				}
				return
			}
			if s.mirrorApply(ctx, gid, rid, pdID, st) {
				return
			}
		}
	}
}

// mirrorApply posts at most one card per distinct state/stage and, on
// the final phase, finishes the pushdown and moves the requirement
// (executing -> executed | execution_failed). It returns true when the
// pushdown is final and the mirror can stop.
func (s *Service) mirrorApply(ctx context.Context, gid, rid, pdID string, st TaskStatus) bool {
	key := st.State + "/" + st.Stage
	rec, err := s.pd().GetPushdownByTask(ctx, st.TaskID)
	if err != nil || rec.ID != pdID {
		return true
	}
	switch rec.Status {
	case PDSucceeded, PDFailed, PDCancelled:
		return true
	}
	final := st.Phase == "succeeded" || st.Phase == "failed" || st.Phase == "cancelled"
	if rec.LastMirroredState == key && !final {
		return false
	}
	req, err := s.reqRepo.GetRequirement(ctx, rid)
	if err != nil {
		return true
	}
	err = s.reviewCommit(ctx, gid, func(tx *sqlite.GroupRepo, at time.Time) ([]Event, error) {
		won, err := sqlite.NewPushdownRepo(tx.DB()).AdvanceMirror(ctx, pdID, rec.LastMirroredState, key, at)
		if err != nil || !won {
			return nil, err // a twin mirror won the CAS; it posts the card
		}
		var evs []Event
		body := "执行状态：" + phaseLabelZH(st.Phase)
		if st.Stage != "" {
			body += "（" + st.Stage + "）"
		}
		if st.Summary != "" {
			body += " — " + truncateRunes(st.Summary, 300)
		}
		ev, err := s.p8SystemMessage(ctx, tx, gid, rid, KindTaskStatus, body, map[string]any{
			"requirement_id": rid, "task_id": st.TaskID, "phase": st.Phase,
			"state": st.State, "stage": st.Stage, "summary": truncateRunes(st.Summary, 300)}, at)
		if err != nil {
			return nil, err
		}
		evs = append(evs, ev)
		ev, err = rvAppendEvent(ctx, tx, gid, "task.mirrored", map[string]any{
			"requirement_id": rid, "task_id": st.TaskID, "phase": st.Phase,
			"state": st.State, "stage": st.Stage}, at)
		if err != nil {
			return nil, err
		}
		evs = append(evs, ev)
		if !final {
			return evs, nil
		}
		pdStatus, code := "", ""
		switch st.Phase {
		case "succeeded":
			pdStatus = PDSucceeded
		case "failed":
			pdStatus, code = PDFailed, st.FailureCategory
			if code == "" {
				code = "runner_error"
			}
		case "cancelled":
			pdStatus, code = PDCancelled, "cancelled"
		}
		if err := sqlite.NewPushdownRepo(tx.DB()).UpdatePushdown(ctx, rec.ID, map[string]any{
			"status": pdStatus, "failure_category": code, "finished_at": at}, at); err != nil {
			return nil, err
		}
		if st.Phase == "cancelled" {
			// The requirement's own state is owned by whoever cancelled
			// it (CancelRequirement); never post a failure card here —
			// the Quick draft did, and a user-cancelled requirement got
			// a spurious "execution failed" card.
			return evs, nil
		}
		toState := ReqExecuted
		extra := map[string]any{}
		if st.Phase == "failed" {
			toState, extra["failure_code"] = ReqExecutionFailed, code
		}
		ev, won, err = s.p8TransitionTx(ctx, tx, req, []string{ReqExecuting}, toState, extra, at)
		if err != nil {
			return nil, err
		}
		if won {
			evs = append(evs, ev)
			if st.Phase == "failed" {
				ev, err = s.p8SystemMessage(ctx, tx, gid, rid, KindExecFailure,
					"执行失败："+failureLabelZH(code),
					map[string]any{"requirement_id": rid, "retryable": false, "category": code}, at)
				if err != nil {
					return nil, err
				}
				evs = append(evs, ev)
			}
		}
		return evs, nil
	})
	if err != nil {
		log.Printf("group: mirror %s: %v", pdID, err)
		return false
	}
	return final
}

func phaseLabelZH(phase string) string {
	switch phase {
	case "pending":
		return "等待执行"
	case "running":
		return "执行中"
	case "succeeded":
		return "执行完成"
	case "failed":
		return "执行失败"
	case "cancelled":
		return "已取消"
	default:
		return phase
	}
}

// ResumeMirrors re-attaches to interrupted pushdowns at startup.
// Records that never reached execution re-run the resumable pushDown
// (as the original approver); records mid-execution just get a mirror.
func (s *Service) ResumeMirrors(ctx context.Context) error {
	if !s.pushdownEnabled() {
		return nil
	}
	recs, err := s.pd().ListPushdownsByStatus(ctx, []string{PDApproved, PDTaskCreated, PDTaskApproved, PDExecuting})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		rec := rec
		if rec.Status == PDExecuting {
			if rec.TaskID != nil && *rec.TaskID != "" {
				s.startMirror(rec.GroupID, rec.RequirementID, rec.ID, *rec.TaskID)
			}
			continue
		}
		go func() {
			if err := s.pushDown(s.mirrorCtx(), rec.ApprovedBy, rec.GroupID, rec.RequirementID); err != nil {
				log.Printf("group: resume pushdown %s: %v", rec.ID, err)
			}
		}()
	}
	return nil
}

// cancelPushdown best-effort-cancels the underlying task when a
// requirement is cancelled from approved/executing. The landed council
// engine refuses to cancel a task that is actively EXECUTING (the
// runner call is synchronous with no cancel RPC), so a refusal is
// logged and the mirror keeps reporting the task's real outcome — the
// requirement itself is already cancelled and ignores it.
func (s *Service) cancelPushdown(ctx context.Context, actorID, rid string) {
	if !s.pushdownEnabled() {
		return
	}
	rec, err := s.pd().LatestPushdown(ctx, rid)
	if err != nil || rec == nil || rec.TaskID == nil || *rec.TaskID == "" {
		return
	}
	if err := s.tasks.CancelTask(ctx, *rec.TaskID, actorID); err != nil {
		log.Printf("group: cancel pushdown task %s: %v", *rec.TaskID, err)
	}
}
