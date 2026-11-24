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

type fakeWS struct {
	mu       sync.Mutex
	created  []string
	archived map[string]bool
}

func (f *fakeWS) CreateGroupWorkspace(_ context.Context, name, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "ws_g" + string(rune('a'+len(f.created)))
	f.created = append(f.created, id)
	return id, nil
}

func (f *fakeWS) ArchiveWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archived[id] = true
	return nil
}

type fakeModels map[string]AIModelInfo

func (f fakeModels) GetAIModel(_ context.Context, id string) (AIModelInfo, error) {
	if m, ok := f[id]; ok {
		return m, nil
	}
	return AIModelInfo{}, ErrDirectoryNotFound
}

func (f fakeModels) FindAIModelByHandle(_ context.Context, h string) (AIModelInfo, error) {
	for _, m := range f {
		if m.Handle == h {
			return m, nil
		}
	}
	return AIModelInfo{}, ErrDirectoryNotFound
}

func (f fakeModels) SearchAIModels(_ context.Context, _ string, _ int) ([]AIModelInfo, error) {
	var out []AIModelInfo
	for _, m := range f {
		if m.Status == "active" {
			out = append(out, m)
		}
	}
	return out, nil
}

type fakeUsers map[string]UserInfo

func (f fakeUsers) GetUser(_ context.Context, id string) (UserInfo, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return UserInfo{}, ErrDirectoryNotFound
}

func (f fakeUsers) FindUserByUsername(_ context.Context, n string) (UserInfo, error) {
	for _, u := range f {
		if u.Username == n {
			return u, nil
		}
	}
	return UserInfo{}, ErrDirectoryNotFound
}

type fakeAudit struct {
	mu      sync.Mutex
	actions []string
}

func (f *fakeAudit) Record(_ context.Context, e AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, e.Action)
	return nil
}

type env struct {
	svc   *Service
	ws    *fakeWS
	audit *fakeAudit
	clock *time.Time
}

var (
	alice = Actor{UserID: "u_alice", CanCreateGroup: true}
	bob   = Actor{UserID: "u_bob", CanCreateGroup: true}
	eve   = Actor{UserID: "u_eve"}
)

// newEnv uses the project's own store.Open (file-backed, WAL + busy_timeout,
// full AutoMigrate) instead of an in-memory glebarez DSN: the glebarez/sqlite
// GORM dialector is not a dependency of this module.
func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "group-test.sqlite"))
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
		"aim_1": {ID: "aim_1", Handle: "claude-x", DisplayName: "Claude X", Status: "active", WorkspaceStatus: "ready"},
		"aim_2": {ID: "aim_2", Handle: "gpt-y", DisplayName: "GPT Y", Status: "active", WorkspaceStatus: "ready"},
		"aim_3": {ID: "aim_3", Handle: "off-z", DisplayName: "Off Z", Status: "disabled"},
	}
	users := fakeUsers{
		"u_alice": {ID: "u_alice", Username: "alice", DisplayName: "Alice"},
		"u_bob":   {ID: "u_bob", Username: "bob", DisplayName: "Bob"},
		"u_eve":   {ID: "u_eve", Username: "eve", DisplayName: "Eve"},
	}
	svc := NewService(store.NewGroupRepo(db), ws, models, users, audit, NewHub(64))
	clock := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	return &env{svc: svc, ws: ws, audit: audit, clock: &clock}
}

func code(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	if err == nil {
		return ""
	}
	return "untyped:" + err.Error()
}

