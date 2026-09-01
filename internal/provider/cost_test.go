package provider

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEstimateCostUsesModelPriceTable(t *testing.T) {
	require.Equal(t, int64(25), EstimateCost("gpt-4o", Usage{InputTokens: 2, OutputTokens: 1}))
	require.Zero(t, EstimateCost("unknown", Usage{InputTokens: 100}))
}
