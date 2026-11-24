package sqlite

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCouncilSetConflict = errors.New("council role assignment set version conflict")

type CouncilRolesRepository struct{ db *gorm.DB }

func NewCouncilRolesRepository(db *gorm.DB) *CouncilRolesRepository {
	return &CouncilRolesRepository{db: db}
}

func scopeIDFor(scopeType, scopeID string) string {
	if scopeType == "global" {
		return ""
	}
	return scopeID
}

// LoadSet returns the stored set for one scope. found=false means the scope
// has no set of its own and resolution must fall through.
func (r *CouncilRolesRepository) LoadSet(ctx context.Context, ownerID, scopeType, scopeID string) (set CouncilRoleAssignmentSetRecord, slots []CouncilRoleAssignmentRecord, found bool, err error) {
	scopeID = scopeIDFor(scopeType, scopeID)
	err = r.db.WithContext(ctx).Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).First(&set).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return set, nil, false, nil
	}
	if err != nil {
		return set, nil, false, err
	}
	err = r.db.WithContext(ctx).Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).
		Order("role ASC").Order("ordinal ASC").Find(&slots).Error
	if err != nil {
		return set, nil, false, err
	}
	return set, slots, true, nil
}

// ReplaceSet atomically replaces the full slot list of a scope. The caller
// passes the version it read (0 when creating); a mismatch with the stored
// version is ErrCouncilSetConflict. Returns the new version.
func (r *CouncilRolesRepository) ReplaceSet(ctx context.Context, ownerID, scopeType, scopeID string, expectedVersion int, slots []CouncilRoleAssignmentRecord, updatedBy string) (int, error) {
	scopeID = scopeIDFor(scopeType, scopeID)
	newVersion := 0
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current CouncilRoleAssignmentSetRecord
		err := tx.Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expectedVersion != 0 {
				return ErrCouncilSetConflict
			}
		} else if err != nil {
			return err
		} else if current.Version != expectedVersion {
			return ErrCouncilSetConflict
		}
		if err := tx.Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).Delete(&CouncilRoleAssignmentRecord{}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for i := range slots {
			slots[i].OwnerUserID = ownerID
			slots[i].ScopeType = scopeType
			slots[i].ScopeID = scopeID
			slots[i].UpdatedBy = updatedBy
			slots[i].CreatedAt = now
			slots[i].UpdatedAt = now
			if err := tx.Create(&slots[i]).Error; err != nil {
				return err
			}
		}
		newVersion = expectedVersion + 1
		set := CouncilRoleAssignmentSetRecord{OwnerUserID: ownerID, ScopeType: scopeType, ScopeID: scopeID, Version: newVersion, UpdatedBy: updatedBy, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "owner_user_id"}, {Name: "scope_type"}, {Name: "scope_id"}},
			DoUpdates: clause.Assignments(map[string]any{"version": newVersion, "updated_by": updatedBy, "updated_at": now}),
		}).Create(&set).Error
	})
	return newVersion, err
}

// DeleteSet removes a scope's set so resolution falls through again.
func (r *CouncilRolesRepository) DeleteSet(ctx context.Context, ownerID, scopeType, scopeID string) error {
	scopeID = scopeIDFor(scopeType, scopeID)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).Delete(&CouncilRoleAssignmentRecord{}).Error; err != nil {
			return err
		}
		return tx.Where("owner_user_id = ? AND scope_type = ? AND scope_id = ?", ownerID, scopeType, scopeID).Delete(&CouncilRoleAssignmentSetRecord{}).Error
	})
}

// SaveCouncilConfig stores the resolved council configuration snapshot for a
// run. Transport tasks do not otherwise create run rows, so the row is
// upserted with the task's current state; an existing row only gains the
// snapshot.
func (r *CouncilRolesRepository) SaveCouncilConfig(ctx context.Context, runID, state string, configJSON []byte) error {
	now := time.Now().UTC()
	record := RunRecord{ID: runID, State: state, Version: 1, CouncilConfigJSON: configJSON, CreatedAt: now, UpdatedAt: now}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{"council_config_json": configJSON, "updated_at": now}),
	}).Create(&record).Error
}
