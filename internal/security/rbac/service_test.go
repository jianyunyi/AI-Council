package rbac

import (
	"context"
	"errors"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
)

func TestRBACAuthorizeByRole(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()
	require.NoError(t, s.CreateUser(ctx, "u1", "secret"))
	require.NoError(t, s.GrantRole(ctx, "u1", "operator"))
	require.NoError(t, s.Authorize(ctx, "secret", "operator"))
	require.ErrorIs(t, s.Authorize(ctx, "secret", "admin"), ErrForbidden)
	require.ErrorIs(t, s.Authorize(ctx, "bad", "operator"), ErrUnauthorized)
}

func TestPasswordHashAndVerification(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	require.NotContains(t, hash, "correct horse battery staple")
	require.NoError(t, VerifyPassword(hash, "correct horse battery staple"))
	require.Error(t, VerifyPassword(hash, "wrong password"))
	require.Error(t, VerifyPassword("not-an-argon2id-hash", "correct horse battery staple"))
}

func TestRBACPermissionAndTokenLifecycle(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.NoError(t, s.CreateRole(ctx, "operator"))
	require.NoError(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.NoError(t, s.AssignRole(ctx, "alice", "operator"))

	plain, record, err := s.IssueToken(ctx, "alice", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, plain)
	require.NotEmpty(t, record.TokenHash)
	require.NotContains(t, record.TokenHash, plain)

	identity, err := s.Authenticate(ctx, plain)
	require.NoError(t, err)
	require.Equal(t, "alice", identity.Subject)
	require.Contains(t, identity.Roles, "operator")
	require.Contains(t, identity.Permissions, "task:read")
	require.NoError(t, s.AuthorizePermission(ctx, plain, "task:read"))
	require.ErrorIs(t, s.AuthorizePermission(ctx, plain, "task:write"), ErrForbidden)

	require.NoError(t, s.RevokeToken(ctx, plain))
	_, err = s.Authenticate(ctx, plain)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestTokenRejectsDisabledAndExpiredUsers(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	expired, _, err := s.IssueToken(ctx, "alice", -time.Second)
	require.NoError(t, err)
	_, err = s.Authenticate(ctx, expired)
	require.ErrorIs(t, err, ErrUnauthorized)

	active, _, err := s.IssueToken(ctx, "alice", time.Hour)
	require.NoError(t, err)
	require.NoError(t, db.Model(&sqlite.UserRecord{}).Where("subject = ?", "alice").Update("disabled", true).Error)
	_, err = s.Authenticate(ctx, active)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestBootstrapAdminIsIdempotent(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	token, err := s.BootstrapAdmin(ctx, "admin", "password", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NoError(t, s.AuthorizePermission(ctx, token, "admin:*"))

	token, err = s.BootstrapAdmin(ctx, "admin", "different", time.Hour)
	require.NoError(t, err)
	require.Empty(t, token)

	var users int64
	require.NoError(t, db.Model(&sqlite.UserRecord{}).Where("subject = ?", "admin").Count(&users).Error)
	require.EqualValues(t, 1, users)
}

func TestSetPassword(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "old"))
	require.NoError(t, s.SetPassword(ctx, "alice", "new"))
	var user sqlite.UserRecord
	require.NoError(t, db.Where("subject = ?", "alice").First(&user).Error)
	require.NotNil(t, user.PasswordHash)
	require.NoError(t, VerifyPassword(*user.PasswordHash, "new"))
	require.Error(t, VerifyPassword(*user.PasswordHash, "old"))
	require.Error(t, s.SetPassword(ctx, "missing", "new"))
}

func TestDuplicateRBACLinksAreRejected(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.Error(t, s.CreateUserWithPassword(ctx, "alice", "password"))
	require.NoError(t, s.CreateRole(ctx, "operator"))
	require.NoError(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.Error(t, s.GrantPermission(ctx, "operator", "task:read"))
	require.NoError(t, s.AssignRole(ctx, "alice", "operator"))
	require.Error(t, s.AssignRole(ctx, "alice", "operator"))
}

func TestAuthenticateUnknownToken(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = New(db).Authenticate(context.Background(), "unknown")
	require.True(t, errors.Is(err, ErrUnauthorized))
}
