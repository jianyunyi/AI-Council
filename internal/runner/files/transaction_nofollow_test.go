//go:build !windows

package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
	"github.com/stretchr/testify/require"
)

func TestApplyRefusesSymlinkedTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret\n"), 0o600))
	link := filepath.Join(root, "candidate.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	guard, err := pathguard.New(root, 1<<20)
	require.NoError(t, err)
	_, err = NewTransaction(guard).Apply(context.Background(), []schema.Patch{{Path: "candidate.txt", UnifiedDiff: "@@ -1 +1 @@\n-secret\n+x\n"}})
	require.Error(t, err)
}

func TestResolveWriteRefusesGitInternal(t *testing.T) {
	guard, err := pathguard.New(t.TempDir(), 1<<20)
	require.NoError(t, err)
	_, err = guard.ResolveWrite(filepath.Join(".git", "config"))
	require.ErrorIs(t, err, pathguard.ErrGitInternalPath)
}