func (e *env) mkGroup(t *testing.T) *GroupDetail {
	t.Helper()
	g, err := e.svc.CreateGroup(context.Background(), alice, CreateGroupInput{
		Name: "Team", Members: []AddMemberInput{
			{MemberType: MemberUser, UserID: "u_bob", GroupRole: RoleOperator},
			{MemberType: MemberAI, AIModelID: "aim_1"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestCreateGroup(t *testing.T) {
	e := newEnv(t)
	g := e.mkGroup(t)
	if g.MyRole != RoleOwner || !g.CanManage || len(g.Members) != 3 || g.SharedWorkspaceID != "ws_ga" {
		t.Fatalf("unexpected group: %+v", g)
	}
	if !g.Policy.RequireHumanApproval || g.LastEventSeq != 1 {
		t.Fatalf("policy=%+v evseq=%d", g.Policy, g.LastEventSeq)
	}
	if _, err := e.svc.CreateGroup(context.Background(), eve, CreateGroupInput{Name: "x"}); code(err) != "forbidden" {
		t.Fatalf("eve lacks group:create: %v", err)
	}
	_, err := e.svc.CreateGroup(context.Background(), alice, CreateGroupInput{Name: "x",
		Members: []AddMemberInput{{MemberType: MemberAI, AIModelID: "aim_3"}}})
	if code(err) != "ai_model_not_active" {
		t.Fatalf("disabled model: %v", err)
	}
	if len(e.ws.created) != 1 {
		t.Fatal("validation failures must not create a workspace")
	}
}

func TestNonMemberGets404(t *testing.T) {
	e := newEnv(t)
	g := e.mkGroup(t)
	if _, err := e.svc.GetGroup(context.Background(), eve, g.ID); code(err) != "group_not_found" {
		t.Fatalf("want 404, got %v", err)
	}
	if _, err := e.svc.GetGroup(context.Background(), Actor{UserID: "u_eve", ReadAny: true}, g.ID); err != nil {
		t.Fatalf("read:any must see it: %v", err)
	}
	list, _, err := e.svc.ListGroups(context.Background(), eve, ListGroupsInput{})
	if err != nil || len(list) != 0 {
		t.Fatalf("eve list=%d err=%v", len(list), err)
	}
}

func TestPostMessageIdempotentAndMentions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	body := "hi @claude-x @gpt-y @nobody @bob @eve"
	r1, err := e.svc.PostMessage(ctx, bob, g.ID, PostMessageInput{BodyMD: body, ClientMsgID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Duplicate || r1.Message.Seq != 1 || r1.Message.Author == nil || r1.Message.Author.Handle != "bob" {
		t.Fatalf("r1=%+v", r1.Message)
	}
	want := map[string]string{"claude-x": MentionResolved, "gpt-y": MentionNotMember, "nobody": MentionUnknown,
		"bob": MentionResolved, "eve": MentionNotMember}
	if len(r1.Message.Mentions) != 5 {
		t.Fatalf("mentions=%+v", r1.Message.Mentions)
	}
	for _, m := range r1.Message.Mentions {
		if want[m.Handle] != m.Resolution {
			t.Fatalf("%s: got %s want %s", m.Handle, m.Resolution, want[m.Handle])
		}
	}
	r2, err := e.svc.PostMessage(ctx, bob, g.ID, PostMessageInput{BodyMD: body, ClientMsgID: "c1"})
	if err != nil || !r2.Duplicate || r2.Message.ID != r1.Message.ID {
		t.Fatalf("retry must return the same message: %+v %v", r2, err)
	}
	r3, _ := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{BodyMD: "x", ClientMsgID: "c1"})
	if r3.Duplicate || r3.Message.Seq != 2 {
		t.Fatalf("client_msg_id is scoped per author: %+v", r3)
	}
}

func TestPostMessageGuards(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	cases := []struct {
		actor Actor
		in    PostMessageInput
		want  string
	}{
		{eve, PostMessageInput{BodyMD: "x", ClientMsgID: "a"}, "group_not_found"},
		{Actor{UserID: "u_eve", ReadAny: true}, PostMessageInput{BodyMD: "x", ClientMsgID: "a"}, "not_a_member"},
		{bob, PostMessageInput{BodyMD: "   ", ClientMsgID: "a"}, "invalid_body"},
		{bob, PostMessageInput{BodyMD: "x", ClientMsgID: ""}, "invalid_client_msg_id"},
		{bob, PostMessageInput{BodyMD: "@claude-x do", ClientMsgID: "a", Intent: "dispatch"}, "intent_not_available"},
		{bob, PostMessageInput{BodyMD: "x", ClientMsgID: "a", Intent: "bogus"}, "invalid_intent"},
		{bob, PostMessageInput{BodyMD: "x", ClientMsgID: "a", RequirementID: "req_1"}, "requirement_not_available"},
		{bob, PostMessageInput{BodyMD: "x", ClientMsgID: "a", ReplyToMessageID: "msg_nope"}, "reply_not_found"},
	}
	for _, c := range cases {
		if _, err := e.svc.PostMessage(ctx, c.actor, g.ID, c.in); code(err) != c.want {
			t.Fatalf("%+v: want %s got %v", c.in, c.want, err)
		}
	}
}

func TestEditMessageWindowAndHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	r, _ := e.svc.PostMessage(ctx, bob, g.ID, PostMessageInput{BodyMD: "hello", ClientMsgID: "c"})
	if _, err := e.svc.EditMessage(ctx, alice, g.ID, r.Message.ID, "x"); code(err) != "message_not_editable" {
		t.Fatalf("others cannot edit: %v", err)
	}
	v, err := e.svc.EditMessage(ctx, bob, g.ID, r.Message.ID, "hello @claude-x")
	if err != nil || v.EditedAt == nil || len(v.Mentions) != 1 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	*e.clock = e.clock.Add(11 * time.Minute)
	if _, err := e.svc.EditMessage(ctx, bob, g.ID, r.Message.ID, "late"); code(err) != "message_not_editable" {
		t.Fatalf("edit window: %v", err)
	}
}

func TestMembershipRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	var ownerID, bobID, aiID string
	for _, m := range g.Members {
		switch {
		case m.UserID == "u_alice":
			ownerID = m.ID
		case m.UserID == "u_bob":
			bobID = m.ID
		case m.MemberType == MemberAI:
			aiID = m.ID
		}
	}
	if err := e.svc.RemoveMember(ctx, alice, g.ID, ownerID); code(err) != "last_owner" {
		t.Fatalf("last owner: %v", err)
	}
	if _, err := e.svc.AddMember(ctx, bob, g.ID, AddMemberInput{MemberType: MemberAI, AIModelID: "aim_2"}); code(err) != "forbidden" {
		t.Fatalf("operator cannot add: %v", err)
	}
	if _, err := e.svc.AddMember(ctx, alice, g.ID, AddMemberInput{MemberType: MemberAI, AIModelID: "aim_1"}); code(err) != "member_exists" {
		t.Fatalf("duplicate AI: %v", err)
	}
	ai := RoleOwner
	if _, err := e.svc.UpdateMember(ctx, alice, g.ID, aiID, UpdateMemberInput{GroupRole: &ai}); code(err) != "ai_role_fixed" {
		t.Fatalf("ai role fixed: %v", err)
	}

	sub := e.svc.Hub().Subscribe(g.ID, "u_bob")
	if err := e.svc.RemoveMember(ctx, bob, g.ID, bobID); err != nil { // self-leave
		t.Fatal(err)
	}
	for range sub.C { // drain until the kick closes C
	}
	if _, err := e.svc.GetGroup(ctx, bob, g.ID); code(err) != "group_not_found" {
		t.Fatalf("removed member loses access: %v", err)
	}
	if _, err := e.svc.AddMember(ctx, alice, g.ID, AddMemberInput{MemberType: MemberUser, UserID: "u_bob"}); err != nil {
		t.Fatalf("re-add after removal: %v", err)
	}
}

func TestUpdateGroupCASAndArchive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	n := "Renamed"
	if _, err := e.svc.UpdateGroup(ctx, alice, g.ID, UpdateGroupInput{Name: &n}); code(err) != "expected_version_required" {
		t.Fatalf("%v", err)
	}
	up, err := e.svc.UpdateGroup(ctx, alice, g.ID, UpdateGroupInput{Name: &n, ExpectedVersion: g.Version})
	if err != nil || up.Name != "Renamed" || up.Version != g.Version+1 {
		t.Fatalf("up=%+v err=%v", up, err)
	}
	if _, err := e.svc.UpdateGroup(ctx, alice, g.ID, UpdateGroupInput{Name: &n, ExpectedVersion: g.Version}); code(err) != "version_conflict" {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := e.svc.UpdateGroup(ctx, alice, g.ID, UpdateGroupInput{
		Policy: map[string]any{"require_human_approval": false}, ExpectedVersion: up.Version}); code(err) != "human_approval_required" {
		t.Fatalf("D1 lock: %v", err)
	}
	ar, err := e.svc.ArchiveGroup(ctx, alice, g.ID)
	if err != nil || ar.Status != StatusArchived || !e.ws.archived[g.SharedWorkspaceID] {
		t.Fatalf("archive: %+v %v", ar, err)
	}
	if _, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{BodyMD: "x", ClientMsgID: "z"}); code(err) != "group_archived" {
		t.Fatalf("post to archived: %v", err)
	}
}

func TestEventsOrderedAndReplayable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	sub := e.svc.Hub().Subscribe(g.ID, "u_alice")
	defer sub.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.PostMessage(ctx, alice, g.ID, PostMessageInput{BodyMD: "m", ClientMsgID: string(rune('A' + i))})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	evs, err := e.svc.EventsAfter(ctx, g.ID, 0, 100)
	if err != nil || len(evs) != 21 { // group.created + 20 messages
		t.Fatalf("events=%d err=%v", len(evs), err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("gap at %d: %d", i, ev.Seq)
		}
	}
	var prev int64 = 1
	for i := 0; i < 20; i++ {
		ev := <-sub.C
		if ev.Seq != prev+1 {
			t.Fatalf("live order broken: %d after %d", ev.Seq, prev)
		}
		prev = ev.Seq
	}
	page, err := e.svc.ListMessages(ctx, alice, g.ID, ListMessagesInput{Limit: 5})
	if err != nil || len(page.Items) != 5 || !page.HasMore || page.Items[4].Seq != 20 || page.Items[0].Seq != 16 {
		t.Fatalf("newest page wrong: %+v", page)
	}
	older, _ := e.svc.ListMessages(ctx, alice, g.ID, ListMessagesInput{BeforeSeq: 16, Limit: 5})
	if older.Items[4].Seq != 15 {
		t.Fatalf("before_seq page wrong: %+v", older.Items)
	}
}

func TestMentionCandidates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := e.mkGroup(t)
	owner, _ := e.svc.ResolveMentionCandidates(ctx, alice, g.ID, "")
	var hasNonMember bool
	for _, c := range owner {
		if !c.IsMember && c.Handle == "gpt-y" {
			hasNonMember = true
		}
		if c.Handle == "off-z" {
			t.Fatal("disabled models must not be suggested")
		}
	}
	if !hasNonMember {
		t.Fatalf("owner should see addable models: %+v", owner)
	}
	op, _ := e.svc.ResolveMentionCandidates(ctx, bob, g.ID, "cl")
	if len(op) != 1 || op[0].Handle != "claude-x" || !op[0].IsMember {
		t.Fatalf("operator prefix search: %+v", op)
	}
}
