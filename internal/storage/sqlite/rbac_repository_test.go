package sqlite

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRBACRecordsPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rbac.sqlite")
	db, err := Open(path)
	require.NoError(t, err)
	passwordHash := "argon2id-hash"
	now := time.Now().UTC().Truncate(time.Second)
	revokedAt := now.Add(time.Minute)

	require.NoError(t, db.Create(&UserRecord{ID: "u1", Subject: "alice", PasswordHash: &passwordHash}).Error)
	require.NoError(t, db.Create(&RoleRecord{ID: "r1", Name: "operator"}).Error)
	require.NoError(t, db.Create(&PermissionRecord{ID: "p1", Name: "task:read"}).Error)
	require.NoError(t, db.Create(&UserRoleRecord{UserID: "u1", RoleID: "r1"}).Error)
	require.NoError(t, db.Create(&RolePermissionRecord{RoleID: "r1", PermissionID: "p1"}).Error)
	require.NoError(t, db.Create(&AccessTokenRecord{
		ID: "t1", UserID: "u1", TokenHash: "hash", ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt, CreatedAt: now,
	}).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	db, err = Open(path)
	require.NoError(t, err)
	sqlDB, err = db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var user UserRecord
	require.NoError(t, db.Where("subject = ?", "alice").First(&user).Error)
	require.Equal(t, passwordHash, *user.PasswordHash)
	var link RolePermissionRecord
	require.NoError(t, db.Where("role_id = ? AND permission_id = ?", "r1", "p1").First(&link).Error)
	var token AccessTokenRecord
	require.NoError(t, db.Where("token_hash = ?", "hash").First(&token).Error)
	require.NotNil(t, token.RevokedAt)
	require.WithinDuration(t, revokedAt, *token.RevokedAt, time.Second)
	require.WithinDuration(t, now.Add(time.Hour), token.ExpiresAt, time.Second)
}

func TestRBACUniqueConstraints(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "rbac.sqlite"))
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Create(&UserRecord{ID: "u1", Subject: "alice"}).Error)
	require.Error(t, db.Create(&UserRecord{ID: "u2", Subject: "alice"}).Error)
	require.NoError(t, db.Create(&RolePermissionRecord{RoleID: "r1", PermissionID: "p1"}).Error)
	require.Error(t, db.Create(&RolePermissionRecord{RoleID: "r1", PermissionID: "p1"}).Error)
	require.NoError(t, db.Create(&AccessTokenRecord{ID: "t1", UserID: "u1", TokenHash: "hash-1"}).Error)
	require.Error(t, db.Create(&AccessTokenRecord{ID: "t2", UserID: "u1", TokenHash: "hash-1"}).Error)
}
