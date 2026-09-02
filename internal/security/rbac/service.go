package rbac

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUnauthorized = errors.New("unauthorized")
var ErrForbidden = errors.New("forbidden")

type Identity struct {
	UserID      string
	Subject     string
	Roles       []string
	Permissions []string
}

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

// CreateUser preserves the original static-token user API for hybrid deployments.
func (s *Service) CreateUser(ctx context.Context, subject, token string) error {
	return s.db.WithContext(ctx).Create(&sqlite.UserRecord{
		ID:        subject,
		Subject:   subject,
		TokenHash: hashToken(token),
	}).Error
}

func (s *Service) CreateUserWithPassword(ctx context.Context, subject, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(&sqlite.UserRecord{
		ID:           subject,
		Subject:      subject,
		PasswordHash: &hash,
	}).Error
}

func (s *Service) SetPassword(ctx context.Context, subject, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&sqlite.UserRecord{}).
		Where("subject = ?", subject).
		Update("password_hash", hash)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Service) CreateRole(ctx context.Context, name string) error {
	return s.db.WithContext(ctx).Create(&sqlite.RoleRecord{ID: name, Name: name}).Error
}

func (s *Service) GrantPermission(ctx context.Context, role, permission string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var roleRecord sqlite.RoleRecord
		if err := tx.Where("name = ?", role).First(&roleRecord).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrForbidden
			}
			return err
		}

		var permissionRecord sqlite.PermissionRecord
		if err := tx.Where("name = ?", permission).FirstOrCreate(
			&permissionRecord,
			&sqlite.PermissionRecord{ID: permission, Name: permission},
		).Error; err != nil {
			return err
		}
		return tx.Create(&sqlite.RolePermissionRecord{
			RoleID:       roleRecord.ID,
			PermissionID: permissionRecord.ID,
		}).Error
	})
}

func (s *Service) AssignRole(ctx context.Context, subject, role string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return err
		}
		var roleRecord sqlite.RoleRecord
		if err := tx.Where("name = ?", role).First(&roleRecord).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrForbidden
			}
			return err
		}
		return tx.Create(&sqlite.UserRoleRecord{UserID: user.ID, RoleID: roleRecord.ID}).Error
	})
}

// GrantRole preserves the original API, including creating a missing role.
func (s *Service) GrantRole(ctx context.Context, subject, role string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return err
		}
		var roleRecord sqlite.RoleRecord
		if err := tx.Where("name = ?", role).FirstOrCreate(
			&roleRecord,
			&sqlite.RoleRecord{ID: role, Name: role},
		).Error; err != nil {
			return err
		}
		return tx.FirstOrCreate(&sqlite.UserRoleRecord{
			UserID: user.ID,
			RoleID: roleRecord.ID,
		}).Error
	})
}

func (s *Service) BootstrapAdmin(ctx context.Context, subject, password string, ttl time.Duration) (string, error) {
	var token string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := sqlite.UserRecord{ID: subject, Subject: subject}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
		if result.Error != nil {
			return fmt.Errorf("create bootstrap user: %w", result.Error)
		}
		created := result.RowsAffected == 1

		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return fmt.Errorf("load bootstrap user: %w", err)
		}
		updates := map[string]any{}
		if user.PasswordHash == nil {
			hash, err := HashPassword(password)
			if err != nil {
				return err
			}
			updates["password_hash"] = hash
			user.PasswordHash = &hash
		}
		if user.Disabled {
			updates["disabled"] = false
			user.Disabled = false
		}
		if len(updates) > 0 {
			if err := tx.Model(&sqlite.UserRecord{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
				return fmt.Errorf("repair bootstrap user: %w", err)
			}
		}

		roleSeed := sqlite.RoleRecord{ID: "admin", Name: "admin"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&roleSeed).Error; err != nil {
			return fmt.Errorf("create admin role: %w", err)
		}
		var role sqlite.RoleRecord
		if err := tx.Where("name = ?", "admin").First(&role).Error; err != nil {
			return fmt.Errorf("load admin role: %w", err)
		}
		permissionSeed := sqlite.PermissionRecord{ID: "admin:*", Name: "admin:*"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&permissionSeed).Error; err != nil {
			return fmt.Errorf("create admin permission: %w", err)
		}
		var permission sqlite.PermissionRecord
		if err := tx.Where("name = ?", "admin:*").First(&permission).Error; err != nil {
			return fmt.Errorf("load admin permission: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(
			&sqlite.UserRoleRecord{UserID: user.ID, RoleID: role.ID},
		).Error; err != nil {
			return fmt.Errorf("assign admin role: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(
			&sqlite.RolePermissionRecord{RoleID: role.ID, PermissionID: permission.ID},
		).Error; err != nil {
			return fmt.Errorf("grant admin permission: %w", err)
		}

		if !created {
			return nil
		}
		plain, _, err := (&Service{db: tx}).IssueToken(ctx, user.ID, ttl)
		if err != nil {
			return err
		}
		token = plain
		return nil
	})
	return token, err
}

