package group

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// P7 test harness: the P6 env shape (project store over a temp file,
// package fakes from service_test.go) plus a provider fake that routes
// on the system prompt — proposal calls return a valid plan and record
// their user prompt; review calls go through reviewFn.

type p7Provider struct {
	mu              sync.Mutex
	reviewFn        func() (string, error)
	reviewCalls     int
	proposalPrompts []string
}

func (p *p7Provider) Generate(_ context.Context, req InvokeRequest) (InvokeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(req.Messages) > 0 && req.Messages[0].Content == reviewSystemPrompt {
		p.reviewCalls++
		if p.reviewFn != nil {
			content, err := p.reviewFn()
			if err != nil {
				return InvokeResult{}, err
			}
			return InvokeResult{Content: content, InputTokens: 40, OutputTokens: 20}, nil
		}
		return InvokeResult{Content: `{"verdict":"approve","rationale":"looks fine"}`, InputTokens: 40, OutputTokens: 20}, nil
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			p.proposalPrompts = append(p.proposalPrompts, req.Messages[i].Content)
			break
		}
	}
	return InvokeResult{Content: validPlanJSON, InputTokens: 100, OutputTokens: 50}, nil
}

func (p *p7Provider) prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.proposalPrompts...)
}

type p7Env struct {
	svc   *Service
	prov  *p7Provider
	clock *time.Time
	repo  *store.GroupRepo
	reqs  *store.RequirementRepo
	revs  *store.ReviewRepo
}

func newP7Env(t *testing.T) *p7Env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "group-p7-test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	ws := &fakeWS{archived: map[string]bool{}}
	audit := &fakeAudit{}
	models := fakeModels{
		"aim_1": {ID: "aim_1", Handle: "claude-x", DisplayName: "Claude X", Model: "claude-x-1", ProviderName: "anthropic", Status: "active", WorkspaceStatus: "ready"},
		"aim_2": {ID: "aim_2", Handle: "gpt-y", DisplayName: "GPT Y", Model: "gpt-y-1", ProviderName: "openai", Status: "active", WorkspaceStatus: "ready"},
		"aim_3": {ID: "aim_3", Handle: "off-z", DisplayName: "Off Z", Model: "off-z-1", Status: "disabled"},
	}
	users := fakeUsers{
		"u_alice": {ID: "u_alice", Username: "alice", DisplayName: "Alice"},
		"u_bob":   {ID: "u_bob", Username: "bob", DisplayName: "Bob"},
		"u_eve":   {ID: "u_eve", Username: "eve", DisplayName: "Eve"},
		"u_v":     {ID: "u_v", Username: "vera", DisplayName: "Vera"},
	}
	prov := &p7Provider{}
	repo := store.NewGroupRepo(db)
	reqs := store.NewRequirementRepo(db)
	svc := NewService(repo, reqs, ws, models, prov, users, audit, NewHub(64))
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	return &p7Env{svc: svc, prov: prov, clock: &clock, repo: repo, reqs: reqs, revs: store.NewReviewRepo(db)}
}

