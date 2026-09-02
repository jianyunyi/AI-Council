package rbac

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

const tokenBytes = 32

func (s *Service) IssueToken(ctx context.Context, userID string, ttl time.Duration) (plain string, record sqlite.AccessTokenRecord, err error) {
	var user sqlite.UserRecord
	if err := s.db.WithContext(ctx).
		Where("id = ? OR subject = ?", userID, userID).
		First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", sqlite.AccessTokenRecord{}, ErrUnauthorized
		}
		return "", sqlite.AccessTokenRecord{}, fmt.Errorf("load token user: %w", err)
	}
	if user.Disabled {
		return "", sqlite.AccessTokenRecord{}, ErrUnauthorized
	}

	plain, err = randomToken(tokenBytes)
	if err != nil {
		return "", sqlite.AccessTokenRecord{}, err
	}
	id, err := randomToken(tokenBytes / 2)
	if err != nil {
		return "", sqlite.AccessTokenRecord{}, err
	}
	now := time.Now()
	record = sqlite.AccessTokenRecord{
		ID:        id,
		UserID:    user.ID,
		TokenHash: hashToken(plain),
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return "", sqlite.AccessTokenRecord{}, fmt.Errorf("store access token: %w", err)
	}
	return plain, record, nil
}

func (s *Service) RevokeToken(ctx context.Context, token string) error {
	tokenHash := hashToken(token)
	var record sqlite.AccessTokenRecord
	if err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUnauthorized
		}
		return err
	}
	if record.RevokedAt != nil {
		return nil
	}
	now := time.Now()
	return s.db.WithContext(ctx).Model(&record).Update("revoked_at", &now).Error
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
