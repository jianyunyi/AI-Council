package rbac

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

var expectedStandardPermissions = []string{
	"workspace:read", "workspace:write", "task:read", "task:write", "task:approve", "task:execute",
	"admin:users", "admin:roles", "admin:permissions", "admin:*",
}

func TestSeedPermissionsSurvivesRestartWithoutChangingGrants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	db, err := sqlite.Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "existing-permission-id", Name: "task:read"}).Error)
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "custom-id", Name: "custom:read"}).Error)
	s := New(db)
	_, err = s.CreateManagedRole(ctx, "reader", []string{"task:read", "custom:read"})
	require.NoError(t, err)
	_, err = s.CreateManagedUser(ctx, "reader", "reader-password", []string{"reader"})
	require.NoError(t, err)
	beforeRoles, err := s.ListRoles(ctx)
	require.NoError(t, err)
	beforeUsers, err := s.ListUsers(ctx)
	require.NoError(t, err)
	require.NoError(t, s.SeedPermissions(ctx))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	db, err = sqlite.Open(path)
	require.NoError(t, err)
	sqlDB, err = db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	s = New(db)
	require.NoError(t, s.SeedPermissions(ctx))
	permissions, err := s.ListPermissions(ctx)
	require.NoError(t, err)
	names := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		names = append(names, permission.Name)
	}
	require.ElementsMatch(t, append(append([]string{}, expectedStandardPermissions...), "custom:read"), names)
	var existing sqlite.PermissionRecord
	require.NoError(t, db.Where("name = ?", "task:read").First(&existing).Error)
	require.Equal(t, "existing-permission-id", existing.ID)
	afterRoles, err := s.ListRoles(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeRoles, afterRoles)
	afterUsers, err := s.ListUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeUsers, afterUsers)
}

func TestBootstrapAdminGrantsOperationsAndPreservesExistingPermissions(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "bootstrap.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	s := New(db)
	require.NoError(t, db.Create(&sqlite.RoleRecord{ID: "existing-admin-id", Name: "admin"}).Error)
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "existing-permission-id", Name: "workspace:read"}).Error)
	_, err = s.ReplaceRolePermissions(ctx, "admin", []string{"custom:read"})
	require.NoError(t, err)
	_, err = s.BootstrapAdmin(ctx, "owner", "original-password", time.Hour)
	require.NoError(t, err)
	_, err = s.BootstrapAdmin(ctx, "owner", "ignored-password", time.Hour)
	require.NoError(t, err)
	token, identity, err := s.Login(ctx, "owner", "original-password", time.Hour)
	require.NoError(t, err)
	require.ElementsMatch(t, append(append([]string{}, expectedStandardPermissions...), "custom:read"), identity.Permissions)
	for _, permission := range expectedStandardPermissions {
		require.NoError(t, s.AuthorizePermission(ctx, token, permission), permission)
	}
	_, _, err = s.Login(ctx, "owner", "ignored-password", time.Hour)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestBootstrapLegacyAdminPreservesTokenAndExistingIDs(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	s := New(db)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "user-id", Subject: "owner", TokenHash: hashToken("original-token")}).Error)
	require.NoError(t, db.Create(&sqlite.RoleRecord{ID: "role-id", Name: "operator"}).Error)
	require.NoError(t, db.Create(&sqlite.PermissionRecord{ID: "permission-id", Name: "task:read"}).Error)
	_, err = s.ReplaceRolePermissions(ctx, "operator", []string{"custom:read"})
	require.NoError(t, err)
	require.NoError(t, s.BootstrapLegacyAdmin(ctx, "owner", "replacement-token", "operator"))
	require.NoError(t, s.BootstrapLegacyAdmin(ctx, "owner", "another-token", "operator"))
	identity, err := s.Authenticate(ctx, "original-token")
	require.NoError(t, err)
	require.Equal(t, "user-id", identity.UserID)
	require.Equal(t, []string{"operator"}, identity.Roles)
	require.ElementsMatch(t, append(append([]string{}, expectedStandardPermissions...), "custom:read"), identity.Permissions)
	_, err = s.Authenticate(ctx, "replacement-token")
	require.ErrorIs(t, err, ErrUnauthorized)
	var tokens int64
	require.NoError(t, db.Model(&sqlite.AccessTokenRecord{}).Count(&tokens).Error)
	require.Zero(t, tokens)
}
