package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type workspaceMigrationRunner struct {
	requests []*runnerv1.RegisterWorkspaceRequest
	err      error
	root     string
}

func (r *workspaceMigrationRunner) RegisterWorkspace(_ context.Context, req *runnerv1.RegisterWorkspaceRequest, _ ...grpc.CallOption) (*runnerv1.RegisterWorkspaceResponse, error) {
	r.requests = append(r.requests, req)
	if r.err != nil {
		return nil, r.err
	}
	return &runnerv1.RegisterWorkspaceResponse{Root: filepath.Join(r.root, filepath.FromSlash(req.RelativePath)), IsGit: true}, nil
}

func TestMigrateRunnerWorkspacesRegistersLegacyPathsAndFailsClosedOutsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o700))
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "council.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "legacy", CanonicalRoot: repo}).Error)
	runner := &workspaceMigrationRunner{root: root}
	require.NoError(t, migrateRunnerWorkspaces(context.Background(), db, runner, root))
	require.Len(t, runner.requests, 1)
	require.Equal(t, "repo", runner.requests[0].RelativePath)
	var migrated sqlite.WorkspaceRecord
	require.NoError(t, db.First(&migrated, "id = ?", "legacy").Error)
	require.Equal(t, "repo", migrated.RunnerPath)
	require.Equal(t, repo, migrated.CanonicalRoot)
	require.True(t, migrated.IsGit)

	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "unsafe", CanonicalRoot: outside}).Error)
	require.ErrorContains(t, migrateRunnerWorkspaces(context.Background(), db, runner, root), `legacy workspace "unsafe" is outside Runner root`)
	require.Len(t, runner.requests, 1)
	require.NoError(t, db.Delete(&sqlite.WorkspaceRecord{}, "id = ?", "unsafe").Error)

	// A transient Runner failure for one workspace must be skipped, not fatal:
	// Council keeps starting and the workspace stays NotFound until re-registered.
	runner.err = errors.New("runner unavailable")
	require.NoError(t, migrateRunnerWorkspaces(context.Background(), db, runner, root))
}

// A workspace whose directory has been deleted is skipped so Council still starts.
func TestMigrateRunnerWorkspacesSkipsMissingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(root, 0o700))
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "council.db"))
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	// RunnerPath set, but the directory does not exist and the runner rejects it.
	require.NoError(t, db.Create(&sqlite.WorkspaceRecord{ID: "gone", CanonicalRoot: filepath.Join(root, "gone"), RunnerPath: "gone"}).Error)
	runner := &workspaceMigrationRunner{root: root, err: errors.New("workspace registration rejected")}
	require.NoError(t, migrateRunnerWorkspaces(context.Background(), db, runner, root))
}
