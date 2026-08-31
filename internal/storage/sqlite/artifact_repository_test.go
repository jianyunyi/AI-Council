package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/core/artifact"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/stretchr/testify/require"
)

func TestArtifactRepositoryRoundTripAndDetectsTampering(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "council.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := NewArtifactRepository(db, filepath.Join(root, "artifacts"))
	env, err := artifact.New("proposal", "artifact-1", "", schema.Proposal{ID: "p1", Summary: "safe"})
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), "run-1", env))
	loaded, err := repo.Load(context.Background(), env.ID)
	require.NoError(t, err)
	require.Equal(t, env.ContentHash, loaded.ContentHash)
	require.JSONEq(t, string(env.Content), string(loaded.Content))

	path := filepath.Join(root, "artifacts", "run-1", "artifact-1.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"tampered":true}`), 0o600))
	_, err = repo.Load(context.Background(), env.ID)
	require.Error(t, err)
}
