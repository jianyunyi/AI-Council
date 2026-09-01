package rbac

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

var ErrUnauthorized = errors.New("unauthorized")
var ErrForbidden = errors.New("forbidden")

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }
func (s *Service) CreateUser(ctx context.Context, subject, token string) error {
	sum := sha256.Sum256([]byte(token))
	return s.db.WithContext(ctx).Create(&sqlite.UserRecord{ID: subject, Subject: subject, TokenHash: hex.EncodeToString(sum[:])}).Error
}
func (s *Service) GrantRole(ctx context.Context, subject, role string) error {
	var u sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("subject = ?", subject).First(&u).Error; err != nil {
		return err
	}
	var r sqlite.RoleRecord
	if err := s.db.WithContext(ctx).Where("name = ?", role).First(&r).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		r = sqlite.RoleRecord{ID: role, Name: role}
		if err = s.db.WithContext(ctx).Create(&r).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Save(&sqlite.UserRoleRecord{UserID: u.ID, RoleID: r.ID}).Error
}
func (s *Service) Authorize(ctx context.Context, token, role string) error {
	sum := sha256.Sum256([]byte(token))
	var u sqlite.UserRecord
	if err := s.db.WithContext(ctx).Where("token_hash = ? AND disabled = ?", hex.EncodeToString(sum[:]), false).First(&u).Error; err != nil {
		return ErrUnauthorized
	}
	var r sqlite.RoleRecord
	if err := s.db.WithContext(ctx).Where("name = ?", role).First(&r).Error; err != nil {
		return ErrForbidden
	}
	var link sqlite.UserRoleRecord
	if err := s.db.WithContext(ctx).Where("user_id = ? AND role_id = ?", u.ID, r.ID).First(&link).Error; err != nil {
		return ErrForbidden
	}
	return nil
}
