package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PurposeVerify = "verify_email"
	PurposeReset  = "password_reset"
)

var ErrInvalidToken = errors.New("invalid or expired identity token")
var ErrInvalidInput = errors.New("invalid identity input")

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func New(db *gorm.DB) *Service { return &Service{db: db, now: time.Now} }

// RegisterEmail creates an unverified account and returns a one-time verification secret.
// The caller must send it through the configured mail transport and must never log it.
func (s *Service) RegisterEmail(ctx context.Context, email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || len(password) < 12 || len(password) > 1024 {
		return "", ErrInvalidInput
	}
	hash, err := rbac.HashPassword(password)
	if err != nil {
		return "", err
	}
	userID, err := randomSecret(18)
	if err != nil {
		return "", err
	}
	token, err := randomSecret(32)
	if err != nil {
		return "", err
	}
	passwordHash := hash
	user := sqlite.UserRecord{ID: userID, Subject: email, Email: &email, PasswordHash: &passwordHash}
	record, err := newTokenRecord(userID, PurposeVerify, token, s.now().Add(24*time.Hour))
	if err != nil {
		return "", err
	}
	if err := rbac.New(s.db).SeedPermissions(ctx); err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return AssignDefaultMemberRole(tx, user.ID)
	}); err != nil {
		return "", err
	}
	return token, nil
}

func AssignDefaultMemberRole(tx *gorm.DB, userID string) error {
	var role sqlite.RoleRecord
	if err := tx.Where("name = ?", "member").Attrs(sqlite.RoleRecord{ID: "member", Name: "member"}).FirstOrCreate(&role).Error; err != nil {
		return err
	}
	// New self-service members may draft and start tasks, but NOT approve or
	// execute them: approval/execution are privileged and granted explicitly by
	// an administrator. This preserves separation of duties.
	for _, name := range []string{"workspace:read", "workspace:write", "provider:read", "provider:write", "task:read", "task:write"} {
		var permission sqlite.PermissionRecord
		if err := tx.Where("name = ?", name).First(&permission).Error; err != nil {
			return err
		}
		link := sqlite.RolePermissionRecord{RoleID: role.ID, PermissionID: permission.ID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
			return err
		}
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&sqlite.UserRoleRecord{UserID: userID, RoleID: role.ID}).Error
}

func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	return s.consume(ctx, token, PurposeVerify, func(tx *gorm.DB, record sqlite.IdentityTokenRecord, now time.Time) error {
		result := tx.Model(&sqlite.UserRecord{}).Where("id = ? AND email_verified_at IS NULL", record.UserID).Update("email_verified_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		return nil
	})
}

// RequestPasswordReset returns a mail-only secret. A missing email returns an empty
// secret with no error, allowing the HTTP layer to respond identically either way.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var user sqlite.UserRecord
	result := s.db.WithContext(ctx).Where("email = ?", email).Limit(1).Find(&user)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", nil
	}
	token, err := randomSecret(32)
	if err != nil {
		return "", err
	}
	record, err := newTokenRecord(user.ID, PurposeReset, token, s.now().Add(time.Hour))
	if err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND purpose = ? AND consumed_at IS NULL", user.ID, PurposeReset).Delete(&sqlite.IdentityTokenRecord{}).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) CancelUnverifiedRegistration(ctx context.Context, email, token string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user sqlite.UserRecord
		if err := tx.Where("email = ? AND email_verified_at IS NULL", strings.ToLower(strings.TrimSpace(email))).First(&user).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND purpose = ? AND token_hash = ?", user.ID, PurposeVerify, tokenHash(token)).Delete(&sqlite.IdentityTokenRecord{}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	})
}

func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 12 || len(password) > 1024 {
		return ErrInvalidInput
	}
	hash, err := rbac.HashPassword(password)
	if err != nil {
		return err
	}
	return s.consume(ctx, token, PurposeReset, func(tx *gorm.DB, record sqlite.IdentityTokenRecord, now time.Time) error {
		if err := tx.Model(&sqlite.UserRecord{}).Where("id = ?", record.UserID).Update("password_hash", hash).Error; err != nil {
			return err
		}
		return tx.Model(&sqlite.AccessTokenRecord{}).Where("user_id = ? AND revoked_at IS NULL", record.UserID).Update("revoked_at", now).Error
	})
}

func (s *Service) consume(ctx context.Context, token, purpose string, apply func(*gorm.DB, sqlite.IdentityTokenRecord, time.Time) error) error {
	if token == "" || len(token) > 256 {
		return ErrInvalidToken
	}
	hash := tokenHash(token)
	now := s.now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record sqlite.IdentityTokenRecord
		result := tx.Where("token_hash = ? AND purpose = ? AND consumed_at IS NULL AND expires_at > ?", hash, purpose, now).Limit(1).Find(&record)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrInvalidToken
		}
		if err := apply(tx, record, now); err != nil {
			return err
		}
		result = tx.Model(&sqlite.IdentityTokenRecord{}).Where("id = ? AND consumed_at IS NULL", record.ID).Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInvalidToken
	}
	return err
}

func newTokenRecord(userID, purpose, token string, expires time.Time) (sqlite.IdentityTokenRecord, error) {
	id, err := randomSecret(18)
	if err != nil {
		return sqlite.IdentityTokenRecord{}, err
	}
	return sqlite.IdentityTokenRecord{ID: id, UserID: userID, Purpose: purpose, TokenHash: tokenHash(token), ExpiresAt: expires, CreatedAt: time.Now()}, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomSecret(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate identity secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
