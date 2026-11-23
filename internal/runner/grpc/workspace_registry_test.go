package grpc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRegistryPersistsAndRejectsRebindingOrAliasing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	require.NoError(t, os.Mkdir(root, 0o700))
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	db, err := sqlite.Open(filepath.Join(base, "runner.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	registry, err := NewWorkspaceRegistry(root, db)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(root, "one"), 0o700))
	registered, err := registry.Register("ws-one", "one")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "one"), registered)
	_, err = registry.Register("ws-two", "one")
	require.ErrorIs(t, err, ErrWorkspacePathAlreadyRegistered)
	_, err = registry.Register("ws-one", "../other")
	require.ErrorIs(t, err, ErrWorkspaceIDRebound)
	resolved, err := registry.Resolve("ws-one")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "one"), resolved)
	_, err = registry.Resolve("missing")
	require.ErrorIs(t, err, ErrWorkspaceNotRegistered)

	require.NoError(t, pool.Close())
	db, err = sqlite.Open(filepath.Join(base, "runner.db"))
	require.NoError(t, err)
	pool, err = db.DB()
	require.NoError(t, err)
	defer pool.Close()
	registry, err = NewWorkspaceRegistry(root, db)
	require.NoError(t, err)
	resolved, err = registry.Resolve("ws-one")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "one"), resolved)
}

func TestWorkspaceRegistryRejectsUnsafePaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.Mkdir(outside, 0o700))
	db, err := sqlite.Open(filepath.Join(base, "runner.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	registry, err := NewWorkspaceRegistry(root, db)
	require.NoError(t, err)
	for _, path := range []string{"", "../outside", outside} {
		_, err = registry.Register("workspace-"+filepath.Base(filepath.Clean(path)), path)
		require.ErrorIs(t, err, ErrWorkspacePathOutsideRoot, path)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err == nil {
		_, err = registry.Register("symlink", "escape")
		require.ErrorIs(t, err, ErrWorkspacePathOutsideRoot)
	}
}
