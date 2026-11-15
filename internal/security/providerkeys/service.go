package providerkeys

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"gorm.io/gorm"
)

var ErrInvalidMasterKey = errors.New("provider secret master key must be 32 bytes")
var ErrInvalidProfile = errors.New("invalid provider profile")
var ErrNotFound = errors.New("provider profile not found")
var ErrCorruptSecret = errors.New("provider secret ciphertext is invalid")

const ciphertextVersion = "PK1"

type Profile struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	MaskedKey string    `json:"masked_key"`
	HasKey    bool      `json:"has_key"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	db   *gorm.DB
	aead cipher.AEAD
}

func New(db *gorm.DB, masterKey []byte) (*Service, error) {
	if len(masterKey) != 32 {
		return nil, ErrInvalidMasterKey
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, ErrInvalidMasterKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, aead: aead}, nil
}

func (s *Service) Create(ctx context.Context, ownerID, provider, model, apiKey string) (Profile, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	if ownerID == "" || !supportedProvider(provider) || model == "" || len(model) > 128 || strings.TrimSpace(apiKey) == "" || len(apiKey) > 4096 || !utf8.ValidString(apiKey) {
		return Profile{}, ErrInvalidProfile
	}
	id, err := randomID()
	if err != nil {
		return Profile{}, err
	}
	metadata, err := json.Marshal([]string{ownerID, id, provider, model})
	if err != nil {
		return Profile{}, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Profile{}, err
	}
	ciphertext := append([]byte(ciphertextVersion), nonce...)
	ciphertext = s.aead.Seal(ciphertext, nonce, []byte(apiKey), metadata)
	record := sqlite.ProviderProfileRecord{ID: id, OwnerUserID: ownerID, Provider: provider, Model: model, APIKeyCiphertext: ciphertext, CreatedAt: time.Now().UTC()}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return Profile{}, fmt.Errorf("save provider profile: %w", err)
	}
	return publicProfile(record, apiKey), nil
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Profile, error) {
	if ownerID == "" {
		return nil, ErrInvalidProfile
	}
	var records []sqlite.ProviderProfileRecord
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", ownerID).Order("created_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(records))
	for _, record := range records {
		profiles = append(profiles, publicProfile(record, ""))
	}
	return profiles, nil
}

func (s *Service) Secret(ctx context.Context, ownerID, id string) (string, error) {
	if ownerID == "" || id == "" {
		return "", ErrNotFound
	}
	var record sqlite.ProviderProfileRecord
	result := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerID).Limit(1).Find(&record)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrNotFound
	}
	return s.decrypt(record)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	if ownerID == "" || id == "" {
		return ErrNotFound
	}
	result := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerID).Delete(&sqlite.ProviderProfileRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) decrypt(record sqlite.ProviderProfileRecord) (string, error) {
	if len(record.APIKeyCiphertext) < len(ciphertextVersion)+s.aead.NonceSize()+s.aead.Overhead() || string(record.APIKeyCiphertext[:len(ciphertextVersion)]) != ciphertextVersion {
		return "", ErrCorruptSecret
	}
	nonceStart := len(ciphertextVersion)
	nonce := record.APIKeyCiphertext[nonceStart : nonceStart+s.aead.NonceSize()]
	metadata, err := json.Marshal([]string{record.OwnerUserID, record.ID, record.Provider, record.Model})
	if err != nil {
		return "", ErrCorruptSecret
	}
	plain, err := s.aead.Open(nil, nonce, record.APIKeyCiphertext[nonceStart+s.aead.NonceSize():], metadata)
	if err != nil {
		return "", ErrCorruptSecret
	}
	return string(plain), nil
}

func supportedProvider(provider string) bool {
	switch provider {
	case "openai", "deepseek", "anthropic":
		return true
	default:
		return false
	}
}

func publicProfile(record sqlite.ProviderProfileRecord, key string) Profile {
	return Profile{ID: record.ID, Provider: record.Provider, Model: record.Model, MaskedKey: mask(key), HasKey: len(record.APIKeyCiphertext) > 0, CreatedAt: record.CreatedAt}
}

func mask(key string) string {
	if key == "" {
		return "configured"
	}
	runes := []rune(key)
	if len(runes) <= 4 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}

func randomID() (string, error) {
	raw := make([]byte, 18)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
