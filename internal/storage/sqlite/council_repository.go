package sqlite

import (
	"context"

	"gorm.io/gorm"
)

type CouncilRepository struct{ db *gorm.DB }

func NewCouncilRepository(db *gorm.DB) *CouncilRepository { return &CouncilRepository{db: db} }

func (r *CouncilRepository) SaveProviderProfile(ctx context.Context, record ProviderProfileRecord) error {
	return r.db.WithContext(ctx).Save(&record).Error
}
func (r *CouncilRepository) SaveWorkspace(ctx context.Context, record WorkspaceRecord) error {
	return r.db.WithContext(ctx).Save(&record).Error
}
func (r *CouncilRepository) SaveTask(ctx context.Context, record TaskRecord) error {
	return r.db.WithContext(ctx).Save(&record).Error
}
func (r *CouncilRepository) SaveSeats(ctx context.Context, records []SeatRecord) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range records {
			if err := tx.Save(&records[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *CouncilRepository) RecordInvocation(ctx context.Context, record ModelInvocationRecord) error {
	return r.db.WithContext(ctx).Create(&record).Error
}
