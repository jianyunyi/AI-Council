package rbac

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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
	ExpiresAt   *time.Time
}

// User is the safe public representation of a managed user.
type User struct {
	Subject  string
	Disabled bool
	Roles    []string
}

// Role is the safe public representation of a role and its permissions.
type Role struct {
	Name        string
	Permissions []string
}

// Permission is the safe public representation of a permission.
type Permission struct{ Name string }

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

// Login authenticates a password user and issues a revocable access token.
func (s *Service) Login(ctx context.Context, subject, password string, ttl time.Duration) (string, Identity, error) {
	var user sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("subject = ?", subject).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", Identity{}, ErrUnauthorized
		}
		return "", Identity{}, fmt.Errorf("load login user: %w", err)
	}
	if user.Disabled || user.PasswordHash == nil || VerifyPassword(*user.PasswordHash, password) != nil {
		return "", Identity{}, ErrUnauthorized
	}
	token, record, err := s.issueTokenForUser(ctx, user, ttl)
	if err != nil {
		return "", Identity{}, err
	}
	identity, err := s.identityForUser(ctx, user.ID)
	if err != nil {
		return "", Identity{}, err
	}
	identity.ExpiresAt = &record.ExpiresAt
	return token, identity, nil
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

		if err := grantBootstrapPermissions(tx, user.ID, "admin"); err != nil {
			return err
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
		identity, err := s.identityForUser(ctx, record.UserID)
		if err != nil {
			return Identity{}, err
		}
		identity.ExpiresAt = &record.ExpiresAt
		return identity, nil
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
		if granted == permission || (strings.HasSuffix(granted, ":*") && strings.HasPrefix(permission, strings.TrimSuffix(granted, "*"))) {
			return nil
		}
	}
	return ErrForbidden
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	var records []sqlite.UserRecord
	if err := s.db.WithContext(ctx).Order("subject").Find(&records).Error; err != nil {
		return nil, err
	}
	users := make([]User, 0, len(records))
	for _, record := range records {
		roles, err := s.rolesForUser(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		users = append(users, User{Subject: record.Subject, Disabled: record.Disabled, Roles: roles})
	}
	return users, nil
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	var records []sqlite.RoleRecord
	if err := s.db.WithContext(ctx).Order("name").Find(&records).Error; err != nil {
		return nil, err
	}
	roles := make([]Role, 0, len(records))
	for _, record := range records {
		permissions, err := s.permissionsForRole(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		roles = append(roles, Role{Name: record.Name, Permissions: permissions})
	}
	return roles, nil
}

func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
	var records []sqlite.PermissionRecord
	if err := s.db.WithContext(ctx).Order("name").Find(&records).Error; err != nil {
		return nil, err
	}
	permissions := make([]Permission, 0, len(records))
	for _, record := range records {
		permissions = append(permissions, Permission{Name: record.Name})
	}
	return permissions, nil
}

func (s *Service) CreateManagedUser(ctx context.Context, subject, password string, roles []string) (User, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		hash, err := HashPassword(password)
		if err != nil {
			return err
		}
		user := sqlite.UserRecord{ID: subject, Subject: subject, PasswordHash: &hash}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return replaceUserRoles(tx, user.ID, roles)
	})
	if err != nil {
		return User{}, err
	}
	return s.userBySubject(ctx, subject)
}

func (s *Service) UpdateUser(ctx context.Context, subject, password string, roles []string, disabled bool) (User, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return err
		}
		if password != "" {
			hash, err := HashPassword(password)
			if err != nil {
				return err
			}
			if err := tx.Model(&user).Update("password_hash", hash).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&user).Update("disabled", disabled).Error; err != nil {
			return err
		}
		return replaceUserRoles(tx, user.ID, roles)
	})
	if err != nil {
		return User{}, err
	}
	return s.userBySubject(ctx, subject)
}

