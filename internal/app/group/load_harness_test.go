package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

// P10.1 load harness. Adapted from the Quick draft to the landed shapes:
//   - InvokeResult carries Content string + InputTokens/OutputTokens.
//   - Proposal replies must be a bare ExecutionPlan JSON (parsePlan decodes
//     the first JSON object directly); validPlanJSON is the landed fixture.
//   - Red team invokes by profile via GenerateForProfile, never Generate.
//   - There is no NewWorker/drainOne in the landed code: ldWorker composes
//     svc.claimNext + svc.runJob, the same primitives ProposalWorker.Run
//     and the P8 test drain use.
//   - Job states are "queued"/"running" (no "leased").

// ldScale gates and sizes the load suite. -short skips it; AICOUNCIL_LOAD_SCALE multiplies it.
func ldScale(t *testing.T) int {
	t.Helper()
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}
	n, _ := strconv.Atoi(os.Getenv("AICOUNCIL_LOAD_SCALE"))
	if n < 1 {
		n = 1
	}
	return n
}

const (
	ldApproveJSON = `{"verdict":"approve","rationale":"ok"}`
	ldRedTeamJSON = `{"blocking":[],"non_blocking":[],"recommendation":"proceed"}`
	ldSummaryJSON = `{"summary_md":"done","acceptance_report":[]}`
)

// ldProvider is a deterministic invoker. For proposal calls it tracks
// in-flight calls per serial key (group:assignee) to prove the queue's
// concurrency-1 rule, and calls per requirement marker to prove no job
// runs twice.
type ldProvider struct {
	mu            sync.Mutex
	delay         time.Duration
	inflight      map[string]int
	maxInflight   map[string]int
	proposalCalls map[string]int
	total         int
}

func newLDProvider(delay time.Duration) *ldProvider {
	return &ldProvider{delay: delay, inflight: map[string]int{}, maxInflight: map[string]int{}, proposalCalls: map[string]int{}}
}

// ldMarker extracts "[ld:g<idx>:<handle>:r<id>]" (first occurrence = the requirement title).
func ldMarker(s string) string {
	i := strings.Index(s, "[ld:")
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], "]")
	if j < 0 {
		return ""
	}
	return s[i : i+j+1]
}

func ldSerial(marker string) string {
	parts := strings.Split(strings.Trim(marker, "[]"), ":")
	if len(parts) < 4 {
		return marker
	}
	return parts[1] + ":" + parts[2]
}

func ldSystemOf(in InvokeRequest) string {
	for _, m := range in.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

func (p *ldProvider) Generate(ctx context.Context, in InvokeRequest) (InvokeResult, error) {
	system := ldSystemOf(in)
	var user string
	for _, m := range in.Messages {
		if m.Role != "system" {
			user += m.Content
		}
	}
	var reply string
	switch system {
	case proposalSystemPrompt:
		mk := ldMarker(user)
		key := ldSerial(mk)
		p.mu.Lock()
		p.inflight[key]++
		if p.inflight[key] > p.maxInflight[key] {
			p.maxInflight[key] = p.inflight[key]
		}
		p.proposalCalls[mk]++
		p.total++
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			p.inflight[key]--
			p.mu.Unlock()
		}()
		if p.delay > 0 {
			select {
			case <-time.After(p.delay):
			case <-ctx.Done():
				return InvokeResult{}, ctx.Err()
			}
		}
		reply = validPlanJSON
	case reviewSystemPrompt, resultReviewSystem:
		reply = ldApproveJSON
	case redTeamSystemPrompt:
		// Red team normally arrives via GenerateForProfile; answer here
		// too so a routing change fails noisily elsewhere, not silently.
		reply = ldRedTeamJSON
	case resultSummarySystem:
		reply = ldSummaryJSON
	default:
		return InvokeResult{}, errors.New("ldProvider: unexpected system prompt (WIRING L2)")
	}
	return InvokeResult{Content: reply, InputTokens: 10, OutputTokens: 5}, nil
}

