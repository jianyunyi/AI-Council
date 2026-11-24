package group

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// P8: red-team gate, D1 approval, push-down, mirroring.

func TestRedTeamClearThenApprovePushesDownOnce(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	req := e.passToAwaiting(t, g)
	if req.RedTeam == nil || req.RedTeam.Status != RTClear {
		t.Fatalf("redteam view = %+v, want clear", req.RedTeam)
	}
	if !req.CanApprove {
		t.Fatalf("alice should be able to approve (blocked: %s)", req.ApproveBlockedReason)
	}
	// Bob the OPERATOR approves (D1 allows owner or operator).
	v, err := e.svc.Approve(ctx, bob, g.ID, req.ID, ApproveInput{PlanHash: req.RedTeam.PlanHash, ExpectedVersion: req.Version})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != ReqExecuting {
		t.Fatalf("state = %s, want executing", v.State)
	}
	creates, approvals, executes := e.bridge.counts()
	if creates != 1 || approvals != 1 || executes != 1 {
		t.Fatalf("bridge counts = %d/%d/%d, want 1/1/1", creates, approvals, executes)
	}
	// From executing, push-down retry is not offered.
	if _, err := e.svc.RetryPushdown(ctx, alice, g.ID, req.ID); codeOf(err) != "pushdown_not_retryable" {
		t.Fatalf("retry pushdown from executing: code = %q, want pushdown_not_retryable", codeOf(err))
	}
	// A second approve is impossible: awaiting_approval has exactly one exit.
	if _, err := e.svc.Approve(ctx, alice, g.ID, req.ID, ApproveInput{}); codeOf(err) != "not_approvable" {
		t.Fatalf("second approve: code = %q, want not_approvable", codeOf(err))
	}
	taskID := "task_pd:" + req.ID + ":1"
	e.bridge.finish(taskID, "SUCCEEDED")
	got := e.waitState(t, g.ID, req.ID, ReqExecuted)
	if got.Pushdown == nil || got.Pushdown.Status != PDSucceeded {
		t.Fatalf("pushdown view = %+v, want succeeded", got.Pushdown)
	}
}

func TestConcurrentApprovesCreateOnePushdown(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	req := e.passToAwaiting(t, g)

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, actor := range []Actor{alice, bob} {
		wg.Add(1)
		go func(i int, actor Actor) {
			defer wg.Done()
			<-start
			_, errs[i] = e.svc.Approve(ctx, actor, g.ID, req.ID, ApproveInput{PlanHash: req.RedTeam.PlanHash})
		}(i, actor)
	}
	close(start)
	wg.Wait()
	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("successful approves = %d, want exactly 1 (errs: %v)", okCount, errs)
	}
	creates, _, executes := e.bridge.counts()
	if creates != 1 || executes != 1 {
		t.Fatalf("bridge creates/executes = %d/%d, want 1/1", creates, executes)
	}
}

