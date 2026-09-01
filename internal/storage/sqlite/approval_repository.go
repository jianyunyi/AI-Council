package sqlite

import (
	"context"
	"gorm.io/gorm"
	"time"
)

type ApprovalRecord struct {
	ID            string `gorm:"primaryKey"`
	RunID         string `gorm:"index"`
	PlanVersion   int
	SnapshotHash  string
	Decision      string
	Actor         string
	CreatedAt     time.Time
	InvalidatedAt *time.Time
}
type ApprovalRepository struct{ db *gorm.DB }

func NewApprovalRepository(db *gorm.DB) *ApprovalRepository { return &ApprovalRepository{db: db} }
func (r *ApprovalRepository) Save(ctx context.Context, record ApprovalRecord) error {
	return r.db.WithContext(ctx).Create(&record).Error
}
func (r *ApprovalRepository) Current(ctx context.Context, runID string, planVersion int) (ApprovalRecord, error) {
	var out ApprovalRecord
	err := r.db.WithContext(ctx).Where("run_id = ? AND plan_version = ? AND decision = ? AND invalidated_at IS NULL", runID, planVersion, "approved").Order("created_at DESC").First(&out).Error
	return out, err
}
func (r *ApprovalRepository) Invalidate(ctx context.Context, runID string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&ApprovalRecord{}).Where("run_id = ? AND invalidated_at IS NULL", runID).Update("invalidated_at", now).Error
}