// GenerateForProfile serves red-team seats (the only profile invocations
// in Part B).
func (p *ldProvider) GenerateForProfile(_ context.Context, _, _ string, in InvokeRequest) (InvokeResult, error) {
	if ldSystemOf(in) != redTeamSystemPrompt {
		return InvokeResult{}, errors.New("ldProvider: unexpected profile invocation (WIRING L2)")
	}
	return InvokeResult{Content: ldRedTeamJSON, InputTokens: 10, OutputTokens: 5}, nil
}

type ldGroup struct {
	idx       int
	id        string
	owner     Actor
	ops       []Actor
	aiHandles []string
	member    map[string]string // AI handle or user id -> member id
}

type ldEnv struct {
	svc    *Service
	prov   *ldProvider
	bridge *fakeBridge
	arts   *fakeArtifacts
	groups []*ldGroup
}

type ldOpts struct {
	groups, ais, aiPool, ops, hubBuffer int
	delay                               time.Duration
	pushdown                            bool
}

// ldUser is the only place that builds a UserInfo (WIRING L1: the landed
// UserInfo does carry ID/Username/DisplayName, as the draft assumed).
func ldUser(users fakeUsers, id, username string) Actor {
	users[id] = UserInfo{ID: id, Username: username, DisplayName: username}
	return Actor{UserID: id, CanCreateGroup: true}
}

// newLoadEnv builds `groups` groups. Each gets `ais` AI members drawn round-robin from a pool of
// `aiPool` models, so groups SHARE models (and therefore model workspaces), plus `ops` operators.
func newLoadEnv(t *testing.T, o ldOpts) *ldEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "load.db"))
	if err != nil {
		t.Fatal(err)
	}
	models := fakeModels{}
	for i := 0; i < o.aiPool; i++ {
		id := fmt.Sprintf("aim_ld_%d", i)
		models[id] = AIModelInfo{ID: id, Handle: fmt.Sprintf("ld-ai-%d", i), DisplayName: fmt.Sprintf("LD %d", i),
			Status: "active", WorkspaceStatus: "ready", ProviderProfileID: "pp", Model: fmt.Sprintf("m-%d", i)}
	}
	users := fakeUsers{}
	prov := newLDProvider(o.delay)
	svc := NewService(store.NewGroupRepo(db), store.NewRequirementRepo(db), &fakeWS{archived: map[string]bool{}},
		models, prov, users, &fakeAudit{}, NewHub(o.hubBuffer))
	e := &ldEnv{svc: svc, prov: prov}
	if o.pushdown {
		e.bridge, e.arts = newFakeBridge(), newFakeArtifacts()
		svc.EnablePushdown(&fakeSeats{seats: []RedTeamSeat{{ProfileID: "pp", Model: "m-redteam", Ordinal: 0}}}, e.bridge, &fakeModelWS{wsID: "ws_model_ld", status: "ready"})
		svc.EnableResults(e.arts)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel) // runs before TempDir removal (LIFO)
		svc.SetMirrorContext(ctx)
	}
	ctx := context.Background()
	for g := 0; g < o.groups; g++ {
		lg := &ldGroup{idx: g, member: map[string]string{}}
		lg.owner = ldUser(users, fmt.Sprintf("u_ld_%d_owner", g), fmt.Sprintf("ldg%downer", g))
		var members []AddMemberInput
		for k := 0; k < o.ops; k++ {
			op := ldUser(users, fmt.Sprintf("u_ld_%d_op%d", g, k), fmt.Sprintf("ldg%dop%d", g, k))
			lg.ops = append(lg.ops, op)
			members = append(members, AddMemberInput{MemberType: MemberUser, UserID: op.UserID, GroupRole: RoleOperator})
		}
		for k := 0; k < o.ais; k++ {
			id := fmt.Sprintf("aim_ld_%d", (g+k)%o.aiPool)
			members = append(members, AddMemberInput{MemberType: MemberAI, AIModelID: id})
			lg.aiHandles = append(lg.aiHandles, models[id].Handle)
		}
		gd, err := svc.CreateGroup(ctx, lg.owner, CreateGroupInput{Name: fmt.Sprintf("load-%d", g), Members: members})
		if err != nil {
			t.Fatalf("create group %d: %v", g, err)
		}
		lg.id = gd.ID
		for _, m := range gd.Members {
			if m.MemberType == MemberAI {
				lg.member[m.Handle] = m.ID
			} else {
				lg.member[m.UserID] = m.ID
			}
		}
		e.groups = append(e.groups, lg)
	}
	return e
}

