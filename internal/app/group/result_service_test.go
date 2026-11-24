package group

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

var errBoom = errors.New("boom")

// p9Provider routes by system prompt: result summary -> summary JSON
// (or summaryFn), result review -> verdict JSON (or resultRevFn),
// proposal review -> verdict JSON (or reviewFn), anything else -> a
// valid plan (and the prompt is recorded). GenerateForProfile serves
// the red-team seats with a clear verdict.
type p9Provider struct {
	mu            sync.Mutex
	reviewFn      func() (string, error)
	resultRevFn   func() (string, error)
	summaryFn     func() (string, error)
	proposalTexts []string
	resultRevSeen []string
}

func (p *p9Provider) Generate(_ context.Context, req InvokeRequest) (InvokeResult, error) {
	var sys, user string
	if len(req.Messages) > 0 {
		sys = req.Messages[0].Content
	}
	if len(req.Messages) > 1 {
		user = req.Messages[1].Content
	}
	respond := func(body string, err error) (InvokeResult, error) {
		if err != nil {
			return InvokeResult{}, err
		}
		return InvokeResult{Content: body, InputTokens: 10, OutputTokens: 5}, nil
	}
	switch sys {
	case resultSummarySystem:
		if p.summaryFn != nil {
			return respond(p.summaryFn())
		}
		return respond(`{"summary_md":"did the thing","acceptance_report":[{"acceptance":"tests pass","status":"met","evidence":"report.md"}]}`, nil)
	case resultReviewSystem:
		p.mu.Lock()
		p.resultRevSeen = append(p.resultRevSeen, user)
		p.mu.Unlock()
		if p.resultRevFn != nil {
			return respond(p.resultRevFn())
		}
		return respond(`{"verdict":"approve","rationale":"result approve"}`, nil)
	case reviewSystemPrompt:
		if p.reviewFn != nil {
			return respond(p.reviewFn())
		}
		return respond(`{"verdict":"approve","rationale":"fine"}`, nil)
	default:
		p.mu.Lock()
		p.proposalTexts = append(p.proposalTexts, user)
		p.mu.Unlock()
		return respond(validPlanJSON, nil)
	}
}

func (p *p9Provider) GenerateForProfile(_ context.Context, profileID, model string, req InvokeRequest) (InvokeResult, error) {
	return InvokeResult{Content: `{"blocking":[],"non_blocking":[],"recommendation":"proceed"}`, InputTokens: 30, OutputTokens: 10}, nil
}

type p9Env struct {
	*p8Env
	prov9 *p9Provider
	arts  *fakeArtifacts
	pds   *store.PushdownRepo
	db    *gorm.DB
}

func newP9Env(t *testing.T) *p9Env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "group-p9-test.sqlite"))
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
		"aim_1": {ID: "aim_1", Handle: "claude-x", DisplayName: "Claude X", Model: "claude-x-1", ProviderName: "anthropic", ProviderProfileID: "prof_a", Status: "active", WorkspaceStatus: "ready"},
		"aim_2": {ID: "aim_2", Handle: "gpt-y", DisplayName: "GPT Y", Model: "gpt-y-1", ProviderName: "openai", ProviderProfileID: "prof_b", Status: "active", WorkspaceStatus: "ready"},
		"aim_3": {ID: "aim_3", Handle: "off-z", DisplayName: "Off Z", Model: "off-z-1", Status: "disabled"},
	}
	users := fakeUsers{
		"u_alice": {ID: "u_alice", Username: "alice", DisplayName: "Alice"},
		"u_bob":   {ID: "u_bob", Username: "bob", DisplayName: "Bob"},
		"u_eve":   {ID: "u_eve", Username: "eve", DisplayName: "Eve"},
		"u_v":     {ID: "u_v", Username: "vera", DisplayName: "Vera"},
	}
	prov := &p9Provider{}
	repo := store.NewGroupRepo(db)
	reqs := store.NewRequirementRepo(db)
	svc := NewService(repo, reqs, ws, models, prov, users, audit, NewHub(64))
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	seats := &fakeSeats{seats: []RedTeamSeat{{ProfileID: "prof_rt", Model: "rt-1", Ordinal: 0}}}
	bridge := newFakeBridge()
	mws := &fakeModelWS{wsID: "ws_model_1", status: "ready"}
	svc.EnablePushdown(seats, bridge, mws)
	arts := newFakeArtifacts()
	svc.EnableResults(arts)
	mctx, cancel := context.WithCancel(context.Background())
	svc.SetMirrorContext(mctx)
	t.Cleanup(cancel)
	return &p9Env{
		p8Env: &p8Env{svc: svc, seats: seats, bridge: bridge, mws: mws, clock: &clock, repo: repo, reqs: reqs},
		prov9: prov, arts: arts, pds: store.NewPushdownRepo(db), db: db,
	}
}