func (e *p7Env) mkGroup(t *testing.T, policy map[string]any) *GroupDetail {
	t.Helper()
	g, err := e.svc.CreateGroup(context.Background(), alice, CreateGroupInput{
		Name: "Team", Policy: policy,
		Members: []AddMemberInput{
			{MemberType: MemberUser, UserID: "u_bob", GroupRole: RoleOperator},
			{MemberType: MemberUser, UserID: "u_v", GroupRole: RoleViewer},
			{MemberType: MemberAI, AIModelID: "aim_1"},
			{MemberType: MemberAI, AIModelID: "aim_2"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func memberID(t *testing.T, g *GroupDetail, handle string) string {
	t.Helper()
	for _, m := range g.Members {
		if m.Handle == handle {
			return m.ID
		}
	}
	t.Fatalf("member %s not found", handle)
	return ""
}

// drain runs the worker synchronously until no job is claimable.
func (e *p7Env) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		job, err := e.svc.claimNext(ctx, "wtest")
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			return
		}
		if err := e.svc.runJob(ctx, "wtest", job); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("drain did not quiesce")
}

// drainWithRetries drains, advancing the clock past job backoffs, until
// the queue stays empty across a full retry window.
func (e *p7Env) drainWithRetries(t *testing.T) {
	t.Helper()
	for i := 0; i < 12; i++ {
		e.drain(t)
		*e.clock = e.clock.Add(31 * time.Second)
		_ = e.svc.requeueExpired(context.Background())
	}
	e.drain(t)
}

// propose dispatches to @claude-x and drains to a finished proposal.
func (e *p7Env) propose(t *testing.T, gid string) RequirementView {
	t.Helper()
	dispatch(t, &p6testEnv{svc: e.svc}, alice, gid, "@claude-x plan the migration")
	e.drain(t)
	page, err := e.svc.ListRequirements(context.Background(), alice, gid, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("requirements = %d, want 1", len(page.Items))
	}
	req := page.Items[0]
	if req.State != ReqProposed {
		t.Fatalf("state = %s, want proposed (failure: %s %s)", req.State, req.FailureCode, req.FailureDetail)
	}
	return req
}

func TestReviewAllApprovePasses(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2 := memberID(t, g, "gpt-y")

	st, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != ReqInReview {
		t.Fatalf("state = %s, want in_review", st.State)
	}
	e.drain(t)
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqReviewPassed {
		t.Fatalf("state = %s, want review_passed", got.State)
	}
	st, err = e.svc.GetReview(ctx, bob, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rounds) != 1 || st.Rounds[0].Status != RoundClosed || st.Rounds[0].Outcome != OutcomePassed {
		t.Fatalf("rounds = %+v", st.Rounds)
	}
	d := st.Rounds[0].Decisions[0]
	if d.Verdict != VerdictApprove || d.Rationale != "looks fine" {
		t.Fatalf("revealed decision = %+v", d)
	}
	// review_passed is NOT terminal: cancel is still offered, and the
	// allowed actions carry no P8 actions.
	found := map[string]bool{}
	for _, a := range got.AllowedActions {
		found[a] = true
	}
	if !found["cancel"] || found["start_review"] || found["revise"] || found["retry"] {
		t.Fatalf("allowed_actions = %v", got.AllowedActions)
	}
}

func TestHumanReviewerBlindThenReveal(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2, bobM := memberID(t, g, "gpt-y"), memberID(t, g, "bob")
	e.prov.reviewFn = func() (string, error) {
		return `{"verdict":"request_changes","rationale":"missing tests"}`, nil
	}
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2, bobM}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t) // the AI verdict lands; bob is still pending, round open

	st, err := e.svc.GetReview(ctx, bob, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rounds) != 1 || st.Rounds[0].Status != RoundOpen {
		t.Fatalf("round = %+v, want open", st.Rounds)
	}
	for _, d := range st.Rounds[0].Decisions {
		if d.Verdict != "" || d.Rationale != "" {
			t.Fatalf("verdict leaked while round open: %+v", d)
		}
	}
	if !st.CanDecide {
		t.Fatal("bob should be able to decide")
	}
	// Alice is not a reviewer in this round.
	if _, err := e.svc.SubmitDecision(ctx, alice, g.ID, req.ID, SubmitDecisionInput{Verdict: VerdictApprove}); code(err) != "not_a_reviewer" {
		t.Fatalf("alice decision err = %v, want not_a_reviewer", err)
	}
	st, err = e.svc.SubmitDecision(ctx, bob, g.ID, req.ID, SubmitDecisionInput{Verdict: VerdictApprove, Rationale: "fine by me"})
	if err != nil {
		t.Fatal(err)
	}
	// unanimous: one request_changes -> changes_requested, now revealed.
	if st.State != ReqChangesRequested {
		t.Fatalf("state = %s, want changes_requested", st.State)
	}
	if len(st.Rounds[0].Decisions) != 2 {
		t.Fatalf("decisions = %d, want 2 revealed", len(st.Rounds[0].Decisions))
	}
	// A second decision from bob: the round is gone.
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, req.ID, SubmitDecisionInput{Verdict: VerdictApprove}); code(err) != "no_open_review" {
		t.Fatalf("second decision err = %v, want no_open_review", err)
	}
}

