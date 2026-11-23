package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/security/rbac"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func testService(t *testing.T) (*Service, *rbac.Service) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "identity.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return New(db), rbac.New(db)
}

func TestRegisterVerifyEmailAndPasswordResetRevokeSessions(t *testing.T) {
	ctx := context.Background()
	identity, auth := testService(t)
	verify, err := identity.RegisterEmail(ctx, "Alice@example.com", "correct horse battery staple")
	require.NoError(t, err)
	_, _, err = auth.Login(ctx, "alice@example.com", "correct horse battery staple", time.Hour)
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
	require.NoError(t, identity.VerifyEmail(ctx, verify))
	require.ErrorIs(t, identity.VerifyEmail(ctx, verify), ErrInvalidToken)
	session, _, err := auth.Login(ctx, "alice@example.com", "correct horse battery staple", time.Hour)
	require.NoError(t, err)
	reset, err := identity.RequestPasswordReset(ctx, "ALICE@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, reset)
	require.NoError(t, identity.ResetPassword(ctx, reset, "a different long password"))
	require.ErrorIs(t, identity.ResetPassword(ctx, reset, "a third long password"), ErrInvalidToken)
	_, err = auth.Authenticate(ctx, session)
	require.ErrorIs(t, err, rbac.ErrUnauthorized)
	_, account, err := auth.Login(ctx, "alice@example.com", "a different long password", time.Hour)
	require.NoError(t, err)
	require.Contains(t, account.Roles, "member")
	require.Contains(t, account.Permissions, "task:read")
	require.Contains(t, account.Permissions, "task:write")
	require.NotContains(t, account.Permissions, "task:approve")
	require.NotContains(t, account.Permissions, "task:execute")
	require.NotContains(t, account.Permissions, "admin:users")
}

func TestResetRequestDoesNotRevealMissingEmail(t *testing.T) {
	ctx := context.Background()
	identity, _ := testService(t)
	token, err := identity.RequestPasswordReset(ctx, "nobody@example.com")
	require.NoError(t, err)
	require.Empty(t, token)
	_, err = identity.RegisterEmail(ctx, "bad", "correct horse battery staple")
	require.ErrorIs(t, err, ErrInvalidInput)
	_, err = identity.RegisterEmail(ctx, "valid@example.com", "short")
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestExpiredVerificationTokenRejected(t *testing.T) {
	ctx := context.Background()
	identity, _ := testService(t)
	now := time.Now()
	identity.now = func() time.Time { return now }
	token, err := identity.RegisterEmail(ctx, "expired@example.com", "correct horse battery staple")
	require.NoError(t, err)
	identity.now = func() time.Time { return now.Add(25 * time.Hour) }
	require.True(t, errors.Is(identity.VerifyEmail(ctx, token), ErrInvalidToken))
}
