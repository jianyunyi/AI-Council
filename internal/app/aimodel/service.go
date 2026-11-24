package aimodel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aicouncil/aicouncil/internal/app/wskind"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusDegraded = "degraded"

	WSProvisioning = "provisioning"
	WSReady        = "ready"
	WSError        = "error"
	WSArchived     = "archived"
)

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

type WorkspaceView struct {
	WorkspaceID string    `json:"workspace_id"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	LastError   string    `json:"last_error,omitempty"`
	Attempts    int       `json:"attempts"`
	RunnerReady bool      `json:"runner_ready"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// View is the API DTO. It never includes provider keys.
type View struct {
	ID                string         `json:"id"`
	ProviderProfileID string         `json:"provider_profile_id"`
	ProviderName      string         `json:"provider_name"`
	ProviderType      string         `json:"provider_type"`
	Model             string         `json:"model"`
	Handle            string         `json:"handle"`
	DisplayName       string         `json:"display_name"`
	DefaultParams     map[string]any `json:"default_params"`
	Status            string         `json:"status"`
	Workspace         *WorkspaceView `json:"workspace"`
	Warnings          []string       `json:"warnings,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type CreateInput struct {
	ProviderProfileID string         `json:"provider_profile_id"`
	Model             string         `json:"model"`
	Handle            string         `json:"handle"`
	DisplayName       string         `json:"display_name"`
	DefaultParams     map[string]any `json:"default_params"`
}

type UpdateInput struct {
	DisplayName   *string        `json:"display_name,omitempty"`
	DefaultParams map[string]any `json:"default_params,omitempty"`
	Status        *string        `json:"status,omitempty"`
}

type Service struct {
	repo     *sqlite.AIModelRepo
	profiles ProfileLookup
	models   ModelLister // may be nil
	ws       WorkspaceProvisioner
	runner   RunnerRegistrar
	spawn    func(func()) // async executor; tests replace with synchronous
	locks    sync.Map     // aiModelID -> *sync.Mutex
}

func NewService(repo *sqlite.AIModelRepo, p ProfileLookup, m ModelLister, ws WorkspaceProvisioner, r RunnerRegistrar) *Service {
	return &Service{repo: repo, profiles: p, models: m, ws: ws, runner: r, spawn: func(f func()) { go f() }}
}

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Service) lock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Service) Create(ctx context.Context, actorID string, in CreateInput) (*View, error) {
	in.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
	in.Model = strings.TrimSpace(in.Model)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if !handleRe.MatchString(in.Handle) {
		return nil, errInvalidHandle
	}
	if in.Model == "" || utf8.RuneCountInString(in.Model) > 200 {
		return nil, errInvalidModel
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Handle
	}
	if utf8.RuneCountInString(in.DisplayName) > 120 {
		return nil, errInvalidName
	}
	paramsJSON, err := validateParams(in.DefaultParams)
	if err != nil {
		return nil, err
	}

	prof, err := s.profiles.GetProfile(ctx, in.ProviderProfileID)
	if errors.Is(err, ErrProfileNotFound) {
		return nil, errProfileMissing
	}
	if err != nil {
		return nil, err
	}
	if !prof.Enabled {
		return nil, errProfileOff
	}
	if !prof.HasKey {
		return nil, errProfileNoKey
	}

	var warnings []string
	if s.models != nil {
		offered, lerr := s.models.ListModels(ctx, prof.ID)
		if lerr != nil {
			warnings = append(warnings, "model_unverified")
		} else if !contains(offered, in.Model) {
			return nil, errModelNotOffer
		}
	} else {
		warnings = append(warnings, "model_unverified")
	}

	wsID, err := s.ws.CreateModelWorkspace(ctx, "model:"+in.Handle, actorID)
	if err != nil {
		return nil, err
	}
	rec := &sqlite.AIModelRecord{
		ID: newID("aim_"), ProviderProfileID: prof.ID, Model: in.Model, Handle: in.Handle,
		DisplayName: in.DisplayName, DefaultParamsJSON: []byte(paramsJSON), Status: StatusActive, CreatedBy: actorID,
	}
	mw := &sqlite.ModelWorkspaceRecord{WorkspaceID: wsID, Status: WSProvisioning}
	if err := s.repo.CreateWithWorkspace(ctx, rec, mw); err != nil {
		// Do not leave an orphan workspace behind.
		if aerr := s.ws.ArchiveWorkspace(context.Background(), wsID); aerr != nil {
			log.Printf("aimodel: archive orphan workspace %s: %v", wsID, aerr)
		}
		if errors.Is(err, sqlite.ErrAIModelConflict) {
			return nil, errConflict
		}
		return nil, err
	}

	id := rec.ID
	s.spawn(func() { s.Provision(context.Background(), id) })

	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Warnings = warnings
	return v, nil
}

// Provision registers the Runner workspace. Safe to call many times and concurrently:
// an in-process mutex, a status check, CAS transitions and an idempotent registrar
// together guarantee at most one effective registration per workspace.
func (s *Service) Provision(ctx context.Context, aiModelID string) {
	mu := s.lock(aiModelID)
	mu.Lock()
	defer mu.Unlock()

	w, err := s.repo.GetWorkspace(ctx, aiModelID)
	if err != nil {
		log.Printf("aimodel: provision %s: %v", aiModelID, err)
		return
	}
	if w.Status != WSProvisioning {
		return
	}
	rid, err := s.runner.EnsureRunnerWorkspace(ctx, w.WorkspaceID)
	if err != nil {
		if _, merr := s.repo.MarkWorkspaceError(ctx, aiModelID, err.Error()); merr != nil {
			log.Printf("aimodel: mark error %s: %v", aiModelID, merr)
		}
		return
	}
	if _, err := s.repo.MarkWorkspaceReady(ctx, aiModelID, rid); err != nil {
		log.Printf("aimodel: mark ready %s: %v", aiModelID, err)
	}
}

// ResumePending re-drives provisioning interrupted by a restart. Call once at startup.
func (s *Service) ResumePending(ctx context.Context) error {
	ws, err := s.repo.ListWorkspacesByStatus(ctx, WSProvisioning)
	if err != nil {
		return err
	}
	for _, w := range ws {
		id := w.AIModelID
		s.spawn(func() { s.Provision(context.Background(), id) })
	}
	return nil
}

// RetryWorkspace moves an errored workspace back to provisioning.
func (s *Service) RetryWorkspace(ctx context.Context, aiModelID string) (*View, error) {
	w, err := s.repo.GetWorkspace(ctx, aiModelID)
	if errors.Is(err, sqlite.ErrAIModelNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if w.Status != WSError {
		return nil, errNotRetryable
	}
	if ok, err := s.repo.RequeueWorkspace(ctx, aiModelID); err != nil {
		return nil, err
	} else if ok {
		s.spawn(func() { s.Provision(context.Background(), aiModelID) })
	}
	return s.Get(ctx, aiModelID)
}

func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (*View, error) {
	cur, err := s.repo.Get(ctx, id)
	if errors.Is(err, sqlite.ErrAIModelNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if in.DisplayName != nil {
		n := strings.TrimSpace(*in.DisplayName)
		if n == "" || utf8.RuneCountInString(n) > 120 {
			return nil, errInvalidName
		}
		fields["display_name"] = n
	}
	if in.DefaultParams != nil {
		pj, err := validateParams(in.DefaultParams)
		if err != nil {
			return nil, err
		}
		fields["default_params_json"] = []byte(pj)
	}
	reactivate := false
	if in.Status != nil {
		st := *in.Status
		if st != StatusActive && st != StatusDisabled && st != StatusDegraded {
			return nil, errInvalidStatus
		}
		if st == StatusDisabled {
			return s.Disable(ctx, id)
		}
		fields["status"] = st
		reactivate = cur.Status == StatusDisabled && st == StatusActive
	}
	if len(fields) > 0 {
		if err := s.repo.UpdateFields(ctx, id, fields); err != nil {
			return nil, err
		}
	}
	if reactivate {
		w, err := s.repo.GetWorkspace(ctx, id)
		if err != nil {
			return nil, err
		}
		if w.Status == WSArchived {
			if err := s.ws.UnarchiveWorkspace(ctx, w.WorkspaceID); err != nil {
				return nil, err
			}
			if ok, err := s.repo.RequeueWorkspace(ctx, id); err != nil {
				return nil, err
			} else if ok {
				s.spawn(func() { s.Provision(context.Background(), id) })
			}
		}
	}
	return s.Get(ctx, id)
}

// Disable is the DELETE semantics: soft-disable plus archive. Never a hard delete. Idempotent.
func (s *Service) Disable(ctx context.Context, id string) (*View, error) {
	if err := s.repo.UpdateFields(ctx, id, map[string]any{"status": StatusDisabled}); err != nil {
		if errors.Is(err, sqlite.ErrAIModelNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	w, err := s.repo.GetWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.Status != WSArchived {
		if err := s.ws.ArchiveWorkspace(ctx, w.WorkspaceID); err != nil {
			return nil, err
		}
		if _, err := s.repo.ArchiveWorkspace(ctx, id); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*View, error) {
	rec, err := s.repo.Get(ctx, id)
	if errors.Is(err, sqlite.ErrAIModelNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.toView(ctx, rec, map[string]ProfileInfo{})
}

func (s *Service) List(ctx context.Context, f sqlite.AIModelFilter) ([]View, error) {
	recs, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}
	cache := map[string]ProfileInfo{}
	out := make([]View, 0, len(recs))
	for i := range recs {
		v, err := s.toView(ctx, &recs[i], cache)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (s *Service) GetWorkspace(ctx context.Context, id string) (*WorkspaceView, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return v.Workspace, nil
}

func (s *Service) toView(ctx context.Context, rec *sqlite.AIModelRecord, cache map[string]ProfileInfo) (*View, error) {
	prof, ok := cache[rec.ProviderProfileID]
	if !ok {
		p, err := s.profiles.GetProfile(ctx, rec.ProviderProfileID)
		if err != nil && !errors.Is(err, ErrProfileNotFound) {
			return nil, err
		}
		prof = p
		cache[rec.ProviderProfileID] = p
	}
	params := map[string]any{}
	_ = json.Unmarshal(rec.DefaultParamsJSON, &params)
	v := &View{
		ID: rec.ID, ProviderProfileID: rec.ProviderProfileID, ProviderName: prof.Name, ProviderType: prof.ProviderType,
		Model: rec.Model, Handle: rec.Handle, DisplayName: rec.DisplayName, DefaultParams: params,
		Status: rec.Status, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
	if w, err := s.repo.GetWorkspace(ctx, rec.ID); err == nil {
		v.Workspace = &WorkspaceView{
			WorkspaceID: w.WorkspaceID, Kind: wskind.ModelPrivate, Status: w.Status, LastError: w.LastError,
			Attempts: w.Attempts, RunnerReady: w.Status == WSReady && w.RunnerWorkspaceID != "", UpdatedAt: w.UpdatedAt,
		}
	} else if !errors.Is(err, sqlite.ErrAIModelNotFound) {
		return nil, err
	}
	return v, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
