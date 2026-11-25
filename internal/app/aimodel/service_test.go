package aimodel

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	store "github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

type fakeProfiles map[string]ProfileInfo

func (f fakeProfiles) GetProfile(_ context.Context, id string) (ProfileInfo, error) {
	p, ok := f[id]
	if !ok {
		return ProfileInfo{}, ErrProfileNotFound
	}
	return p, nil
}

type fakeModels map[string][]string

func (f fakeModels) ListModels(_ context.Context, id string) ([]string, error) { return f[id], nil }

type fakeWS struct {
	mu       sync.Mutex
	n        int
	archived map[string]bool
}

func (f *fakeWS) CreateModelWorkspace(_ context.Context, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return "ws_" + string(rune('a'+f.n)), nil
}
func (f *fakeWS) ArchiveWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archived[id] = true
	return nil
}
func (f *fakeWS) UnarchiveWorkspace(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.archived, id)
	return nil
}

type fakeRunner struct {
	calls atomic.Int32
	fail  atomic.Bool
}

func (f *fakeRunner) EnsureRunnerWorkspace(_ context.Context, wsID string) (string, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return "", errors.New("runner unavailable")
	}
	return "rw_" + wsID, nil
}

func newTestService(t *testing.T) (*Service, *fakeRunner, *fakeWS, *store.AIModelRepo) {
	t.Helper()
	// Codebase test style: a real store opened on a temp file (AutoMigrate
	// in store.Open creates the ai_models / model_workspaces tables).
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	repo := store.NewAIModelRepo(db)
	r := &fakeRunner{}
	ws := &fakeWS{archived: map[string]bool{}}
	svc := NewService(repo,
		fakeProfiles{"pp_1": {ID: "pp_1", Name: "Anthropic prod", ProviderType: "anthropic", Enabled: true, HasKey: true},
			"pp_off": {ID: "pp_off", Enabled: false, HasKey: true}},
		fakeModels{"pp_1": {"claude-sonnet-x"}}, ws, r)
	svc.spawn = func(f func()) { f() } // synchronous for determinism
	return svc, r, ws, repo
}

func TestCreateProvisionsReady(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	v, err := svc.Create(context.Background(), "u_admin", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "Claude-Sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Handle != "claude-sonnet" || v.Workspace == nil || v.Workspace.Status != WSReady || !v.Workspace.RunnerReady {
		t.Fatalf("unexpected view: %+v ws=%+v", v, v.Workspace)
	}
	if r.calls.Load() != 1 {
		t.Fatalf("want 1 runner call, got %d", r.calls.Load())
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	cases := []struct {
		in   CreateInput
		code string
	}{
		{CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "x"}, "invalid_handle"},
		{CreateInput{ProviderProfileID: "pp_nope", Model: "m", Handle: "ok-handle"}, "profile_not_found"},
		{CreateInput{ProviderProfileID: "pp_off", Model: "m", Handle: "ok-handle"}, "profile_disabled"},
		{CreateInput{ProviderProfileID: "pp_1", Model: "gpt-unknown", Handle: "ok-handle"}, "model_not_offered"},
		{CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "ok-handle",
			DefaultParams: map[string]any{"temperature": 5.0}}, "params_out_of_range"},
	}
	for _, c := range cases {
		_, err := svc.Create(ctx, "u", c.in)
		var se *Error
		if !errors.As(err, &se) || se.Code != c.code {
			t.Fatalf("input %+v: want %s, got %v", c.in, c.code, err)
		}
	}
}

func TestCreateConflictArchivesOrphan(t *testing.T) {
	svc, _, ws, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "a1"}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(ctx, "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "a2"})
	var se *Error
	if !errors.As(err, &se) || se.Code != "conflict" {
		t.Fatalf("want conflict, got %v", err)
	}
	if len(ws.archived) != 1 {
		t.Fatalf("orphan workspace must be archived, archived=%v", ws.archived)
	}
}

func TestProvisionIdempotentConcurrent(t *testing.T) {
	svc, r, _, repo := newTestService(t)
	ctx := context.Background()
	r.fail.Store(true)
	v, err := svc.Create(ctx, "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Workspace.Status != WSError {
		t.Fatalf("want error status, got %s", v.Workspace.Status)
	}
	r.fail.Store(false)
	r.calls.Store(0)
	if _, err := repo.RequeueWorkspace(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.Provision(ctx, v.ID) }()
	}
	wg.Wait()
	if r.calls.Load() != 1 {
		t.Fatalf("want exactly 1 registration, got %d", r.calls.Load())
	}
	got, _ := svc.Get(ctx, v.ID)
	if got.Workspace.Status != WSReady || got.Workspace.Attempts != 2 {
		t.Fatalf("unexpected workspace: %+v", got.Workspace)
	}
	svc.Provision(ctx, v.ID) // ready: no-op
	if r.calls.Load() != 1 {
		t.Fatal("provision on ready workspace must be a no-op")
	}
}

func TestRetryOnlyFromError(t *testing.T) {
	svc, r, _, _ := newTestService(t)
	ctx := context.Background()
	v, _ := svc.Create(ctx, "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "r1"})
	if _, err := svc.RetryWorkspace(ctx, v.ID); err == nil {
		t.Fatal("retry on ready must fail")
	}
	_, _ = svc.Disable(ctx, v.ID)
	r.fail.Store(true)
	st := StatusActive
	got, _ := svc.Update(ctx, v.ID, UpdateInput{Status: &st})
	if got.Workspace.Status != WSError {
		t.Fatalf("want error, got %s", got.Workspace.Status)
	}
	r.fail.Store(false)
	got, err := svc.RetryWorkspace(ctx, v.ID)
	if err != nil || got.Workspace.Status != WSReady {
		t.Fatalf("retry failed: %v %+v", err, got)
	}
}

func TestDisableArchivesAndIsIdempotent(t *testing.T) {
	svc, _, ws, _ := newTestService(t)
	ctx := context.Background()
	v, _ := svc.Create(ctx, "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "d1"})
	for i := 0; i < 2; i++ {
		got, err := svc.Disable(ctx, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusDisabled || got.Workspace.Status != WSArchived {
			t.Fatalf("unexpected: %+v %+v", got, got.Workspace)
		}
	}
	if !ws.archived[v.Workspace.WorkspaceID] {
		t.Fatal("workspace must be archived via provisioner")
	}
	// Reactivation unarchives and re-provisions.
	st := StatusActive
	got, err := svc.Update(ctx, v.ID, UpdateInput{Status: &st})
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace.Status != WSReady || ws.archived[v.Workspace.WorkspaceID] {
		t.Fatalf("reactivation failed: %+v", got.Workspace)
	}
}

func TestResponsesHaveNoKeyFields(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	v, _ := svc.Create(context.Background(), "u", CreateInput{ProviderProfileID: "pp_1", Model: "claude-sonnet-x", Handle: "k1"})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"api_key", "apiKey", "secret", "ciphertext"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("response leaks %q: %s", bad, b)
		}
	}
}