func TestReviewViaChatMention(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "@gpt-y please review this", ClientMsgID: "cm-rev-1",
		Intent: "review", RequirementID: req.ID,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqInReview {
		t.Fatalf("state = %s, want in_review", got.State)
	}
	e.drain(t)
	got, _ = e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if got.State != ReqReviewPassed {
		t.Fatalf("state = %s, want review_passed", got.State)
	}
}

func TestReviewGuards(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai1 := memberID(t, g, "claude-x")

	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai1}}); code(err) != "self_review" {
		t.Fatalf("assignee review err = %v, want self_review", err)
	}
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{}); code(err) != "no_reviewer_mention" {
		t.Fatalf("empty reviewers err = %v, want no_reviewer_mention", err)
	}
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{"gm_nope"}}); code(err) != "reviewer_not_member" {
		t.Fatalf("unknown reviewer err = %v, want reviewer_not_member", err)
	}
	// Review intent without a requirement id.
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "@gpt-y review", ClientMsgID: "cm-g1", Intent: "review",
	}); code(err) != "requirement_required" {
		t.Fatalf("review without id err = %v, want requirement_required", err)
	}
	// result_review stays gated until P8.
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "x", ClientMsgID: "cm-g2", Intent: "result_review", RequirementID: req.ID,
	}); code(err) != "intent_not_available" {
		t.Fatalf("result_review err = %v, want intent_not_available", err)
	}
	// Chat messages still may not carry a requirement id (P6 rule).
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "x", ClientMsgID: "cm-g3", RequirementID: req.ID,
	}); code(err) != "requirement_not_available" {
		t.Fatalf("chat with id err = %v, want requirement_not_available", err)
	}
	// A viewer cannot be a human reviewer.
	vera := memberID(t, g, "vera")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{vera}}); code(err) != "reviewer_role" {
		t.Fatalf("viewer reviewer err = %v, want reviewer_role", err)
	}
}

func TestRevisionLoopFeedsFeedbackAndCaps(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2 := memberID(t, g, "gpt-y")
	e.prov.reviewFn = func() (string, error) {
		return `{"verdict":"request_changes","rationale":"missing tests"}`, nil
	}
	// Default policy: MaxProposalRounds = 3.
	for round := 1; round <= 3; round++ {
		if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
			t.Fatalf("round %d start: %v", round, err)
		}
		e.drain(t)
		got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != ReqChangesRequested {
			t.Fatalf("round %d state = %s, want changes_requested", round, got.State)
		}
		_, err = e.svc.ReviseRequirement(ctx, alice, g.ID, req.ID, ReviseInput{Note: "please fix"})
		if round < 3 {
			if err != nil {
				t.Fatalf("round %d revise: %v", round, err)
			}
			e.drain(t) // next proposal
		} else if code(err) != "round_limit_reached" {
			t.Fatalf("round 3 revise err = %v, want round_limit_reached", err)
		}
	}
	prompts := e.prov.prompts()
	if len(prompts) != 3 {
		t.Fatalf("proposal calls = %d, want 3", len(prompts))
	}
	if !strings.Contains(prompts[1], "missing tests") || !strings.Contains(prompts[1], "please fix") {
		t.Fatalf("revision prompt lacks feedback/note:\n%s", prompts[1])
	}
}

