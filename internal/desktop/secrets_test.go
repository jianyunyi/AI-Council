package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretStorePutGetDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSecretStore(dir)
	if err != nil {
		t.Fatalf("NewSecretStore() error = %v", err)
	}
	ctx := context.Background()
	want := []byte("sk-test-plaintext-value")
	if err := store.Put(ctx, "openai", want); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, err := store.Get(ctx, "openai")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get() = %q, want %q", got, want)
	}
	if err := store.Delete(ctx, "openai"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	_, err = store.Get(ctx, "openai")
	var missing *SecretNotFoundError
	if !errors.As(err, &missing) || missing.Key != "openai" || !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get() error = %v, want typed missing-key error", err)
	}
}

func TestSecretStoreDoesNotPersistPlaintext(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSecretStore(dir)
	if err != nil {
		t.Fatalf("NewSecretStore() error = %v", err)
	}
	secret := []byte("unique-provider-secret-that-must-not-leak")
	if err := store.Put(context.Background(), "anthropic", secret); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(contents, secret) {
			t.Errorf("plaintext secret found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect secret files: %v", err)
	}
}