func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	tokenHash := hashToken(token)
	var record sqlite.AccessTokenRecord
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&record).Error
	if err == nil {
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(tokenHash)) != 1 ||
			record.RevokedAt != nil || !record.ExpiresAt.After(time.Now()) {
			return Identity{}, ErrUnauthorized
		}
		return s.identityForUser(ctx, record.UserID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Identity{}, fmt.Errorf("load access token: %w", err)
	}

	// Legacy users store their long-lived token directly on the user record.
	var user sqlite.UserRecord
	err = s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Identity{}, ErrUnauthorized
		}
		return Identity{}, fmt.Errorf("load legacy user: %w", err)
	}
	if user.Disabled || subtle.ConstantTimeCompare([]byte(user.TokenHash), []byte(tokenHash)) != 1 {
		return Identity{}, ErrUnauthorized
	}
	return s.identityForUser(ctx, user.ID)
}

func (s *Service) AuthorizePermission(ctx context.Context, token, permission string) error {
	identity, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	for _, granted := range identity.Permissions {
		if granted == permission {
			return nil
		}
	}
	return ErrForbidden
}

func (s *Service) Authorize(ctx context.Context, token, role string) error {
	identity, err := s.Authenticate(ctx, token)
	if err != nil {
		return err
	}
	for _, assigned := range identity.Roles {
		if assigned == role {
			return nil
		}
	}
	return ErrForbidden
}

func (s *Service) identityForUser(ctx context.Context, userID string) (Identity, error) {
	var user sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Identity{}, ErrUnauthorized
		}
		return Identity{}, fmt.Errorf("load identity user: %w", err)
	}
	if user.Disabled {
		return Identity{}, ErrUnauthorized
	}

	identity := Identity{UserID: user.ID, Subject: user.Subject, Roles: []string{}, Permissions: []string{}}
	if err := s.db.WithContext(ctx).Model(&sqlite.RoleRecord{}).
		Distinct("role_records.name").
		Joins("JOIN user_role_records ON user_role_records.role_id = role_records.id").
		Where("user_role_records.user_id = ?", user.ID).
		Order("role_records.name").
		Pluck("role_records.name", &identity.Roles).Error; err != nil {
		return Identity{}, fmt.Errorf("load identity roles: %w", err)
	}
	if err := s.db.WithContext(ctx).Model(&sqlite.PermissionRecord{}).
		Distinct("permission_records.name").
		Joins("JOIN role_permission_records ON role_permission_records.permission_id = permission_records.id").
		Joins("JOIN user_role_records ON user_role_records.role_id = role_permission_records.role_id").
		Where("user_role_records.user_id = ?", user.ID).
		Order("permission_records.name").
		Pluck("permission_records.name", &identity.Permissions).Error; err != nil {
		return Identity{}, fmt.Errorf("load identity permissions: %w", err)
	}
	return identity, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
