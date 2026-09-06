package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestCollectorExcludesRegularGitFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../shared/.git/worktrees/worktree\n"), 0o600))

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Empty(t, snapshot.Files)
}

func TestCollectorExcludesNonUTF8AndCertificateFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "invalid.txt"), []byte{0xff, 0xfe}, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.pem"), []byte("certificate\n"), 0o600))

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Empty(t, snapshot.Files)
}

func TestCollectorAppliesFileCountLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(name), 0o600))
	}

	snapshot, err := NewCollector(Limits{MaxFiles: 2, MaxFileBytes: 20, MaxTotalBytes: 100}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []File{{Path: "a.txt", Content: "a.txt"}, {Path: "b.txt", Content: "b.txt"}}, snapshot.Files)
}

func TestCollectorAppliesTotalByteLimit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("def"), 0o600))

	snapshot, err := NewCollector(Limits{MaxFiles: 10, MaxFileBytes: 10, MaxTotalBytes: 5}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []File{{Path: "a.txt", Content: "abc"}}, snapshot.Files)
}

func TestCollectorUsesDefaultLimits(t *testing.T) {
	root := t.TempDir()
	for index := 0; index <= defaultMaxFiles; index++ {
		name := filepath.Join(root, "file-"+fmt.Sprintf("%03d", index)+".txt")
		require.NoError(t, os.WriteFile(name, []byte("x"), 0o600))
	}

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, snapshot.Files, defaultMaxFiles)
	require.Equal(t, "file-000.txt", snapshot.Files[0].Path)
	require.Equal(t, "file-199.txt", snapshot.Files[len(snapshot.Files)-1].Path)
}

func TestCollectorUsesDefaultPerFileByteLimit(t *testing.T) {
	root := t.TempDir()
	atLimit := strings.Repeat("a", defaultMaxFileBytes)
	require.NoError(t, os.WriteFile(filepath.Join(root, "at-limit.txt"), []byte(atLimit), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "over-limit.txt"), []byte(atLimit+"x"), 0o600))

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []File{{Path: "at-limit.txt", Content: atLimit}}, snapshot.Files)
}

func TestCollectorUsesDefaultTotalByteLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("a", defaultMaxFileBytes)
	fileCount := int(defaultMaxTotalBytes / defaultMaxFileBytes)
	for index := 0; index < fileCount; index++ {
		name := filepath.Join(root, "file-"+fmt.Sprintf("%03d", index)+".txt")
		require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "overflow.txt"), []byte("x"), 0o600))

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, snapshot.Files, fileCount)
	require.Equal(t, "file-000.txt", snapshot.Files[0].Path)
	require.Equal(t, "file-031.txt", snapshot.Files[len(snapshot.Files)-1].Path)
	require.NotContains(t, snapshot.Files, File{Path: "overflow.txt", Content: "x"})
}

func TestCollectorReturnsFilesInSortedOrder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "z.txt"), []byte("z"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "a.txt"), []byte("a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o600))

	snapshot, err := NewCollector(Limits{}).Collect(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []File{
		{Path: "b.txt", Content: "b"},
		{Path: "nested/a.txt", Content: "a"},
		{Path: "z.txt", Content: "z"},
	}, snapshot.Files)
}
