package rbac

import (
	"context"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
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
