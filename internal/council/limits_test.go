package council

import (
	"testing"

	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestMeterEnforcesConfiguredBudgets(t *testing.T) {
	l := Limits{MaxInputTokens: 10, MaxOutputTokens: 8, MaxCostMicros: 100}
	m := Meter{InputTokens: 4, OutputTokens: 2, CostMicros: 20}
	require.True(t, m.Allows(provider.Usage{InputTokens: 6, OutputTokens: 6, EstimatedCostMicros: 80}, l))
	require.False(t, m.Allows(provider.Usage{InputTokens: 7}, l))
	require.False(t, m.Allows(provider.Usage{EstimatedCostMicros: 81}, l))
}
