package group

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// P6 test harness: same shape as newEnv (project store over a temp file —
// no in-memory DSN dependency), plus the requirement repo and a fake
// ProviderInvoker wired in, and the *gorm.DB kept for ledger assertions.

type fakeProvider struct {
	mu    sync.Mutex
	calls int
	fn    func(call int, req InvokeRequest) (InvokeResult, error)
}

func (f *fakeProvider) Generate(_ context.Context, req InvokeRequest) (InvokeResult, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(n, req)
	}
	return InvokeResult{Content: validPlanJSON, InputTokens: 100, OutputTokens: 50}, nil
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

const validPlanJSON = `{"version":1,"commands":[{"executable":"echo","args":["hi"],"timeout_seconds":60,"purpose":"demo"}]}`

type p6testEnv struct {
	svc   *Service
	prov  *fakeProvider
	clock *time.Time
	repo  *store.GroupRepo
	reqs  *store.RequirementRepo
}

func newP6Env(t *testing.T) *p6testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "group-p6-test.sqlite"))
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
	prov := &fakeProvider{}
	repo := store.NewGroupRepo(db)
	reqs := store.NewRequirementRepo(db)
	svc := NewService(repo, reqs, ws, models, prov, users, audit, NewHub(64))
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	return &p6testEnv{svc: svc, prov: prov, clock: &clock, repo: repo, reqs: reqs}
}

