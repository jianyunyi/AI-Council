package context

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectorReturnsOnlySafeTextFilesWithinLimits(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("ok\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=value\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "id_rsa"), []byte("key"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "data.bin"), []byte("text\x00binary"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.txt"), []byte("12345"), 0o600))

	collector := NewCollector(Limits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalBytes: 100})
	snapshot, err := collector.Collect(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []File{{Path: "main.go", Content: "ok\n"}}, snapshot.Files)
}

func TestCollectorExcludesSymlinkedFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600))
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "linked-secret.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Empty(t, snapshot.Files)
}
