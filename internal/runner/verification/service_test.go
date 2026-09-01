package verification

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVerifyChangedPathsRejectsUnapprovedFiles(t *testing.T) {
	require.NoError(t, VerifyChangedPaths([]string{"main.go"}, []string{"main.go"}))
	require.ErrorIs(t, VerifyChangedPaths([]string{"main.go"}, []string{"main.go", "secret.txt"}), ErrUnexpectedChange)
}
