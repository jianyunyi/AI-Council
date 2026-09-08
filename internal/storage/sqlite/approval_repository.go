package sqlite

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

var errApprovalNotActive = errors.New("matching approval is not active")

type ApprovalRecord struct {
	ID            string `gorm:"primaryKey"`
	RunID         string `gorm:"index"`
	PlanVersion   int
	SnapshotHash  string
	Decision      string
	Actor         string
	CreatedAt     time.Time
	InvalidatedAt *time.Time
	ConsumedAt    *time.Time
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

// Consume atomically marks the matching active approval as used. It returns false
// when the approval does not exist, does not match the immutable plan, or was
// already consumed.
func (r *ApprovalRepository) Consume(ctx context.Context, runID string, planVersion int, snapshotHash string) (bool, error) {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&ApprovalRecord{}).
		Where("run_id = ? AND plan_version = ? AND snapshot_hash = ? AND decision = ? AND invalidated_at IS NULL AND consumed_at IS NULL", runID, planVersion, snapshotHash, "approved").
		Update("consumed_at", now)
	return result.RowsAffected == 1, result.Error
}

// ConsumeAndMarkExecuting atomically consumes an approved snapshot and moves
// the matching persisted task into EXECUTING. A false result leaves both rows
// unchanged, so another process cannot lose an approval on a stale task state.
func (r *ApprovalRepository) ConsumeAndMarkExecuting(ctx context.Context, runID string, planVersion int, snapshotHash string) (bool, error) {
	now := time.Now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		consumed := tx.Model(&ApprovalRecord{}).
			Where("run_id = ? AND plan_version = ? AND snapshot_hash = ? AND decision = ? AND invalidated_at IS NULL AND consumed_at IS NULL", runID, planVersion, snapshotHash, "approved").
			Update("consumed_at", now)
		if consumed.Error != nil {
			return consumed.Error
		}
		if consumed.RowsAffected != 1 {
			return errApprovalNotActive
		}
		started := tx.Model(&TaskRecord{}).
			Where("id = ? AND state = ? AND plan_version = ? AND approval_hash = ? AND approval_granted = ?", runID, "AWAITING_APPROVAL", planVersion, snapshotHash, true).
			Update("state", "EXECUTING")
		if started.Error != nil {
			return started.Error
		}
		if started.RowsAffected != 1 {
			return errApprovalNotActive
		}
		return nil
	})
	if errors.Is(err, errApprovalNotActive) {
		return false, nil
	}
	return err == nil, err
}
