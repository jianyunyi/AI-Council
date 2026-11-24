package sqlite

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Sentinel errors for the P8 store, following the ErrGroup*/ErrReview*
// convention of the sibling repos.
var (
	ErrRedTeamCheckNotFound = errors.New("red-team check not found")
	ErrPushdownNotFound     = errors.New("pushdown not found")
)

// PushdownRepo persists P8 red-team checks and push-downs. Like
// RequirementRepo/ReviewRepo it is constructed over any *gorm.DB — the
// request-scoped handle, or tx.DB() inside a GroupRepo.Tx so a check,
// its job and its events commit atomically.
type PushdownRepo struct{ db *gorm.DB }

func NewPushdownRepo(db *gorm.DB) *PushdownRepo { return &PushdownRepo{db: db} }

func (r *PushdownRepo) DB() *gorm.DB { return r.db }

// ---- red-team checks ----

func (r *PushdownRepo) CreateCheck(ctx context.Context, c *RedTeamCheckRecord) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *PushdownRepo) GetCheck(ctx context.Context, id string) (*RedTeamCheckRecord, error) {
	var c RedTeamCheckRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRedTeamCheckNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// LatestCheck returns the newest check for a proposal attempt, or
// (nil, nil) when the attempt was never checked.
func (r *PushdownRepo) LatestCheck(ctx context.Context, requirementID string, attempt int) (*RedTeamCheckRecord, error) {
	var c RedTeamCheckRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ? AND proposal_attempt = ?", requirementID, attempt).
		Order("created_at DESC, id DESC").First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *PushdownRepo) ListChecks(ctx context.Context, requirementID string) ([]RedTeamCheckRecord, error) {
	var out []RedTeamCheckRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ?", requirementID).Order("created_at ASC, id ASC").Find(&out).Error
	return out, err
}

// TransitionCheck is a CAS on status; true only if this caller won.
func (r *PushdownRepo) TransitionCheck(ctx context.Context, id string, from []string, fields map[string]any) (bool, error) {
	res := r.db.WithContext(ctx).Model(&RedTeamCheckRecord{}).Where("id = ? AND status IN ?", id, from).Updates(fields)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// SupersedeOpenChecks marks a requirement's unfinished checks
// superseded (the requirement was cancelled or revised away).
func (r *PushdownRepo) SupersedeOpenChecks(ctx context.Context, requirementID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&RedTeamCheckRecord{}).
		Where("requirement_id = ? AND status IN ?", requirementID, []string{"queued", "running"}).
		Updates(map[string]any{"status": "superseded", "finished_at": at}).Error
}

// ---- pushdowns ----

func (r *PushdownRepo) CreatePushdown(ctx context.Context, p *RequirementPushdownRecord) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *PushdownRepo) GetPushdownForAttempt(ctx context.Context, requirementID string, attempt int) (*RequirementPushdownRecord, error) {
	var p RequirementPushdownRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ? AND proposal_attempt = ?", requirementID, attempt).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPushdownNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// LatestPushdown returns the newest pushdown of a requirement, or
// (nil, nil) when it was never approved.
func (r *PushdownRepo) LatestPushdown(ctx context.Context, requirementID string) (*RequirementPushdownRecord, error) {
	var p RequirementPushdownRecord
	err := r.db.WithContext(ctx).Where("requirement_id = ?", requirementID).
		Order("approved_at DESC, id DESC").First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PushdownRepo) GetPushdownByTask(ctx context.Context, taskID string) (*RequirementPushdownRecord, error) {
	var p RequirementPushdownRecord
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPushdownNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PushdownRepo) ListPushdownsByStatus(ctx context.Context, statuses []string) ([]RequirementPushdownRecord, error) {
	var out []RequirementPushdownRecord
	err := r.db.WithContext(ctx).Where("status IN ?", statuses).Order("approved_at ASC").Find(&out).Error
	return out, err
}

func (r *PushdownRepo) UpdatePushdown(ctx context.Context, id string, fields map[string]any, at time.Time) error {
	fields["updated_at"] = at
	return r.db.WithContext(ctx).Model(&RequirementPushdownRecord{}).Where("id = ?", id).Updates(fields).Error
}

// AdvanceMirror moves last_mirrored_state only if it still equals prev
// (dedupes replays and concurrent mirror goroutines). True only if
// this caller won. Callers read prev BEFORE their transaction; the CAS
// is what makes the read-then-write safe.
func (r *PushdownRepo) AdvanceMirror(ctx context.Context, id, prev, next string, at time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&RequirementPushdownRecord{}).
		Where("id = ? AND last_mirrored_state = ?", id, prev).
		Updates(map[string]any{"last_mirrored_state": next, "updated_at": at})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}
