package command

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunsArgvWithoutShell(t *testing.T) {
	g, err := pathguard.New(t.TempDir(), 1<<20)
	require.NoError(t, err)
	e := NewExecutor(g)
	executable, args := "go", []string{"version"}
	if runtime.GOOS == "windows" {
		executable, args = "cmd", []string{"/c", "echo", "literal-value"}
	}
	// Process startup can exceed one second on a busy Windows CI worker. Keep
	// this test focused on argv execution rather than making it timing-sensitive.
	result, err := e.Run(context.Background(), Spec{Executable: executable, Args: args, WorkDir: ".", Timeout: 5 * time.Second, OutputLimit: 1024})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.False(t, result.TimedOut)
	require.NotEmpty(t, result.Stdout)
	_, err = g.ResolveDirectory(filepath.Join("..", "outside"))
	require.Error(t, err)
}
