package sqlite

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrAIModelNotFound = errors.New("ai model not found")
	ErrAIModelConflict = errors.New("ai model conflict")
)

type AIModelFilter struct {
	ProviderProfileID string
	Status            string
}

type AIModelRepo struct{ db *gorm.DB }

func NewAIModelRepo(db *gorm.DB) *AIModelRepo { return &AIModelRepo{db: db} }

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateWithWorkspace inserts the model and its workspace binding atomically.
func (r *AIModelRepo) CreateWithWorkspace(ctx context.Context, m *AIModelRecord, w *ModelWorkspaceRecord) error {
	w.AIModelID = m.ID
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		return tx.Create(w).Error
	})
	if isUniqueViolation(err) {
		return ErrAIModelConflict
	}
	return err
}

func (r *AIModelRepo) Get(ctx context.Context, id string) (*AIModelRecord, error) {
	var m AIModelRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAIModelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *AIModelRepo) List(ctx context.Context, f AIModelFilter) ([]AIModelRecord, error) {
	q := r.db.WithContext(ctx).Model(&AIModelRecord{})
	if f.ProviderProfileID != "" {
		q = q.Where("provider_profile_id = ?", f.ProviderProfileID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	var out []AIModelRecord
	if err := q.Order("handle ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateFields applies a partial update; returns ErrAIModelNotFound if no row matched.
func (r *AIModelRepo) UpdateFields(ctx context.Context, id string, fields map[string]any) error {
	res := r.db.WithContext(ctx).Model(&AIModelRecord{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAIModelNotFound
	}
	return nil
}

func (r *AIModelRepo) GetWorkspace(ctx context.Context, aiModelID string) (*ModelWorkspaceRecord, error) {
	var w ModelWorkspaceRecord
	err := r.db.WithContext(ctx).Where("ai_model_id = ?", aiModelID).Take(&w).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAIModelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *AIModelRepo) ListWorkspacesByStatus(ctx context.Context, status string) ([]ModelWorkspaceRecord, error) {
	var out []ModelWorkspaceRecord
	err := r.db.WithContext(ctx).Where("status = ?", status).Order("created_at ASC").Find(&out).Error
	return out, err
}

// transition is a compare-and-set on status. It returns true only if this caller won.
func (r *AIModelRepo) transition(ctx context.Context, aiModelID string, from []string, to string, extra map[string]any) (bool, error) {
	fields := map[string]any{"status": to, "updated_at": time.Now().UTC()}
	for k, v := range extra {
		fields[k] = v
	}
	res := r.db.WithContext(ctx).Model(&ModelWorkspaceRecord{}).
		Where("ai_model_id = ? AND status IN ?", aiModelID, from).
		Updates(fields)
	return res.RowsAffected == 1, res.Error
}

func (r *AIModelRepo) MarkWorkspaceReady(ctx context.Context, aiModelID, runnerWorkspaceID string) (bool, error) {
	return r.transition(ctx, aiModelID, []string{"provisioning"}, "ready", map[string]any{
		"runner_workspace_id": runnerWorkspaceID,
		"last_error":          "",
		"attempts":            gorm.Expr("attempts + 1"),
	})
}

func (r *AIModelRepo) MarkWorkspaceError(ctx context.Context, aiModelID, msg string) (bool, error) {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	return r.transition(ctx, aiModelID, []string{"provisioning"}, "error", map[string]any{
		"last_error": msg,
		"attempts":   gorm.Expr("attempts + 1"),
	})
}

// RequeueWorkspace moves error|archived back to provisioning (retry / reactivation).
func (r *AIModelRepo) RequeueWorkspace(ctx context.Context, aiModelID string) (bool, error) {
	return r.transition(ctx, aiModelID, []string{"error", "archived"}, "provisioning", nil)
}

func (r *AIModelRepo) ArchiveWorkspace(ctx context.Context, aiModelID string) (bool, error) {
	return r.transition(ctx, aiModelID, []string{"provisioning", "ready", "error"}, "archived", nil)
}
