package desktop

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	secretMasterKeyFile = "master.key"
	secretFileSuffix    = ".secret"
	secretFormatVersion = byte(1)
)

var ErrSecretNotFound = errors.New("secret not found")

type SecretNotFoundError struct {
	Key string
}

func (e *SecretNotFoundError) Error() string {
	return fmt.Sprintf("secret %q not found", e.Key)
}

func (e *SecretNotFoundError) Unwrap() error {
	return ErrSecretNotFound
}

// SecretStore persists provider secrets encrypted with an installation-local
// AES-256-GCM key. The key and encrypted values are restricted to the owner.
type SecretStore struct {
	dir  string
	aead cipher.AEAD
	mu   sync.RWMutex
}

func NewSecretStore(dir string) (*SecretStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("secret store directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret store directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure secret store directory: %w", err)
	}

	key, err := loadOrCreateMasterKey(filepath.Join(dir, secretMasterKeyFile))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize authenticated encryption: %w", err)
	}
	return &SecretStore{dir: dir, aead: aead}, nil
}

func (s *SecretStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate secret nonce: %w", err)
	}
	contents := make([]byte, 1, 1+len(nonce)+len(value)+s.aead.Overhead())
	contents[0] = secretFormatVersion
	contents = append(contents, nonce...)
	contents = s.aead.Seal(contents, nonce, value, []byte(key))

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write encrypted secret %q: %w", key, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure encrypted secret %q: %w", key, err)
	}
	return nil
}

func (s *SecretStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	contents, err := os.ReadFile(path)
	s.mu.RUnlock()
	if errors.Is(err, os.ErrNotExist) {
		return nil, &SecretNotFoundError{Key: key}
	}
	if err != nil {
		return nil, fmt.Errorf("read encrypted secret %q: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(contents) < 1+s.aead.NonceSize()+s.aead.Overhead() || contents[0] != secretFormatVersion {
		return nil, fmt.Errorf("encrypted secret %q has an invalid format", key)
	}
	nonceEnd := 1 + s.aead.NonceSize()
	plaintext, err := s.aead.Open(nil, contents[1:nonceEnd], contents[nonceEnd:], []byte(key))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret %q: %w", key, err)
	}
	return plaintext, nil
}

func (s *SecretStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return &SecretNotFoundError{Key: key}
	}
	if err != nil {
		return fmt.Errorf("delete encrypted secret %q: %w", key, err)
	}
	return nil
}

func (s *SecretStore) pathForKey(key string) (string, error) {
	if !validSecretKey(key) {
		return "", fmt.Errorf("invalid secret key %q", key)
	}
	return filepath.Join(s.dir, key+secretFileSuffix), nil
}

func validSecretKey(key string) bool {
	if key == "" || len(key) > 255 || key == "." || key == ".." {
		return false
	}
	for _, character := range key {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}
