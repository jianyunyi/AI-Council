package pathguard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuardRejectsTraversalSensitiveAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "big.bin"), []byte("01234567890123456789012345678901234567890123456789"), 0o600))
	g, err := New(root, 32)
	require.NoError(t, err)
	_, err = g.ResolveRead("main.go")
	require.NoError(t, err)
	_, err = g.ResolveRead("../outside")
	require.ErrorIs(t, err, ErrOutsideWorkspace)
	_, err = g.ResolveRead(".env")
	require.ErrorIs(t, err, ErrSensitivePath)
	_, err = g.ResolveRead("big.bin")
	require.ErrorIs(t, err, ErrFileTooLarge)
	_, err = g.ResolveWrite("new.go")
	require.NoError(t, err)
}
