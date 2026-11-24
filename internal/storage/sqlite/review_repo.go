package sqlite

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ReviewRepo persists review rounds and their (blind) decisions. It
// follows the RequirementRepo pattern: construct over any *gorm.DB —
// the request-scoped handle, or tx.DB() inside a GroupRepo.Tx so round
// creation, decision rows, jobs and events commit atomically.
type ReviewRepo struct{ db *gorm.DB }

func NewReviewRepo(db *gorm.DB) *ReviewRepo { return &ReviewRepo{db: db} }

func (r *ReviewRepo) DB() *gorm.DB { return r.db }

func (r *ReviewRepo) CreateRound(ctx context.Context, rec *ReviewRoundRecord) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

func (r *ReviewRepo) CreateDecision(ctx context.Context, rec *ReviewDecisionRecord) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

func (r *ReviewRepo) GetRound(ctx context.Context, id string) (*ReviewRoundRecord, error) {
	var rec ReviewRoundRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetOpenRound returns the requirement's single open round, or
// ErrReviewNotFound when none is open.
func (r *ReviewRepo) GetOpenRound(ctx context.Context, requirementID string) (*ReviewRoundRecord, error) {
	var rec ReviewRoundRecord
	err := r.db.WithContext(ctx).
		Where("requirement_id = ? AND status = ?", requirementID, "open").
		Order("created_at DESC, id DESC").First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *ReviewRepo) GetLatestRound(ctx context.Context, requirementID string) (*ReviewRoundRecord, error) {
	var rec ReviewRoundRecord
	err := r.db.WithContext(ctx).
		Where("requirement_id = ?", requirementID).
		Order("proposal_round DESC, created_at DESC, id DESC").First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *ReviewRepo) ListRounds(ctx context.Context, requirementID string) ([]ReviewRoundRecord, error) {
	var out []ReviewRoundRecord
	err := r.db.WithContext(ctx).
		Where("requirement_id = ?", requirementID).
		Order("proposal_round ASC, created_at ASC, id ASC").Find(&out).Error
	return out, err
}

func (r *ReviewRepo) UpdateRound(ctx context.Context, id string, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&ReviewRoundRecord{}).Where("id = ?", id).Updates(fields).Error
}

func (r *ReviewRepo) GetDecision(ctx context.Context, id string) (*ReviewDecisionRecord, error) {
	var rec ReviewDecisionRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *ReviewRepo) FindDecision(ctx context.Context, roundID, memberID string) (*ReviewDecisionRecord, error) {
	var rec ReviewDecisionRecord
	err := r.db.WithContext(ctx).
		Where("round_id = ? AND reviewer_member = ?", roundID, memberID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *ReviewRepo) ListDecisions(ctx context.Context, roundID string) ([]ReviewDecisionRecord, error) {
	var out []ReviewDecisionRecord
	err := r.db.WithContext(ctx).
		Where("round_id = ?", roundID).
		Order("created_at ASC, id ASC").Find(&out).Error
	return out, err
}

// TransitionDecision CAS-moves a decision out of one of fromStatuses,
// applying fields. Returns (false, nil) when the decision was no longer
// in a from-status (lost race, round superseded, ...) — callers treat
// that as "already handled", never as an error.
func (r *ReviewRepo) TransitionDecision(ctx context.Context, id string, from []string, fields map[string]any) (bool, error) {
	res := r.db.WithContext(ctx).Model(&ReviewDecisionRecord{}).
		Where("id = ? AND status IN ?", id, from).Updates(fields)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *ReviewRepo) UpdateDecisionFields(ctx context.Context, id string, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&ReviewDecisionRecord{}).Where("id = ?", id).Updates(fields).Error
}
