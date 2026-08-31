package council

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProposalAliasesAreDeterministic(t *testing.T) {
	require.Equal(t, "Proposal A", proposalAlias(0))
	require.Equal(t, "Proposal C", proposalAlias(2))
	require.Equal(t, "Proposal AA", proposalAlias(26))
}
