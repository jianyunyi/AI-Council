package providerkeys

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestProviderKeysAreEncryptedMaskedAndOwnerScoped(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	service, err := New(db, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	profile, err := service.Create(context.Background(), "user-a", "openai", "gpt-test", "sk-live-super-secret")
	require.NoError(t, err)
	require.Equal(t, "••••cret", profile.MaskedKey)
	require.NotContains(t, profile.MaskedKey, "super-secret")
	var stored sqlite.ProviderProfileRecord
	require.NoError(t, db.First(&stored, "id = ?", profile.ID).Error)
	require.NotContains(t, string(stored.APIKeyCiphertext), "sk-live-super-secret")
	secret, err := service.Secret(context.Background(), "user-a", profile.ID)
	require.NoError(t, err)
	require.Equal(t, "sk-live-super-secret", secret)
	_, err = service.Secret(context.Background(), "user-b", profile.ID)
	require.ErrorIs(t, err, ErrNotFound)
	profiles, err := service.List(context.Background(), "user-b")
	require.NoError(t, err)
	require.Empty(t, profiles)
	require.ErrorIs(t, service.Delete(context.Background(), "user-b", profile.ID), ErrNotFound)
	require.NoError(t, service.Delete(context.Background(), "user-a", profile.ID))
}

func TestProviderKeyRejectsInvalidKeyOrProvider(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = New(db, []byte("short"))
	require.ErrorIs(t, err, ErrInvalidMasterKey)
	service, err := New(db, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	_, err = service.Create(context.Background(), "user-a", "other", "model", "api-key-long-enough")
	require.ErrorIs(t, err, ErrInvalidProfile)
}