func ldDispatchBody(g *ldGroup, handle, tag string) string {
	return fmt.Sprintf("[ld:g%d:%s:r%s] task\n@%s", g.idx, handle, tag, handle)
}

func (e *ldEnv) dispatch(t *testing.T, g *ldGroup, poster Actor, handle, tag string) {
	t.Helper()
	if _, err := e.svc.PostMessage(context.Background(), poster, g.id, PostMessageInput{
		BodyMD: ldDispatchBody(g, handle, tag), ClientMsgID: "d-" + tag, Intent: "dispatch"}); err != nil {
		t.Errorf("dispatch g%d %s: %v", g.idx, tag, err)
	}
}

func (e *ldEnv) requirements(t *testing.T, g *ldGroup) []RequirementView {
	t.Helper()
	var out []RequirementView
	cursor := ""
	for {
		page, err := e.svc.ListRequirements(context.Background(), g.owner, g.id, ListRequirementsInput{Cursor: cursor, Limit: 100})
		if err != nil {
			t.Fatalf("list requirements: %v", err)
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
}

func (e *ldEnv) allIn(t *testing.T, state string) func() bool {
	return func() bool {
		for _, g := range e.groups {
			for _, r := range e.requirements(t, g) {
				if r.State != state {
					return false
				}
			}
		}
		return true
	}
}

// ldWorker is one queue drainer built on the landed claim/run primitives.
type ldWorker struct {
	svc *Service
	id  string
}

func (w *ldWorker) drainOne(ctx context.Context) bool {
	job, err := w.svc.claimNext(ctx, w.id)
	if err != nil || job == nil {
		return false
	}
	_ = w.svc.runJob(ctx, w.id, job)
	return true
}

// runWorkers drains the queue with `workers` concurrent workers until done() holds.
func (e *ldEnv) runWorkers(t *testing.T, workers int, done func() bool, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &ldWorker{svc: e.svc, id: fmt.Sprintf("ldw-%d", i)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if !w.drainOne(ctx) {
					time.Sleep(2 * time.Millisecond)
				}
			}
		}()
	}
	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			cancel()
			wg.Wait()
			t.Fatalf("workers did not settle within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()
}

func (e *ldEnv) ldOpenJobs(t *testing.T) int64 {
	t.Helper()
	var n int64
	// Landed job states for unfinished work are queued|running (L5).
	if err := e.svc.repo.DB().Model(&store.GroupJobRecord{}).
		Where("state IN ?", []string{"queued", "running"}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// ridOwners maps every requirement id to its group id (for leakage checks).
func (e *ldEnv) ridOwners(t *testing.T) map[string]string {
	out := map[string]string{}
	for _, g := range e.groups {
		for _, r := range e.requirements(t, g) {
			out[r.ID] = g.id
		}
	}
	return out
}

// checkGroup asserts the Part B stream invariants for one group and returns its full history:
//   - events are exactly 1..N and N == group.last_event_seq
//   - messages are exactly 1..M and M == group.last_message_seq
//   - every message and every message.created event belongs to this group
//   - every event that names a requirement names one of THIS group's requirements
func (e *ldEnv) checkGroup(t *testing.T, g *ldGroup, owners map[string]string) ([]MessageView, []Event) {
	t.Helper()
	ctx := context.Background()
	evs, err := e.svc.EventsAfter(ctx, g.id, 0, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("g%d: event gap at index %d (seq %d)", g.idx, i, ev.Seq)
		}
		var data map[string]any
		_ = json.Unmarshal(ev.Data, &data)
		if rid, ok := data["requirement_id"].(string); ok && rid != "" && owners[rid] != g.id {
			t.Fatalf("g%d: event %d (%s) names foreign requirement %s", g.idx, ev.Seq, ev.Type, rid)
		}
		if ev.Type == "message.created" {
			if gid, _ := data["group_id"].(string); gid != g.id {
				t.Fatalf("g%d: event %d carries message of group %q", g.idx, ev.Seq, gid)
			}
		}
	}
	var msgs []MessageView
	page, err := e.svc.ListMessages(ctx, g.owner, g.id, ListMessagesInput{Limit: 200})
	for {
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(page.Items, msgs...)
		if !page.HasMore || len(page.Items) == 0 {
			break
		}
		page, err = e.svc.ListMessages(ctx, g.owner, g.id, ListMessagesInput{BeforeSeq: page.Items[0].Seq, Limit: 200})
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
	for i, m := range msgs {
		if m.Seq != int64(i+1) || m.GroupID != g.id {
			t.Fatalf("g%d: message index %d has seq %d group %s", g.idx, i, m.Seq, m.GroupID)
		}
	}
	gd, err := e.svc.GetGroup(ctx, g.owner, g.id)
	if err != nil {
		t.Fatal(err)
	}
	if gd.LastEventSeq != int64(len(evs)) || gd.LastMessageSeq != int64(len(msgs)) {
		t.Fatalf("g%d: counters ev=%d/%d msg=%d/%d", g.idx, gd.LastEventSeq, len(evs), gd.LastMessageSeq, len(msgs))
	}
	return msgs, evs
}

func (e *ldEnv) state(t *testing.T, g *ldGroup, rid string) string {
	t.Helper()
	r, err := e.svc.GetRequirement(context.Background(), g.owner, g.id, rid)
	if err != nil {
		t.Fatal(err)
	}
	return r.State
}

// ldApprove approves with the current plan hash threaded through, the
// way the landed P8 tests call Approve (an empty hash would skip the
// client-side check; the internal hash binding is enforced either way).
func (e *ldEnv) ldApprove(ctx context.Context, a Actor, g *ldGroup, rid string) error {
	v, err := e.svc.GetRequirement(ctx, a, g.id, rid)
	if err != nil {
		return err
	}
	in := ApproveInput{}
	if v.RedTeam != nil {
		in.PlanHash = v.RedTeam.PlanHash
	}
	_, err = e.svc.Approve(ctx, a, g.id, rid, in)
	return err
}

// toAwaitingApproval: one requirement per group to ai[0], reviewed by ai[1], through red team.
func (e *ldEnv) toAwaitingApproval(t *testing.T, perGroup int) {
	t.Helper()
	for _, g := range e.groups {
		for r := 0; r < perGroup; r++ {
			e.dispatch(t, g, g.owner, g.aiHandles[0], fmt.Sprintf("aw%d", r))
		}
	}
	e.runWorkers(t, 4, e.allIn(t, ReqProposed), time.Minute)
	for _, g := range e.groups {
		for _, r := range e.requirements(t, g) {
			if _, err := e.svc.StartReview(context.Background(), g.owner, g.id, r.ID,
				StartReviewInput{ReviewerMemberIDs: []string{g.member[g.aiHandles[1]]}}); err != nil {
				t.Fatalf("start review: %v", err)
			}
		}
	}
	e.runWorkers(t, 4, e.allIn(t, ReqAwaitingApproval), time.Minute)
}

func ldCodeIn(err error, allowed ...string) bool {
	c := code(err) // WIRING L1: the landed code() helper in service_test.go
	for _, a := range allowed {
		if c == a {
			return true
		}
	}
	return false
}
