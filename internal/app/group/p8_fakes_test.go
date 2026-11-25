package group

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// P8 test harness: the P7 env shape plus the three P8 ports faked —
// red-team seats, the council task bridge, and the model-workspace
// lookup — and a provider that also implements GenerateForProfile
// (red-team seats invoke by profile, not by AI model).

func codeOf(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	return ""
}

var vera = Actor{UserID: "u_v"}

type p8Provider struct {
	mu              sync.Mutex
	reviewFn        func() (string, error)
	redteamFn       func() (string, error)
	redteamCalls    int
	proposalPrompts []string
}

func (p *p8Provider) Generate(_ context.Context, req InvokeRequest) (InvokeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(req.Messages) > 0 && req.Messages[0].Content == reviewSystemPrompt {
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

func (p *p8Provider) GenerateForProfile(_ context.Context, profileID, model string, req InvokeRequest) (InvokeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.redteamCalls++
	if p.redteamFn != nil {
		content, err := p.redteamFn()
		if err != nil {
			return InvokeResult{}, err
		}
		return InvokeResult{Content: content, InputTokens: 30, OutputTokens: 10}, nil
	}
	return InvokeResult{Content: `{"blocking":[],"non_blocking":[],"recommendation":"proceed"}`, InputTokens: 30, OutputTokens: 10}, nil
}

func (p *p8Provider) prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.proposalPrompts...)
}

type fakeSeats struct {
	seats []RedTeamSeat
	err   error
}

func (f *fakeSeats) RedTeamSeats(context.Context, string) ([]RedTeamSeat, error) {
	return f.seats, f.err
}

type fakeTask struct {
	state    string
	planHash string
}

type fakeBridge struct {
	mu         sync.Mutex
	tasks      map[string]*fakeTask
	byKey      map[string]string
	watchers   map[string][]chan TaskStatus
	creates    int
	approvals  int
	executes   int
	cancels    int
	failExecute error
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{tasks: map[string]*fakeTask{}, byKey: map[string]string{}, watchers: map[string][]chan TaskStatus{}}
}

func (b *fakeBridge) CreatePushdownTask(_ context.Context, in PushdownTaskInput) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, ok := b.byKey[in.IdempotencyKey]; ok {
		return id, nil // idempotent on the external key
	}
	b.creates++
	id := "task_" + in.IdempotencyKey
	b.byKey[in.IdempotencyKey] = id
	b.tasks[id] = &fakeTask{state: "AWAITING_APPROVAL", planHash: PlanHashOf(in.PlanJSON)}
	return id, nil
}

func (b *fakeBridge) ApproveTask(_ context.Context, taskID, approverID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.approvals++
	return "appr_" + taskID, nil
}

func (b *fakeBridge) ExecuteTask(_ context.Context, taskID, actorID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.executes++
	if b.failExecute != nil {
		return b.failExecute
	}
	if t, ok := b.tasks[taskID]; ok {
		t.state = "EXECUTING"
	}
	return nil
}

func (b *fakeBridge) CancelTask(_ context.Context, taskID, actorID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancels++
	if t, ok := b.tasks[taskID]; ok {
		t.state = "CANCELLED"
	}
	return nil
}

func (b *fakeBridge) statusLocked(taskID string) (TaskStatus, bool) {
	t, ok := b.tasks[taskID]
	if !ok {
		return TaskStatus{}, false
	}
	return TaskStatus{TaskID: taskID, State: t.state, Stage: t.state, Phase: PhaseOf(t.state), PlanHash: t.planHash}, true
}

func (b *fakeBridge) TaskStatus(_ context.Context, taskID string) (TaskStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.statusLocked(taskID)
	if !ok {
		return TaskStatus{}, errors.New("task not found")
	}
	return st, nil
}