// PatchUser updates only fields explicitly supplied by the management API.
func (s *Service) PatchUser(ctx context.Context, subject string, password *string, roles *[]string, disabled *bool) (User, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user sqlite.UserRecord
		if err := tx.Where("subject = ?", subject).First(&user).Error; err != nil {
			return err
		}
		if password != nil {
			if *password == "" {
				return ErrInvalidPassword
			}
			hash, err := HashPassword(*password)
			if err != nil {
				return err
			}
			if err := tx.Model(&user).Update("password_hash", hash).Error; err != nil {
				return err
			}
		}
		if disabled != nil {
			if err := tx.Model(&user).Update("disabled", *disabled).Error; err != nil {
				return err
			}
		}
		if roles != nil {
			return replaceUserRoles(tx, user.ID, *roles)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return s.userBySubject(ctx, subject)
}

// RevokePresentedToken revokes a current access token without evaluating its
// user's enabled state. The boolean reports whether a revocable token existed.
func (s *Service) RevokePresentedToken(ctx context.Context, token string) (bool, error) {
	var record sqlite.AccessTokenRecord
	if err := s.db.WithContext(ctx).Where("token_hash = ?", hashToken(token)).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if record.RevokedAt != nil || !record.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&record).Update("revoked_at", &now).Error; err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) CreateManagedRole(ctx context.Context, name string, permissions []string) (Role, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&sqlite.RoleRecord{ID: name, Name: name}).Error; err != nil {
			return err
		}
		return replaceRolePermissions(tx, name, permissions)
	})
	if err != nil {
		return Role{}, err
	}
	roles, err := s.ListRoles(ctx)
	if err != nil {
		return Role{}, err
	}
	for _, role := range roles {
		if role.Name == name {
			return role, nil
		}
	}
	return Role{}, gorm.ErrRecordNotFound
}

func (s *Service) ReplaceRolePermissions(ctx context.Context, name string, permissions []string) (Role, error) {
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return replaceRolePermissions(tx, name, permissions) }); err != nil {
		return Role{}, err
	}
	roles, err := s.ListRoles(ctx)
	if err != nil {
		return Role{}, err
	}
	for _, role := range roles {
		if role.Name == name {
			return role, nil
		}
	}
	return Role{}, gorm.ErrRecordNotFound
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

func (s *Service) userBySubject(ctx context.Context, subject string) (User, error) {
	var record sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("subject = ?", subject).First(&record).Error; err != nil {
		return User{}, err
	}
	roles, err := s.rolesForUser(ctx, record.ID)
	if err != nil {
		return User{}, err
	}
	return User{Subject: record.Subject, Disabled: record.Disabled, Roles: roles}, nil
}

func (s *Service) rolesForUser(ctx context.Context, userID string) ([]string, error) {
	roles := []string{}
	err := s.db.WithContext(ctx).Model(&sqlite.RoleRecord{}).Distinct("role_records.name").
		Joins("JOIN user_role_records ON user_role_records.role_id = role_records.id").
		Where("user_role_records.user_id = ?", userID).Order("role_records.name").Pluck("role_records.name", &roles).Error
	return roles, err
}

func (s *Service) permissionsForRole(ctx context.Context, roleID string) ([]string, error) {
	permissions := []string{}
	err := s.db.WithContext(ctx).Model(&sqlite.PermissionRecord{}).Distinct("permission_records.name").
		Joins("JOIN role_permission_records ON role_permission_records.permission_id = permission_records.id").
		Where("role_permission_records.role_id = ?", roleID).Order("permission_records.name").Pluck("permission_records.name", &permissions).Error
	return permissions, err
}

func replaceUserRoles(tx *gorm.DB, userID string, roleNames []string) error {
	roles := make([]sqlite.RoleRecord, 0, len(roleNames))
	for _, name := range roleNames {
		var role sqlite.RoleRecord
		if err := tx.Where("name = ?", name).First(&role).Error; err != nil {
			return err
		}
		roles = append(roles, role)
	}
	if err := tx.Where("user_id = ?", userID).Delete(&sqlite.UserRoleRecord{}).Error; err != nil {
		return err
	}
	for _, role := range roles {
		if err := tx.Create(&sqlite.UserRoleRecord{UserID: userID, RoleID: role.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func replaceRolePermissions(tx *gorm.DB, roleName string, permissionNames []string) error {
	var role sqlite.RoleRecord
	if err := tx.Where("name = ?", roleName).First(&role).Error; err != nil {
		return err
	}
	permissions := make([]sqlite.PermissionRecord, 0, len(permissionNames))
	for _, name := range permissionNames {
		var permission sqlite.PermissionRecord
		if err := tx.Where("name = ?", name).Attrs(sqlite.PermissionRecord{ID: name, Name: name}).FirstOrCreate(&permission).Error; err != nil {
			return err
		}
		permissions = append(permissions, permission)
	}
	if err := tx.Where("role_id = ?", role.ID).Delete(&sqlite.RolePermissionRecord{}).Error; err != nil {
		return err
	}
	for _, permission := range permissions {
		if err := tx.Create(&sqlite.RolePermissionRecord{RoleID: role.ID, PermissionID: permission.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