func TestRedTeamFlaggedSendsBackWithFeedback(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	e.prov.redteamFn = func() (string, error) {
		return `{"blocking":["rm -rf outside workspace"],"non_blocking":[],"recommendation":"do not run"}`, nil
	}
	g := e.mkGroup(t)
	dispatch(t, &p6testEnv{svc: e.svc}, alice, g.ID, "@claude-x plan the migration")
	e.drain(t)
	page, err := e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	req := page.Items[0]
	ai2 := memberID(t, g, "gpt-y")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqChangesRequested {
		t.Fatalf("state = %s, want changes_requested", got.State)
	}
	if got.FailureCode != FailRedTeamFlagged {
		t.Fatalf("failure_code = %q, want %q", got.FailureCode, FailRedTeamFlagged)
	}
	// Revise: the flagged findings must reach the next proposal prompt
	// through the P7 feedback path.
	before := len(e.prov.prompts())
	if _, err := e.svc.ReviseRequirement(ctx, alice, g.ID, req.ID, ReviseInput{}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	prompts := e.prov.prompts()
	if len(prompts) <= before {
		t.Fatal("no new proposal prompt recorded")
	}
	last := prompts[len(prompts)-1]
	if !strings.Contains(last, "rm -rf outside workspace") {
		t.Fatalf("revision prompt lacks the red-team finding:\n%s", last)
	}
}

func TestRedTeamErrorRetryAndIndependence(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	// The only seat is the assignee's own (profile, model): degraded.
	e.seats.seats = []RedTeamSeat{{ProfileID: "prof_a", Model: "claude-x-1", Ordinal: 0}}
	e.prov.redteamFn = func() (string, error) { return "not json", nil }
	g := e.mkGroup(t)
	dispatch(t, &p6testEnv{svc: e.svc}, alice, g.ID, "@claude-x plan the migration")
	e.drain(t)
	page, err := e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	req := page.Items[0]
	ai2 := memberID(t, g, "gpt-y")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqReviewPassed {
		t.Fatalf("state = %s, want review_passed after red-team error", got.State)
	}
	if got.RedTeam == nil || got.RedTeam.Status != RTError {
		t.Fatalf("redteam view = %+v, want error", got.RedTeam)
	}
	if got.RedTeam.Independence != "degraded" {
		t.Fatalf("independence = %q, want degraded", got.RedTeam.Independence)
	}
	// A VIEWER cannot retry (canManage is owner-only here; the Quick
	// draft's premise that an operator is forbidden was wrong — its
	// forbidden case is adapted to a viewer).
	if _, err := e.svc.RetryRedTeam(ctx, vera, g.ID, req.ID); codeOf(err) != "forbidden" {
		t.Fatalf("viewer retry: code = %q, want forbidden", codeOf(err))
	}
	// The owner retries; with a parseable clear reply the gate passes.
	e.prov.redteamFn = nil
	if _, err := e.svc.RetryRedTeam(ctx, alice, g.ID, req.ID); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got, err = e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqAwaitingApproval {
		t.Fatalf("state = %s, want awaiting_approval after retry", got.State)
	}
}

func TestSelectRedTeamSeatsExcludesAssigneeAndReviewers(t *testing.T) {
	all := []RedTeamSeat{
		{ProfileID: "prof_a", Model: "m1"}, // assignee
		{ProfileID: "prof_b", Model: "m2"}, // AI reviewer
		{ProfileID: "prof_c", Model: "m3"}, // independent
	}
	excluded := map[string]bool{"prof_a|m1": true, "prof_b|m2": true}
	kept, ind := selectRedTeamSeats(all, excluded)
	if ind != "full" || len(kept) != 1 || kept[0].ProfileID != "prof_c" {
		t.Fatalf("kept = %+v (%s), want only prof_c/full", kept, ind)
	}
	kept, ind = selectRedTeamSeats(all, map[string]bool{"prof_a|m1": true, "prof_b|m2": true, "prof_c|m3": true})
	if ind != "degraded" || len(kept) != 3 {
		t.Fatalf("all excluded: kept = %d (%s), want 3/degraded", len(kept), ind)
	}
}

func TestApproveRules(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	req := e.passToAwaiting(t, g)

	// The legacy anonymous actor can never approve.
	if _, err := e.svc.Approve(ctx, Actor{UserID: "legacy"}, g.ID, req.ID, ApproveInput{}); codeOf(err) == "" {
		t.Fatal("legacy actor approve must fail with a coded error")
	}
	// A stale plan hash is refused as plan_changed.
	if _, err := e.svc.Approve(ctx, alice, g.ID, req.ID, ApproveInput{PlanHash: "deadbeef"}); codeOf(err) != "plan_changed" {
		t.Fatalf("stale hash: code = %q, want plan_changed", codeOf(err))
	}
	// A viewer member cannot approve.
	if _, err := e.svc.Approve(ctx, vera, g.ID, req.ID, ApproveInput{}); codeOf(err) != "approve_forbidden" {
		t.Fatalf("viewer approve: code = %q, want approve_forbidden", codeOf(err))
	}
	// A non-member gets group_not_found (access hides the group).
	if _, err := e.svc.Approve(ctx, eve, g.ID, req.ID, ApproveInput{}); codeOf(err) != "group_not_found" {
		t.Fatalf("non-member approve: code = %q, want group_not_found", codeOf(err))
	}
}

func TestMirrorSuccessAndFailureCard(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()

	countKind := func(gid, rid, kind string) int {
		page, err := e.svc.ListMessages(ctx, alice, gid, ListMessagesInput{RequirementID: rid, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range page.Items {
			if m.Kind == kind {
				n++
			}
		}
		return n
	}

	// Success path.
	g1 := e.mkGroup(t)
	r1 := e.passToAwaiting(t, g1)
	if _, err := e.svc.Approve(ctx, alice, g1.ID, r1.ID, ApproveInput{}); err != nil {
		t.Fatal(err)
	}
	e.bridge.finish("task_pd:"+r1.ID+":1", "SUCCEEDED")
	e.waitState(t, g1.ID, r1.ID, ReqExecuted)
	if n := countKind(g1.ID, r1.ID, KindTaskStatus); n < 2 {
		t.Fatalf("task_status cards = %d, want >= 2", n)
	}
	if n := countKind(g1.ID, r1.ID, KindExecFailure); n != 0 {
		t.Fatalf("execution_failure cards = %d, want 0 on success", n)
	}

	// Failure path: exactly one failure card.
	g2 := e.mkGroup(t)
	r2 := e.passToAwaiting(t, g2)
	if _, err := e.svc.Approve(ctx, alice, g2.ID, r2.ID, ApproveInput{}); err != nil {
		t.Fatal(err)
	}
	e.bridge.finish("task_pd:"+r2.ID+":1", "FAILED")
	got := e.waitState(t, g2.ID, r2.ID, ReqExecutionFailed)
	if got.FailureCode == "" {
		t.Fatal("execution_failed must carry a failure category")
	}
	if n := countKind(g2.ID, r2.ID, KindExecFailure); n != 1 {
		t.Fatalf("execution_failure cards = %d, want exactly 1", n)
	}
	// Group event seqs stay gapless across all P8 traffic.
	evs, err := e.repo.ListEventsAfter(ctx, g2.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event seq gap at %d: got %d", i, ev.Seq)
		}
	}
}

func TestResumeAfterCrashBetweenSteps(t *testing.T) {
	e := newP8Env(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	req := e.passToAwaiting(t, g)

	// Execute fails transiently during Approve's push: the approval
	// stands, the requirement stays approved, the push is resumable.
	e.bridge.failExecute = errors.New("connection reset by peer")
	v, err := e.svc.Approve(ctx, alice, g.ID, req.ID, ApproveInput{})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != ReqApproved {
		t.Fatalf("state = %s, want approved (push failed transiently)", v.State)
	}
	e.bridge.failExecute = nil
	if err := e.svc.ResumeMirrors(ctx); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, g.ID, req.ID, ReqExecuting)
	creates, approvals, _ := e.bridge.counts()
	if creates != 1 || approvals != 1 {
		t.Fatalf("creates/approvals = %d/%d, want 1/1 (resume reuses the task and approval)", creates, approvals)
	}
}

func TestPushdownDisabledKeepsP7Behavior(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2 := memberID(t, g, "gpt-y")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqReviewPassed {
		t.Fatalf("state = %s, want review_passed (no red-team gate without EnablePushdown)", got.State)
	}
	if got.RedTeam != nil {
		t.Fatalf("redteam view = %+v, want nil when push-down is disabled", got.RedTeam)
	}
	if _, err := e.svc.Approve(ctx, alice, g.ID, req.ID, ApproveInput{}); codeOf(err) != "pushdown_unavailable" {
		t.Fatalf("approve while disabled: code = %q, want pushdown_unavailable", codeOf(err))
	}
}
