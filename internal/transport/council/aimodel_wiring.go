package council

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/aicouncil/aicouncil/internal/app/aimodel"
	"github.com/aicouncil/aicouncil/internal/app/wskind"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

// Adapters wiring the aimodel service to this codebase (Part B P4 §7).
// Profiles live in ProviderProfileRecord (per owner, no name/enabled
// concepts): the lookup is by profile ID, the display name is derived from
// provider + model, Enabled is always true, and HasKey reflects whether an
// encrypted key is stored. A profile offers exactly its one configured
// model — the same fact GET /api/v1/providers/:id/models reports.

type profileLookupAdapter struct{ db *gorm.DB }

func (p profileLookupAdapter) GetProfile(ctx context.Context, id string) (aimodel.ProfileInfo, error) {
	var rec sqlite.ProviderProfileRecord
	err := p.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return aimodel.ProfileInfo{}, aimodel.ErrProfileNotFound
	}
	if err != nil {
		return aimodel.ProfileInfo{}, err
	}
	return aimodel.ProfileInfo{
		ID:           rec.ID,
		Name:         rec.Provider + " · " + rec.Model,
		ProviderType: rec.Provider,
		Enabled:      true,
		HasKey:       len(rec.APIKeyCiphertext) > 0,
	}, nil
}

type modelListerAdapter struct{ db *gorm.DB }

func (m modelListerAdapter) ListModels(ctx context.Context, profileID string) ([]string, error) {
	var rec sqlite.ProviderProfileRecord
	err := m.db.WithContext(ctx).Where("id = ?", profileID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, aimodel.ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return []string{rec.Model}, nil
}

// aiModelWorkspaceAdapter implements aimodel.WorkspaceProvisioner and
// aimodel.RunnerRegistrar against the API's workspace store. Model
// workspaces live under the Runner-relative path models/<handle>; the
// Runner knows them by the workspace ID itself, which is what
// EnsureRunnerWorkspace returns once registration has happened.
type aiModelWorkspaceAdapter struct{ a *API }

func (w *aiModelWorkspaceAdapter) CreateModelWorkspace(ctx context.Context, name, ownerID string) (string, error) {
	handle := strings.TrimPrefix(name, "model:")
	runnerPath := "models/" + handle
	a := w.a
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	id := "workspace-" + strconv.FormatInt(a.nextID, 10)
	if a.db != nil {
		if err := a.db.WithContext(ctx).Create(&sqlite.WorkspaceRecord{
			ID: id, OwnerUserID: ownerID, RunnerPath: runnerPath, Kind: wskind.ModelPrivate,
		}).Error; err != nil {
			a.nextID--
			return "", err
		}
	}
	a.workspaces[id] = workspace{ID: id, Kind: wskind.ModelPrivate, OwnerUserID: ownerID, RunnerPath: runnerPath}
	return id, nil
}

func (w *aiModelWorkspaceAdapter) ArchiveWorkspace(ctx context.Context, workspaceID string) error {
	a := w.a
	if a.db != nil {
		if err := a.db.WithContext(ctx).Model(&sqlite.WorkspaceRecord{}).Where("id = ?", workspaceID).Update("archived", true).Error; err != nil {
			return err
		}
	}
	a.mu.Lock()
	delete(a.workspaces, workspaceID)
	a.mu.Unlock()
	return nil
}

func (w *aiModelWorkspaceAdapter) UnarchiveWorkspace(ctx context.Context, workspaceID string) error {
	a := w.a
	if a.db == nil {
		return nil
	}
	if err := a.db.WithContext(ctx).Model(&sqlite.WorkspaceRecord{}).Where("id = ?", workspaceID).Update("archived", false).Error; err != nil {
		return err
	}
	var rec sqlite.WorkspaceRecord
	if err := a.db.WithContext(ctx).Where("id = ?", workspaceID).First(&rec).Error; err != nil {
		return err
	}
	a.mu.Lock()
	a.workspaces[rec.ID] = workspace{ID: rec.ID, Root: rec.CanonicalRoot, IsGit: rec.IsGit, Dirty: rec.Dirty, Kind: wskind.Normalize(rec.Kind), OwnerUserID: rec.OwnerUserID, RunnerPath: rec.RunnerPath}
	a.mu.Unlock()
	return nil
}

func (w *aiModelWorkspaceAdapter) EnsureRunnerWorkspace(ctx context.Context, workspaceID string) (string, error) {
	a := w.a
	if err := wskind.GuardExecution(ctx, sqlite.NewWorkspaceKindStore(a.db), workspaceID); err != nil {
		return "", err
	}
	var rec sqlite.WorkspaceRecord
	if err := a.db.WithContext(ctx).Where("id = ?", workspaceID).First(&rec).Error; err != nil {
		return "", err
	}
	if rec.CanonicalRoot != "" {
		return workspaceID, nil // already registered: idempotent
	}
	registrar, ok := a.runnerClient.(workspaceRegistrar)
	if !ok {
		return "", errors.New("runner unavailable")
	}
	registered, err := registrar.RegisterWorkspace(ctx, &runnerv1.RegisterWorkspaceRequest{WorkspaceId: workspaceID, RelativePath: rec.RunnerPath})
	if err != nil {
		return "", err
	}
	if registered == nil {
		return "", errors.New("runner returned no registration")
	}
	if err := a.db.WithContext(ctx).Model(&sqlite.WorkspaceRecord{}).Where("id = ?", workspaceID).Updates(map[string]any{
		"canonical_root": registered.Root, "is_git": registered.IsGit, "dirty": registered.Dirty,
	}).Error; err != nil {
		return "", err
	}
	a.mu.Lock()
	if ws, ok := a.workspaces[workspaceID]; ok {
		ws.Root, ws.IsGit, ws.Dirty = registered.Root, registered.IsGit, registered.Dirty
		a.workspaces[workspaceID] = ws
	}
	a.mu.Unlock()
	return workspaceID, nil
}

// InstallAIModels builds the AI model registry service against the API's
// database and workspace store and installs it on the API. Returns nil when
// the API has no database (in-memory mode). The caller should invoke
// ResumePending on the returned service once at startup.
func InstallAIModels(a *API) *aimodel.Service {
	if a.db == nil {
		return nil
	}
	adapter := &aiModelWorkspaceAdapter{a: a}
	svc := aimodel.NewService(
		sqlite.NewAIModelRepo(a.db),
		profileLookupAdapter{db: a.db},
		modelListerAdapter{db: a.db},
		adapter,
		adapter,
	)
	a.WithAIModelService(svc)
	return svc
}