func (e *p6testEnv) mkGroup(t *testing.T, budget map[string]any) *GroupDetail {
	t.Helper()
	g, err := e.svc.CreateGroup(context.Background(), alice, CreateGroupInput{
		Name: "Team", Budget: budget,
		Members: []AddMemberInput{
			{MemberType: MemberUser, UserID: "u_bob", GroupRole: RoleOperator},
			{MemberType: MemberAI, AIModelID: "aim_1"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// drain runs the worker synchronously until no job is claimable.
func (e *p6testEnv) drain(t *testing.T) {
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
		if err := e.svc.runProposalJob(ctx, "wtest", job); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("drain did not quiesce")
}

func (e *p6testEnv) onlyRequirement(t *testing.T, gid string) RequirementView {
	t.Helper()
	page, err := e.svc.ListRequirements(context.Background(), alice, gid, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("requirements = %d, want 1", len(page.Items))
	}
	return page.Items[0]
}

var dispatchSeq int64

func dispatch(t *testing.T, e *p6testEnv, actor Actor, gid, body string) *PostMessageResult {
	t.Helper()
	n := atomic.AddInt64(&dispatchSeq, 1)
	res, err := e.svc.PostMessage(context.Background(), actor, gid, PostMessageInput{
		BodyMD: body, ClientMsgID: "cm-dispatch-" + strconv.FormatInt(n, 10), Intent: "dispatch",
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDispatchCreatesRequirementCardAndProposal(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)

	res := dispatch(t, e, alice, g.ID, "@claude-x please plan the migration")
	req := e.onlyRequirement(t, g.ID)
	if req.State != ReqDispatched {
		t.Fatalf("state = %s, want dispatched", req.State)
	}
	if req.AssigneeAIModelID != "aim_1" || req.AssigneeHandle != "claude-x" {
		t.Fatalf("assignee = %s/%s", req.AssigneeAIModelID, req.AssigneeHandle)
	}
	if req.AnchorMessageID != res.Message.ID {
		t.Fatalf("anchor = %s, want %s", req.AnchorMessageID, res.Message.ID)
	}
	if req.Job == nil || req.Job.State != "queued" {
		t.Fatalf("job = %+v, want queued", req.Job)
	}
	// The dispatch card follows the anchor in the stream.
	msgs, err := e.svc.ListMessages(ctx, alice, g.ID, ListMessagesInput{RequirementID: req.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	foundCard := false
	for _, m := range msgs.Items {
		if m.Kind == KindDispatchCard && m.RequirementID == req.ID {
			foundCard = true
		}
	}
	if !foundCard {
		t.Fatalf("no dispatch card in stream: %+v", msgs.Items)
	}

	e.drain(t)
	req = e.onlyRequirement(t, g.ID)
	if req.State != ReqProposed {
		t.Fatalf("state = %s, want proposed (failure: %s %s)", req.State, req.FailureCode, req.FailureDetail)
	}
	if req.Proposal == nil || len(req.Proposal.Plan.Commands) != 1 {
		t.Fatalf("proposal = %+v", req.Proposal)
	}
	if e.prov.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", e.prov.callCount())
	}
}

func TestDispatchFanOutTwoAIs(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	if _, err := e.svc.AddMember(ctx, alice, g.ID, AddMemberInput{MemberType: MemberAI, AIModelID: "aim_2"}); err != nil {
		t.Fatal(err)
	}
	dispatch(t, e, alice, g.ID, "@claude-x and @gpt-y both plan this")
	page, err := e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("requirements = %d, want 2 (fan-out)", len(page.Items))
	}
	e.drain(t)
	page, _ = e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	for _, r := range page.Items {
		if r.State != ReqProposed {
			t.Fatalf("requirement %s state = %s, want proposed", r.ID, r.State)
		}
	}
}

func TestDispatchRequiresOneAIMention(t *testing.T) {
	e := newP6Env(t)
	g := e.mkGroup(t, nil)
	for _, body := range []string{"please plan this, no mentions", "@bob please plan this"} {
		_, err := e.svc.PostMessage(context.Background(), alice, g.ID, PostMessageInput{
			BodyMD: body, ClientMsgID: "cm-x-" + body[:4], Intent: "dispatch"})
		if code(err) != "no_ai_mention" {
			t.Fatalf("body %q: err = %v, want no_ai_mention", body, err)
		}
	}
}

func TestDispatchForbiddenForViewer(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	if _, err := e.svc.AddMember(ctx, alice, g.ID, AddMemberInput{MemberType: MemberUser, UserID: "u_v", GroupRole: RoleViewer}); err != nil {
		t.Fatal(err)
	}
	vera := Actor{UserID: "u_v"}
	if _, err := e.svc.PostMessage(ctx, vera, g.ID, PostMessageInput{BodyMD: "hello", ClientMsgID: "cm-v1"}); err != nil {
		t.Fatalf("viewer chat: %v", err)
	}
	_, err := e.svc.PostMessage(ctx, vera, g.ID, PostMessageInput{
		BodyMD: "@claude-x plan this", ClientMsgID: "cm-v2", Intent: "dispatch"})
	if code(err) != "dispatch_forbidden" {
		t.Fatalf("viewer dispatch err = %v, want dispatch_forbidden", err)
	}
	_, err = e.svc.PostMessage(ctx, eve, g.ID, PostMessageInput{
		BodyMD: "@claude-x plan this", ClientMsgID: "cm-e1", Intent: "dispatch"})
	if code(err) != "group_not_found" {
		t.Fatalf("non-member dispatch err = %v, want group_not_found", err)
	}
}

func TestProposalInvalidJSONRepairedOnce(t *testing.T) {
	e := newP6Env(t)
	g := e.mkGroup(t, nil)
	e.prov.fn = func(call int, req InvokeRequest) (InvokeResult, error) {
		if call == 1 {
			return InvokeResult{Content: "sure! here is the plan: ...", InputTokens: 10, OutputTokens: 5}, nil
		}
		return InvokeResult{Content: validPlanJSON, InputTokens: 10, OutputTokens: 5}, nil
	}
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	e.drain(t)
	req := e.onlyRequirement(t, g.ID)
	if req.State != ReqProposed {
		t.Fatalf("state = %s, want proposed after repair", req.State)
	}
	if e.prov.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial + one repair)", e.prov.callCount())
	}
	spend, err := e.reqs.GroupSpend(context.Background(), g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if spend.Invocations != 2 {
		t.Fatalf("ledger invocations = %d, want 2", spend.Invocations)
	}
}

func TestProposalInvalidPlanFailsWithoutRepair(t *testing.T) {
	e := newP6Env(t)
	g := e.mkGroup(t, nil)
	e.prov.fn = func(call int, req InvokeRequest) (InvokeResult, error) {
		return InvokeResult{Content: `{"version":1,"patches":[],"commands":[]}`, InputTokens: 10, OutputTokens: 5}, nil
	}
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	e.drain(t)
	req := e.onlyRequirement(t, g.ID)
	if req.State != ReqProposalFailed || req.FailureCode != FailInvalidPlan {
		t.Fatalf("state/code = %s/%s, want proposal_failed/invalid_plan", req.State, req.FailureCode)
	}
	if !strings.Contains(req.FailureDetail, "empty_plan") {
		t.Fatalf("detail = %q, want empty_plan issue", req.FailureDetail)
	}
	if e.prov.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no repair for parseable-but-invalid plans)", e.prov.callCount())
	}
}

func TestProposalProviderDownRetriesThenFails(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	e.prov.fn = func(call int, req InvokeRequest) (InvokeResult, error) {
		return InvokeResult{}, errors.New("connection refused")
	}
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	for i := 0; i < 12; i++ {
		e.drain(t)
		req := e.onlyRequirement(t, g.ID)
		if req.State == ReqFailed {
			if req.FailureCode != FailProviderError {
				t.Fatalf("failure code = %s, want provider_unavailable", req.FailureCode)
			}
			if e.prov.callCount() != 3 {
				t.Fatalf("provider calls = %d, want 3 (max attempts)", e.prov.callCount())
			}
			return
		}
		*e.clock = e.clock.Add(31 * time.Second) // outrun the retry backoff
		_ = e.svc.requeueExpired(ctx)
	}
	t.Fatal("requirement never failed after retries")
}

func TestDispatchOverBudgetFailsWithZeroCalls(t *testing.T) {
	e := newP6Env(t)
	g := e.mkGroup(t, map[string]any{"token_cap": 10})
	dispatch(t, e, alice, g.ID, "@claude-x plan the whole migration in detail")
	e.drain(t)
	req := e.onlyRequirement(t, g.ID)
	if req.State != ReqFailed || req.FailureCode != FailBudgetExhausted {
		t.Fatalf("state/code = %s/%s, want failed/budget_exhausted", req.State, req.FailureCode)
	}
	if e.prov.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0 (pre-flight must run first)", e.prov.callCount())
	}
}

func TestCancelThenRetryRules(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	req := e.onlyRequirement(t, g.ID)

	cancelled, err := e.svc.CancelRequirement(ctx, alice, g.ID, req.ID, "no longer needed")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != ReqCancelled {
		t.Fatalf("state = %s, want cancelled", cancelled.State)
	}
	if _, err := e.svc.RetryRequirement(ctx, alice, g.ID, req.ID); code(err) != "requirement_state_conflict" {
		t.Fatalf("retry cancelled err = %v, want requirement_state_conflict", err)
	}
	// Bob did not request it and cannot manage: forbidden.
	dispatch(t, e, alice, g.ID, "@claude-x plan another thing")
	page, _ := e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	var other *RequirementView
	for i := range page.Items {
		if page.Items[i].ID != req.ID {
			other = &page.Items[i]
		}
	}
	if _, err := e.svc.CancelRequirement(ctx, bob, g.ID, other.ID, "not mine"); code(err) != "forbidden" {
		t.Fatalf("bob cancel err = %v, want forbidden", err)
	}
}

func TestRetryFailedRequirementReproposes(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	e.prov.fn = func(call int, req InvokeRequest) (InvokeResult, error) {
		if call == 1 {
			return InvokeResult{Content: `{"version":1,"patches":[],"commands":[]}`}, nil
		}
		return InvokeResult{Content: validPlanJSON}, nil
	}
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	e.drain(t)
	req := e.onlyRequirement(t, g.ID)
	if req.State != ReqProposalFailed {
		t.Fatalf("state = %s, want proposal_failed", req.State)
	}
	retried, err := e.svc.RetryRequirement(ctx, alice, g.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != ReqDispatched {
		t.Fatalf("state after retry = %s, want dispatched", retried.State)
	}
	e.drain(t)
	req = e.onlyRequirement(t, g.ID)
	if req.State != ReqProposed {
		t.Fatalf("state = %s, want proposed", req.State)
	}
}

func TestJobSerializationPerAssignee(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	dispatch(t, e, alice, g.ID, "@claude-x first task")
	dispatch(t, e, alice, g.ID, "@claude-x second task")

	job1, err := e.svc.claimNext(ctx, "w1")
	if err != nil || job1 == nil {
		t.Fatalf("claim1 = %v, %v", job1, err)
	}
	job2, err := e.svc.claimNext(ctx, "w2")
	if err != nil {
		t.Fatal(err)
	}
	if job2 != nil {
		t.Fatalf("second claim = %+v, want nil (same assignee serializes)", job2)
	}
	if err := e.svc.completeJob(ctx, "w1", job1); err != nil {
		t.Fatal(err)
	}
	job3, err := e.svc.claimNext(ctx, "w2")
	if err != nil || job3 == nil {
		t.Fatalf("claim after completion = %v, %v", job3, err)
	}
}

func TestP6EventsGapless(t *testing.T) {
	e := newP6Env(t)
	ctx := context.Background()
	g := e.mkGroup(t, nil)
	dispatch(t, e, alice, g.ID, "@claude-x plan it")
	e.drain(t)
	events, err := e.repo.ListEventsAfter(ctx, g.ID, 0, 500)
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
	}
}
