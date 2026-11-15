package workspaceaccess

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSharesGrantUpdateAndRevokeEffectiveAccess(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "access.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "ws", OwnerUserID: "owner", CanonicalRoot: "/workspace/repo"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "editor-id", Subject: "editor"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "viewer-id", Subject: "viewer"}).Error)
	service := New(db)

	role, err := service.EffectiveAccess(context.Background(), "ws", "owner")
	require.NoError(t, err)
	require.Equal(t, Editor, role)
	role, err = service.EffectiveAccess(context.Background(), "ws", "editor-id")
	require.NoError(t, err)
	require.Equal(t, AccessNone, role)

	require.NoError(t, service.Grant(context.Background(), "ws", "owner", "editor", Editor))
	require.NoError(t, service.Grant(context.Background(), "ws", "owner", "viewer", Viewer))
	role, err = service.EffectiveAccess(context.Background(), "ws", "editor-id")
	require.NoError(t, err)
	require.Equal(t, Editor, role)
	role, err = service.EffectiveAccess(context.Background(), "ws", "viewer-id")
	require.NoError(t, err)
	require.Equal(t, Viewer, role)
	require.NoError(t, service.Grant(context.Background(), "ws", "owner", "editor", Viewer))
	role, err = service.EffectiveAccess(context.Background(), "ws", "editor-id")
	require.NoError(t, err)
	require.Equal(t, Viewer, role)
	require.NoError(t, service.Revoke(context.Background(), "ws", "owner", "editor"))
	role, err = service.EffectiveAccess(context.Background(), "ws", "editor-id")
	require.NoError(t, err)
	require.Equal(t, AccessNone, role)
}

func TestWorkspaceWithoutOwnerSupportsStaticSingleUserMode(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "access.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "legacy", CanonicalRoot: "/workspace/repo"}).Error)

	access, err := New(db).EffectiveAccess(context.Background(), "legacy", "")
	require.NoError(t, err)
	require.Equal(t, Editor, access)
	access, err = New(db).EffectiveAccess(context.Background(), "legacy", "authenticated-user")
	require.NoError(t, err)
	require.Equal(t, AccessNone, access)
}

func TestWorkspaceShareRequiresOwnerActiveMemberAndValidLevel(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "access.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "ws", OwnerUserID: "owner"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "owner", Subject: "owner"}).Error)
	require.NoError(t, db.Create(&sqlite.UserRecord{ID: "inactive", Subject: "inactive", Disabled: true}).Error)
	service := New(db)
	require.ErrorIs(t, service.Grant(context.Background(), "ws", "intruder", "missing", Viewer), ErrForbidden)
	require.ErrorIs(t, service.Grant(context.Background(), "ws", "owner", "missing", Viewer), ErrTargetUserNotFound)
	require.ErrorIs(t, service.Grant(context.Background(), "ws", "owner", "inactive", Viewer), ErrTargetUserNotFound)
	require.ErrorIs(t, service.Grant(context.Background(), "ws", "owner", "owner", Viewer), ErrInvalidShare)
	require.ErrorIs(t, service.Grant(context.Background(), "ws", "owner", "inactive", "admin"), ErrInvalidShare)
}
