package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
	"github.com/stretchr/testify/require"
)

func TestTransactionRejectsBeforeHashMismatchWithoutWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	guard, err := pathguard.New(root, 1<<20)
	require.NoError(t, err)
	tx := NewTransaction(guard)
	_, err = tx.Apply(context.Background(), []schema.Patch{{Path: "main.go", UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n", BeforeHash: "wrong"}})
	require.ErrorIs(t, err, ErrBeforeHashMismatch)
	got, _ := os.ReadFile(path)
	require.Equal(t, "old\n", string(got))
}

func TestTransactionAppliesAndRestoresPatches(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one.txt")
	second := filepath.Join(root, "two.txt")
	require.NoError(t, os.WriteFile(first, []byte("one\n"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("two\n"), 0o600))
	guard, err := pathguard.New(root, 1<<20)
	require.NoError(t, err)
	tx := NewTransaction(guard)
	hash := func(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
	patches := []schema.Patch{{Path: "one.txt", UnifiedDiff: "@@ -1 +1 @@\n-one\n+changed\n", BeforeHash: hash("one\n")}, {Path: "two.txt", UnifiedDiff: "@@ -1 +1 @@\n-two\n+changed-two\n", BeforeHash: hash("two\n")}}
	result, err := tx.Apply(context.Background(), patches)
	require.NoError(t, err)
	require.Len(t, result, 2)
	got, _ := os.ReadFile(first)
	require.Equal(t, "changed\n", string(got))
	require.NoError(t, tx.Restore())
	got, _ = os.ReadFile(first)
	require.Equal(t, "one\n", string(got))
}