func TestProviderDownThenForceCloseIsInconclusive(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2, bobM := memberID(t, g, "gpt-y"), memberID(t, g, "bob")
	e.prov.reviewFn = func() (string, error) { return "", errors.New("connection refused") }
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2, bobM}}); err != nil {
		t.Fatal(err)
	}
	e.drainWithRetries(t) // AI job exhausts its attempts -> decision error
	st, err := e.svc.GetReview(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != ReqInReview {
		t.Fatalf("state = %s, want in_review (bob still pending)", st.State)
	}
	// Vera (viewer) cannot force-close; Eve is not a member at all.
	vera := Actor{UserID: "u_v"}
	if _, err := e.svc.ForceCloseReview(ctx, vera, g.ID, req.ID); code(err) != "review_forbidden" {
		t.Fatalf("vera force-close err = %v, want review_forbidden", err)
	}
	if _, err := e.svc.ForceCloseReview(ctx, eve, g.ID, req.ID); code(err) != "group_not_found" {
		t.Fatalf("eve force-close err = %v, want group_not_found", err)
	}
	st, err = e.svc.ForceCloseReview(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Nobody decided -> inconclusive -> back to proposed.
	if st.State != ReqProposed || st.Rounds[0].Outcome != OutcomeInconclusive {
		t.Fatalf("state/outcome = %s/%s, want proposed/inconclusive", st.State, st.Rounds[0].Outcome)
	}
}

func TestQuorumPassesDespiteErroredReviewer(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, map[string]any{"proposal_review_rule": "quorum", "proposal_quorum": 1})
	req := e.propose(t, g.ID)
	ai2, bobM := memberID(t, g, "gpt-y"), memberID(t, g, "bob")
	e.prov.reviewFn = func() (string, error) { return "", errors.New("down") }
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2, bobM}}); err != nil {
		t.Fatal(err)
	}
	e.drainWithRetries(t) // AI decision errors out
	st, err := e.svc.SubmitDecision(ctx, bob, g.ID, req.ID, SubmitDecisionInput{Verdict: VerdictApprove})
	if err != nil {
		t.Fatal(err)
	}
	// quorum=1: bob's approve reaches quorum; the errored AI decision
	// never counts as approval but does not block the pass either.
	if st.State != ReqReviewPassed || st.Rounds[0].Outcome != OutcomePassed {
		t.Fatalf("state/outcome = %s/%s, want review_passed/passed", st.State, st.Rounds[0].Outcome)
	}
}

func TestReviewRejectIsTerminal(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2 := memberID(t, g, "gpt-y")
	e.prov.reviewFn = func() (string, error) {
		return `{"verdict":"reject","rationale":"unsafe plan"}`, nil
	}
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	got, err := e.svc.GetRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReqRejected || got.FailureCode != FailReviewRejected || got.TerminalAt == nil {
		t.Fatalf("state/code/terminal = %s/%s/%v, want rejected/review_rejected/set", got.State, got.FailureCode, got.TerminalAt)
	}
	if _, err := e.svc.CancelRequirement(ctx, alice, g.ID, req.ID, "too late"); code(err) != "requirement_state_conflict" {
		t.Fatalf("cancel rejected err = %v, want requirement_state_conflict", err)
	}
}

func TestCancelDuringReviewSupersedes(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	bobM := memberID(t, g, "bob")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{bobM}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelRequirement(ctx, alice, g.ID, req.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.GetReview(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rounds) != 1 || st.Rounds[0].Outcome != OutcomeSuperseded {
		t.Fatalf("rounds = %+v, want one superseded round", st.Rounds)
	}
	if st.Rounds[0].Decisions[0].Verdict != "" {
		t.Fatal("superseded round must never reveal verdicts")
	}
	if st.CanDecide {
		t.Fatal("can_decide must be false after supersede")
	}
}

func TestP7EventsGaplessAndBlind(t *testing.T) {
	e := newP7Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	req := e.propose(t, g.ID)
	ai2, bobM := memberID(t, g, "gpt-y"), memberID(t, g, "bob")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, req.ID, StartReviewInput{ReviewerMemberIDs: []string{ai2, bobM}}); err != nil {
		t.Fatal(err)
	}
	e.drain(t)
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, req.ID, SubmitDecisionInput{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	events, err := e.repo.ListEventsAfter(ctx, g.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want gapless %d", i, ev.Seq, i+1)
		}
		if ev.Type == "review.decision_recorded" {
			var payload map[string]any
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if _, leaked := payload["verdict"]; leaked {
				t.Fatalf("decision_recorded event leaks a verdict: %s", ev.Data)
			}
			if _, leaked := payload["rationale"]; leaked {
				t.Fatalf("decision_recorded event leaks a rationale: %s", ev.Data)
			}
		}
	}
}
