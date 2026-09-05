package rbac

import (
	"context"
	"fmt"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var standardPermissions = []string{
	"workspace:read", "workspace:write",
	"task:read", "task:write", "task:approve", "task:execute",
	"admin:users", "admin:roles", "admin:permissions", "admin:*",
}

// SeedPermissions maintains the permission catalog without changing any grants.
// Looking up names separately from creation attributes preserves existing IDs.
func (s *Service) SeedPermissions(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := seedPermissions(tx)
		return err
	})
}

func seedPermissions(tx *gorm.DB) ([]sqlite.PermissionRecord, error) {
	permissions := make([]sqlite.PermissionRecord, 0, len(standardPermissions))
	for _, name := range standardPermissions {
		var permission sqlite.PermissionRecord
		if err := tx.Where("name = ?", name).
			Attrs(sqlite.PermissionRecord{ID: name, Name: name}).FirstOrCreate(&permission).Error; err != nil {
			return nil, fmt.Errorf("seed permission catalog: %w", err)
		}
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

// grantBootstrapPermissions is used only by an explicit administrator bootstrap.
// Additive links preserve any custom permissions or roles already assigned.
func grantBootstrapPermissions(tx *gorm.DB, userID, roleName string) error {
	var role sqlite.RoleRecord
	if err := tx.Where("name = ?", roleName).
		Attrs(sqlite.RoleRecord{ID: roleName, Name: roleName}).FirstOrCreate(&role).Error; err != nil {
		return fmt.Errorf("create bootstrap role: %w", err)
	}
	permissions, err := seedPermissions(tx)
	if err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(
		&sqlite.UserRoleRecord{UserID: userID, RoleID: role.ID},
	).Error; err != nil {
		return fmt.Errorf("assign bootstrap role: %w", err)
	}
	for _, permission := range permissions {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(
			&sqlite.RolePermissionRecord{RoleID: role.ID, PermissionID: permission.ID},
		).Error; err != nil {
			return fmt.Errorf("grant bootstrap permission: %w", err)
		}
	}
	return nil
}

// BootstrapLegacyAdmin preserves an existing user's token and gives the explicit
// bootstrap role the permissions formerly provided by the legacy role gate.
func (s *Service) BootstrapLegacyAdmin(ctx context.Context, subject, token, role string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := sqlite.UserRecord{ID: subject, Subject: subject, TokenHash: hashToken(token)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return fmt.Errorf("create legacy bootstrap user: %w", err)
		}
		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return fmt.Errorf("load legacy bootstrap user: %w", err)
		}
		return grantBootstrapPermissions(tx, user.ID, role)
	})
}