func (b *fakeBridge) WatchTask(_ context.Context, taskID string) (<-chan TaskStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan TaskStatus, 4)
	// Deliver the current status immediately, like the real polling
	// bridge does on its first tick — a task that finished before the
	// watcher attached must not be missed.
	if st, ok := b.statusLocked(taskID); ok {
		ch <- st
		if st.Phase == "succeeded" || st.Phase == "failed" || st.Phase == "cancelled" {
			close(ch)
			return ch, nil
		}
	}
	b.watchers[taskID] = append(b.watchers[taskID], ch)
	return ch, nil
}

// finish moves a task to a final state and notifies watchers.
func (b *fakeBridge) finish(taskID, state string) {
	b.mu.Lock()
	if t, ok := b.tasks[taskID]; ok {
		t.state = state
	}
	st, _ := b.statusLocked(taskID)
	ws := b.watchers[taskID]
	delete(b.watchers, taskID)
	b.mu.Unlock()
	for _, ch := range ws {
		ch <- st
		close(ch)
	}
}

func (b *fakeBridge) counts() (creates, approvals, executes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creates, b.approvals, b.executes
}

type fakeModelWS struct {
	wsID   string
	status string
}

func (f *fakeModelWS) ModelWorkspace(context.Context, string) (string, string, error) {
	return f.wsID, f.status, nil
}

type p8Env struct {
	svc    *Service
	prov   *p8Provider
	seats  *fakeSeats
	bridge *fakeBridge
	mws    *fakeModelWS
	clock  *time.Time
	repo   *store.GroupRepo
	reqs   *store.RequirementRepo
}

func newP8Env(t *testing.T) *p8Env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "group-p8-test.sqlite"))
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
	prov := &p8Provider{}
	repo := store.NewGroupRepo(db)
	reqs := store.NewRequirementRepo(db)
	svc := NewService(repo, reqs, ws, models, prov, users, audit, NewHub(64))
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	seats := &fakeSeats{seats: []RedTeamSeat{{ProfileID: "prof_rt", Model: "rt-1", Ordinal: 0}}}
	bridge := newFakeBridge()
	mws := &fakeModelWS{wsID: "ws_model_1", status: "ready"}
	svc.EnablePushdown(seats, bridge, mws)
	mctx, cancel := context.WithCancel(context.Background())
	svc.SetMirrorContext(mctx)
	t.Cleanup(cancel)
	return &p8Env{svc: svc, prov: prov, seats: seats, bridge: bridge, mws: mws, clock: &clock, repo: repo, reqs: reqs}
}

func (e *p8Env) mkGroup(t *testing.T) *GroupDetail {
	t.Helper()
	g, err := e.svc.CreateGroup(context.Background(), alice, CreateGroupInput{
		Name: "Team",
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

func (e *p8Env) drain(t *testing.T) {
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

// passToAwaiting dispatches to @claude-x, passes review with @gpt-y
// and drains through the red-team job, ending in awaiting_approval
// (with the default clear red-team reply).
func (e *p8Env) passToAwaiting(t *testing.T, g *GroupDetail) RequirementView {
	t.Helper()
	ctx := context.Background()
	dispatch(t, &p6testEnv{svc: e.svc}, alice, g.ID, "@claude-x plan the migration")
	e.drain(t)
	page, err := e.svc.ListRequirements(ctx, alice, g.ID, ListRequirementsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("requirements = %d, want 1", len(page.Items))
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
	if got.State != ReqAwaitingApproval {
		t.Fatalf("state = %s, want awaiting_approval", got.State)
	}
	return *got
}

func (e *p8Env) waitState(t *testing.T, gid, rid, want string) RequirementView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, err := e.svc.GetRequirement(context.Background(), alice, gid, rid)
		if err != nil {
			t.Fatal(err)
		}
		if v.State == want {
			return *v
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := e.svc.GetRequirement(context.Background(), alice, gid, rid)
	t.Fatalf("state = %s, want %s (timeout)", v.State, want)
	return *v
}