// dispatchReq posts a dispatch message and returns the new
// requirement's id.
func (e *p9Env) dispatchReq(t *testing.T, g *GroupDetail, body string) string {
	t.Helper()
	dispatch(t, &p6testEnv{svc: e.svc}, alice, g.ID, body)
	page, err := e.svc.ListRequirements(context.Background(), alice, g.ID, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Fatal("dispatch created no requirement")
	}
	return page.Items[len(page.Items)-1].ID
}

// passExistingToAwaiting runs an EXISTING requirement's proposal,
// review and red-team stages, ending in awaiting_approval.
func (e *p9Env) passExistingToAwaiting(t *testing.T, g *GroupDetail, rid string) {
	t.Helper()
	ctx := context.Background()
	e.drain(t)
	ai2 := memberID(t, g, "gpt-y")
	if _, err := e.svc.StartReview(ctx, alice, g.ID, rid, StartReviewInput{ReviewerMemberIDs: []string{ai2}}); err != nil {
		t.Fatalf("start review: %v", err)
	}
	e.drain(t)
	v, err := e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != ReqAwaitingApproval {
		t.Fatalf("state = %s, want awaiting_approval", v.State)
	}
}

// execToFinish approves and push-downs an existing requirement,
// finishes the bridge task, and waits for the mirror to land the
// requirement in executed / execution_failed. Returns the task id.
func (e *p9Env) execToFinish(t *testing.T, g *GroupDetail, rid, finishState string) string {
	t.Helper()
	ctx := context.Background()
	e.passExistingToAwaiting(t, g, rid)
	if _, err := e.svc.Approve(ctx, alice, g.ID, rid, ApproveInput{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	pd, err := e.pds.LatestPushdown(ctx, rid)
	if err != nil || pd == nil || pd.TaskID == nil || *pd.TaskID == "" {
		t.Fatalf("pushdown after approve: %+v %v", pd, err)
	}
	e.bridge.finish(*pd.TaskID, finishState)
	want := ReqExecuted
	if finishState == "FAILED" {
		want = ReqExecutionFailed
	}
	e.waitState(t, g.ID, rid, want)
	return *pd.TaskID
}

// collectResult drains the worker so the queued result job runs to
// completion, ending in result_submitted.
func (e *p9Env) collectResult(t *testing.T, g *GroupDetail, rid string) {
	t.Helper()
	e.drain(t)
	e.waitState(t, g.ID, rid, ReqResultSubmitted)
}

func (e *p9Env) seedArtifacts(taskID string) {
	e.arts.add(taskID, ArtifactMeta{Ref: "art-report", Name: "report.md", Kind: "report", MediaType: "text/markdown", WorkspaceID: "ws_model_1"},
		[]byte("# Report\n\ndid the thing"))
	e.arts.add(taskID, ArtifactMeta{Ref: "art-log", Name: "run.log", Kind: "log", MediaType: "text/plain", WorkspaceID: "ws_model_1"},
		[]byte("secret output"))
}

func resultArtifacts(t *testing.T, e *p9Env, rid string) map[string]GroupArtifactView {
	t.Helper()
	ctx := context.Background()
	sub, err := e.svc.results().LatestSubmission(ctx, rid)
	if err != nil || sub == nil {
		t.Fatalf("latest submission: %+v %v", sub, err)
	}
	arts, err := e.svc.results().ListArtifactsForSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]GroupArtifactView{}
	for i := range arts {
		out[arts[i].Name] = artifactView(arts[i])
	}
	return out
}

func TestP9ResultReviewFlow(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x ship it")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	e.collectResult(t, g, rid)

	v, err := e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.Result == nil {
		t.Fatal("requirement view carries no result")
	}
	if v.Result.ExecutionOutcome != ExecSucceeded || v.Result.SummarySource != "model" {
		t.Fatalf("result = %+v", v.Result)
	}
	byName := resultArtifacts(t, e, rid)
	if byName["report.md"].Status != "copied" {
		t.Fatalf("report artifact = %+v, want copied", byName["report.md"])
	}
	if byName["run.log"].Status != "excluded_kind" {
		t.Fatalf("log artifact = %+v, want excluded_kind", byName["run.log"])
	}

	bobM := memberID(t, g, "bob")
	if _, err := e.svc.StartResultReview(ctx, alice, g.ID, rid, StartResultReviewInput{
		ReviewerMemberIDs: []string{bobM},
	}); err != nil {
		t.Fatalf("start result review: %v", err)
	}
	e.waitState(t, g.ID, rid, ReqResultInReview)
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{
		Verdict: VerdictApprove, Rationale: "looks right",
	}); err != nil {
		t.Fatalf("decision: %v", err)
	}
	e.waitState(t, g.ID, rid, ReqCompleted)
	v, err = e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.CompletionOutcome != CompletionAccepted {
		t.Fatalf("completion_outcome = %q, want accepted", v.CompletionOutcome)
	}
	rec, err := e.reqs.GetRequirement(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.TerminalAt == nil {
		t.Fatal("completed requirement must carry terminal_at")
	}
}

func TestFailedExecutionAcceptedAsFailed(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x will fail")
	taskID := e.execToFinish(t, g, rid, "FAILED")
	e.seedArtifacts(taskID)
	e.collectResult(t, g, rid)

	v, err := e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.Result == nil || v.Result.ExecutionOutcome != ExecFailed {
		t.Fatalf("result = %+v, want failed outcome", v.Result)
	}
	// execution_failed is NOT terminal since P9 (C1).
	rec, err := e.reqs.GetRequirement(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.TerminalAt != nil {
		t.Fatal("execution_failed must not set terminal_at")
	}
	bobM := memberID(t, g, "bob")
	if _, err := e.svc.StartResultReview(ctx, alice, g.ID, rid, StartResultReviewInput{
		ReviewerMemberIDs: []string{bobM},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, g.ID, rid, ReqCompleted)
	v, err = e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.CompletionOutcome != CompletionAcceptedFailed {
		t.Fatalf("completion_outcome = %q, want accepted_failed", v.CompletionOutcome)
	}
}

func TestResultRevisionRunsFullPipelineWithNewApproval(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x loop me")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	e.collectResult(t, g, rid)

	bobM := memberID(t, g, "bob")
	if _, err := e.svc.StartResultReview(ctx, alice, g.ID, rid, StartResultReviewInput{
		ReviewerMemberIDs: []string{bobM},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{
		Verdict: VerdictRequestChanges, Rationale: "result incomplete",
	}); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, g.ID, rid, ReqChangesRequested)
	v, err := e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.RevisionSource != RevisionFromResult {
		t.Fatalf("revision_source = %q, want result", v.RevisionSource)
	}
	if _, err := e.svc.ReviseRequirement(ctx, alice, g.ID, rid, ReviseInput{Note: "address review"}); err != nil {
		t.Fatalf("revise: %v", err)
	}
	e.waitState(t, g.ID, rid, ReqDispatched)
	v, err = e.svc.GetRequirement(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if v.Cycle != 2 {
		t.Fatalf("cycle = %d, want 2", v.Cycle)
	}

	// Full pipeline again: proposal (carrying the result feedback),
	// review, red team, and a FRESH human approval (D1). The clock
	// advances so the cycle-2 pushdown is unambiguously the latest.
	*e.clock = e.clock.Add(time.Minute)
	e.passExistingToAwaiting(t, g, rid)
	e.prov9.mu.Lock()
	prompts := append([]string(nil), e.prov9.proposalTexts...)
	e.prov9.mu.Unlock()
	if len(prompts) == 0 {
		t.Fatal("no proposal prompt recorded for cycle 2")
	}
	last := prompts[len(prompts)-1]
	if !strings.Contains(last, "result request_changes") || !strings.Contains(last, "address review") {
		t.Fatalf("cycle-2 proposal prompt lacks result feedback:\n%s", last)
	}
	if _, err := e.svc.Approve(ctx, alice, g.ID, rid, ApproveInput{}); err != nil {
		t.Fatalf("cycle-2 approve: %v", err)
	}
	creates, approvals, executes := e.bridge.counts()
	if creates != 2 || approvals != 2 || executes != 2 {
		t.Fatalf("bridge counts = %d/%d/%d, want 2/2/2 (new task, new approval, new execution)",
			creates, approvals, executes)
	}
	pd, err := e.pds.LatestPushdown(ctx, rid)
	if err != nil || pd == nil || pd.TaskID == nil {
		t.Fatalf("cycle-2 pushdown: %+v %v", pd, err)
	}
	e.bridge.finish(*pd.TaskID, "SUCCEEDED")
	e.waitState(t, g.ID, rid, ReqExecuted)
	e.seedArtifacts(*pd.TaskID)
	e.collectResult(t, g, rid)
	sub, err := e.svc.results().LatestSubmission(ctx, rid)
	if err != nil || sub == nil {
		t.Fatalf("cycle-2 submission: %+v %v", sub, err)
	}
	if sub.Cycle != 2 {
		t.Fatalf("submission cycle = %d, want 2", sub.Cycle)
	}
	if _, err := e.svc.StartResultReview(ctx, alice, g.ID, rid, StartResultReviewInput{
		ReviewerMemberIDs: []string{bobM},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	e.waitState(t, g.ID, rid, ReqCompleted)
}

func TestCycleCap(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x cap me")
	bobM := memberID(t, g, "bob")
	// Default policy: MaxResultRounds = 2 cycles total.
	for cycle := 1; cycle <= 2; cycle++ {
		if cycle > 1 {
			// LatestPushdown orders by approved_at; distinct cycles
			// need distinct timestamps under the frozen test clock.
			*e.clock = e.clock.Add(time.Minute)
		}
		taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
		e.seedArtifacts(taskID)
		e.collectResult(t, g, rid)
		if _, err := e.svc.StartResultReview(ctx, alice, g.ID, rid, StartResultReviewInput{
			ReviewerMemberIDs: []string{bobM},
		}); err != nil {
			t.Fatalf("cycle %d start review: %v", cycle, err)
		}
		if _, err := e.svc.SubmitDecision(ctx, bob, g.ID, rid, SubmitDecisionInput{
			Verdict: VerdictRequestChanges, Rationale: "again",
		}); err != nil {
			t.Fatalf("cycle %d decision: %v", cycle, err)
		}
		e.waitState(t, g.ID, rid, ReqChangesRequested)
		_, err := e.svc.ReviseRequirement(ctx, alice, g.ID, rid, ReviseInput{})
		if cycle == 2 {
			if codeOf(err) != "cycle_limit_reached" {
				t.Fatalf("cycle-2 revise err = %v, want cycle_limit_reached", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("cycle %d revise: %v", cycle, err)
		}
		e.waitState(t, g.ID, rid, ReqDispatched)
	}
	t.Fatal("unreachable")
}

func TestDigestMismatchNotCopied(t *testing.T) {
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x tamper")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	e.arts.add(taskID, ArtifactMeta{Ref: "art-bad", Name: "bad.txt", Kind: "file", MediaType: "text/plain", WorkspaceID: "ws_model_1"},
		[]byte("original bytes"))
	e.arts.corruptRef("art-bad")
	e.collectResult(t, g, rid)

	byName := resultArtifacts(t, e, rid)
	if byName["bad.txt"].Status != "digest_mismatch" {
		t.Fatalf("bad.txt = %+v, want digest_mismatch", byName["bad.txt"])
	}
	e.arts.mu.Lock()
	defer e.arts.mu.Unlock()
	for ref, b := range e.arts.saved {
		if strings.Contains(string(b), "original bytes") {
			t.Fatalf("tampered copy survived under ref %s", ref)
		}
	}
}

func TestManifestIsolationAndDownload404(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x isolate")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	// A FOREIGN task's artifact in the SAME model workspace must
	// never enter this group's manifest.
	e.arts.add("task_FOREIGN", ArtifactMeta{Ref: "art-foreign", Name: "foreign.txt", Kind: "file", WorkspaceID: "ws_model_1"},
		[]byte("not yours"))
	e.collectResult(t, g, rid)

	items, err := e.svc.ListResults(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("submissions = %d, want 1", len(items))
	}
	for _, a := range items[0].Artifacts {
		if a.Name == "foreign.txt" {
			t.Fatal("foreign artifact leaked into the group manifest")
		}
	}
	// Non-member: the group itself is invisible.
	if _, err := e.svc.ListResults(ctx, eve, g.ID, rid); codeOf(err) != "group_not_found" {
		t.Fatalf("non-member list err = %v, want group_not_found", err)
	}
	// Download under the WRONG requirement id -> artifact_not_found.
	report := resultArtifacts(t, e, rid)["report.md"]
	if _, _, err := e.svc.OpenArtifact(ctx, alice, g.ID, "req_nope", report.ID); codeOf(err) != "artifact_not_found" {
		t.Fatalf("wrong-requirement download err = %v, want artifact_not_found", err)
	}
	// The real download works and returns the copied bytes.
	_, rc, err := e.svc.OpenArtifact(ctx, alice, g.ID, rid, report.ID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	b, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "did the thing") {
		t.Fatalf("downloaded bytes = %q", b)
	}
}

func TestResultReviewBlindAndIntentPath(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x blind")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	e.collectResult(t, g, rid)

	// The chat intent path (member @mentions in the body pick the
	// reviewers).
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "@bob please review the result", ClientMsgID: "cm-rr1",
		Intent: "result_review", RequirementID: rid,
	}); err != nil {
		t.Fatalf("result_review intent: %v", err)
	}
	e.waitState(t, g.ID, rid, ReqResultInReview)

	// Blindness: an open result round shows the round and its
	// decision ROWS, but no verdict or rationale.
	st, err := e.svc.GetReview(ctx, alice, g.ID, rid)
	if err != nil {
		t.Fatal(err)
	}
	var open *ReviewRoundView
	for i := range st.Rounds {
		if st.Rounds[i].Status == RoundOpen {
			open = &st.Rounds[i]
		}
	}
	if open == nil {
		t.Fatal("no open round in review state")
	}
	if open.Stage != StageResult {
		t.Fatalf("open round stage = %q, want result", open.Stage)
	}
	if len(open.Decisions) != 1 {
		t.Fatalf("open result round decisions = %d, want 1 (bob)", len(open.Decisions))
	}
	for _, d := range open.Decisions {
		if d.Verdict != "" || d.Rationale != "" {
			t.Fatalf("verdict leaked while result round open: %+v", d)
		}
	}
	// result_review WITHOUT a requirement id -> requirement_required.
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{
		BodyMD: "x", ClientMsgID: "cm-rr2", Intent: "result_review",
	}); codeOf(err) != "requirement_required" {
		t.Fatalf("result_review without id err = %v, want requirement_required", err)
	}
}

func TestP9EventsAndMessagesGapless(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x gapless")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	e.collectResult(t, g, rid)

	page, err := e.svc.ListMessages(ctx, alice, g.ID, ListMessagesInput{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, m := range page.Items {
		if m.Seq != int64(i+1) {
			t.Fatalf("message seq gap at %d: got %d", i, m.Seq)
		}
		if strings.Contains(m.BodyMD, "secret output") {
			t.Fatal("log artifact content leaked into a group message")
		}
		if m.Kind == KindResultCard {
			found = true
			if !strings.Contains(m.BodyMD, "did the thing") {
				t.Fatalf("result card body = %q, want the model summary", m.BodyMD)
			}
		}
	}
	if !found {
		t.Fatal("no result_submission card message")
	}
	evs, err := e.repo.ListEventsAfter(ctx, g.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event seq gap at %d: got %d", i, ev.Seq)
		}
	}
}

func TestP9ResultJobEnqueuedOnPermanentRefusal(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	e.bridge.failExecute = &PermanentError{Category: "workspace_not_ready", Err: errors.New("gone")}
	rid := e.dispatchReq(t, g, "@claude-x refused")
	e.passExistingToAwaiting(t, g, rid)
	if _, err := e.svc.Approve(ctx, alice, g.ID, rid, ApproveInput{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	e.waitState(t, g.ID, rid, ReqExecutionFailed)
	e.collectResult(t, g, rid)
	sub, err := e.svc.results().LatestSubmission(ctx, rid)
	if err != nil || sub == nil {
		t.Fatalf("submission: %+v %v", sub, err)
	}
	if sub.ExecutionOutcome != ExecNotExecuted {
		t.Fatalf("outcome = %q, want not_executed", sub.ExecutionOutcome)
	}
	arts, err := e.svc.results().ListArtifactsForSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 0 {
		t.Fatalf("artifacts = %d, want 0 (the task never ran)", len(arts))
	}
}

// TestPassingRoundIgnoresResultRounds anchors C2: a passed RESULT
// round over the current proposal must never satisfy the proposal
// gate — Approve still refuses with redteam_not_clear when the only
// passed round is a result round.
func TestPassingRoundIgnoresResultRounds(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	rid := e.dispatchReq(t, g, "@claude-x gate")
	e.drain(t) // proposal lands
	req, err := e.reqs.GetRequirement(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	prop, err := e.reqs.GetProposal(ctx, req.CurrentProposalID)
	if err != nil {
		t.Fatal(err)
	}
	now := *e.clock
	// A clear red-team check over the current plan.
	if err := e.pds.CreateCheck(ctx, &store.RedTeamCheckRecord{
		ID: "rtc_gate", GroupID: g.ID, RequirementID: rid, ProposalID: prop.ID,
		ProposalAttempt: prop.Attempt, ReviewRoundID: "rnd_result_gate",
		PlanHash: planHashOf(prop.PlanJSON), Status: RTClear, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// A closed, PASSED result-stage round over the same proposal.
	if err := store.NewReviewRepo(e.db).CreateRound(ctx, &store.ReviewRoundRecord{
		ID: "rnd_result_gate", GroupID: g.ID, RequirementID: rid,
		ProposalID: prop.ID, ProposalRound: prop.Attempt, PlanHash: "result-hash",
		Status: RoundClosed, Outcome: OutcomePassed, PolicyRule: "unanimous",
		Stage: StageResult, SubmissionID: "sub_gate",
		StartedByUserID: alice.UserID, StartedByMember: "gm_x", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reqs.UpdateRequirementState(ctx, rid, []string{req.State}, req.Version, map[string]any{
		"state": ReqAwaitingApproval, "state_changed_at": now, "updated_at": now,
	}); err != nil {
		t.Fatal(err)
	}
	req, err = e.reqs.GetRequirement(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if pr, err := e.svc.passingRound(ctx, req); err != nil || pr != nil {
		t.Fatalf("passingRound = %+v, %v; want nil (result rounds do not count)", pr, err)
	}
	if _, err := e.svc.Approve(ctx, alice, g.ID, rid, ApproveInput{}); codeOf(err) != "redteam_not_clear" {
		t.Fatalf("approve err = %v, want redteam_not_clear", err)
	}
}

// TestP9FactsFallback: when the summary call keeps failing, the
// result job retries and, on the final attempt, commits a facts-only
// summary instead of stranding the requirement.
func TestP9FactsFallback(t *testing.T) {
	ctx := context.Background()
	e := newP9Env(t)
	g := e.mkGroup(t)
	e.prov9.summaryFn = func() (string, error) { return "", errBoom }
	rid := e.dispatchReq(t, g, "@claude-x facts")
	taskID := e.execToFinish(t, g, rid, "SUCCEEDED")
	e.seedArtifacts(taskID)
	// Drain repeatedly, advancing the clock past each retry backoff,
	// until the requirement lands in result_submitted.
	deadline := time.Now().Add(30 * time.Second)
	for {
		e.drain(t)
		rec, err := e.reqs.GetRequirement(ctx, rid)
		if err != nil {
			t.Fatal(err)
		}
		if rec.State == ReqResultSubmitted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("requirement stuck in %s", rec.State)
		}
		*e.clock = e.clock.Add(2 * time.Minute)
	}
	sub, err := e.svc.results().LatestSubmission(ctx, rid)
	if err != nil || sub == nil {
		t.Fatalf("submission: %+v %v", sub, err)
	}
	if sub.SummarySource != "facts" {
		t.Fatalf("summary_source = %q, want facts", sub.SummarySource)
	}
	if !strings.Contains(sub.SummaryMD, "仅列出事实") {
		t.Fatalf("facts summary = %q, want the facts-only marker", sub.SummaryMD)
	}
}
