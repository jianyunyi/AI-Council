package approval

import (
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/stretchr/testify/require"
)

func TestVerifyRejectsMaterialPlanChanges(t *testing.T) {
	plan := schema.ExecutionPlan{Version: 3, Patches: []schema.Patch{{Path: "main.go", UnifiedDiff: "patch", BeforeHash: "before"}}, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, WorkDir: ".", TimeoutSeconds: 60}}}
	hash, err := Hash("run-1", "workspace-1", plan)
	require.NoError(t, err)
	require.NoError(t, Verify(hash, "run-1", "workspace-1", plan))
	plan.Commands[0].Args = []string{"env"}
	require.ErrorIs(t, Verify(hash, "run-1", "workspace-1", plan), ErrHashMismatch)
}
