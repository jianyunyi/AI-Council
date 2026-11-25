package group

import (
	"context"
	"sync"
	"testing"
)

// TestCancelRetriesAfterLosingRaceToVerdict is the deterministic version of the
// 1-in-8 race from TestLoadCancelRacesDecision, so the fix is pinned even
// without the load suite.
//
// Adapted from Quick's design to the landed code: newP7Env takes no provider
// argument, the env has no gid/rid fields (group + requirement are built with
// mkGroup/propose), and the decision call is SubmitDecision/SubmitDecisionInput
// (not SubmitReviewDecision/ReviewDecisionInput).
func TestCancelRetriesAfterLosingRaceToVerdict(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	bobM := memberID(t, g, "bob")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{bobM}}); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	cancelBeforeCAS = func(rid string) {
		once.Do(func() { // the verdict commits between cancel's read and its CAS
			if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{Verdict: VerdictApprove}); err != nil {
				t.Errorf("verdict: %v", err)
			}
		})
	}
	t.Cleanup(func() { cancelBeforeCAS = nil })

	r, err := e.svc.CancelRequirement(ctx, alice, g.ID, req.ID, "no longer needed")
	if err != nil {
		t.Fatalf("cancel must not report the race: %v", err)
	}
	if r.State != ReqCancelled {
		t.Fatalf("want cancelled, got %s", r.State)
	}
	rv, err := e.svc.GetReview(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rv.Rounds) != 1 || rv.Rounds[0].Outcome != OutcomePassed || len(rv.Rounds[0].Decisions) != 1 {
		t.Fatalf("round closed by the verdict keeps its outcome and reveal: %+v", rv.Rounds[0])
	}
	// Landed contract, kept deliberately: cancelling an already-terminal
	// requirement is a 409, not an idempotent success. Quick's design claimed
	// cancel was "idempotent, as before", but two existing tests pin the 409
	// (review_service_test.go cancel-on-rejected; transport
	// requirement_handlers_test.go "a second cancel conflicts too").
	if _, err := e.svc.CancelRequirement(ctx, alice, g.ID, req.ID, "again"); code(err) != "requirement_state_conflict" {
		t.Fatalf("second cancel must still conflict (landed contract): %v", err)
	}
}
