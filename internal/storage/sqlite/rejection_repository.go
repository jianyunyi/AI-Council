package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// RejectionCategories is the closed enum for TaskRejectionRecord.Category.
var RejectionCategories = map[string]bool{
	"incorrect": true,
	"unsafe":    true,
	"scope":     true,
	"quality":   true,
	"other":     true,
}

type RejectionRepository struct{ db *gorm.DB }

func NewRejectionRepository(db *gorm.DB) *RejectionRepository { return &RejectionRepository{db: db} }

func (r *RejectionRepository) Create(ctx context.Context, record TaskRejectionRecord) error {
	return r.db.WithContext(ctx).Create(&record).Error
}

// ListByTask returns rejections newest first.
func (r *RejectionRepository) ListByTask(ctx context.Context, taskID string, limit int) ([]TaskRejectionRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []TaskRejectionRecord
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("created_at DESC").Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *RejectionRepository) CountByTask(ctx context.Context, taskID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&TaskRejectionRecord{}).Where("task_id = ?", taskID).Count(&count).Error
	return count, err
}

// LatestByTaskIDs returns the newest rejection for each requested task.
func (r *RejectionRepository) LatestByTaskIDs(ctx context.Context, taskIDs []string) (map[string]TaskRejectionRecord, error) {
	out := make(map[string]TaskRejectionRecord, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	var rows []TaskRejectionRecord
	err := r.db.WithContext(ctx).Where("task_id IN ?", taskIDs).Order("created_at DESC").Order("id DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, seen := out[row.TaskID]; !seen {
			out[row.TaskID] = row
		}
	}
	return out, nil
}

// RejectAtomically performs the whole rejection in ONE transaction: insert
// the rejection row, move the task to the given state with approvals cleared,
// invalidate every still-active approval for the run (so a pending approval
// can no longer be consumed), and append the approval.rejected event. If any
// step fails, none of them land.
func (r *RejectionRepository) RejectAtomically(ctx context.Context, rejection TaskRejectionRecord, newState string, eventData any) (Event, error) {
	raw, err := json.Marshal(eventData)
	if err != nil {
		return Event{}, err
	}
	var out Event
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&rejection).Error; err != nil {
			return err
		}
		result := tx.Model(&TaskRecord{}).Where("id = ?", rejection.TaskID).Updates(map[string]any{
			"state": newState, "approval_granted": false, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("task not found for rejection")
		}
		now := time.Now().UTC()
		if err := tx.Model(&ApprovalRecord{}).Where("run_id = ? AND invalidated_at IS NULL", rejection.RunID).Update("invalidated_at", now).Error; err != nil {
			return err
		}
		var last EventRecord
		if err := tx.Where("run_id = ?", rejection.RunID).Order("sequence DESC").First(&last).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		rec := EventRecord{RunID: rejection.RunID, Sequence: last.Sequence + 1, Type: "approval.rejected", Data: raw, CreatedAt: now}
		if err := tx.Create(&rec).Error; err != nil {
			return err
		}
		out = Event{RunID: rec.RunID, Sequence: rec.Sequence, Type: rec.Type, Data: json.RawMessage(rec.Data), CreatedAt: rec.CreatedAt}
		return nil
	})
	return out, err
}
