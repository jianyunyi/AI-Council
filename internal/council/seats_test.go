package council

import (
	"context"
	"errors"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/roles"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestWorkflowFromResolvedBuildsRunnableWorkflowAndSnapshot(t *testing.T) {
	resolved := roles.Resolved{
		Source:  roles.SourceGlobal,
		Version: 3,
		Proposers: []roles.ResolvedSlot{
			{Role: roles.RoleProposer, Ordinal: 0, ProviderProfileID: "prof-a", Provider: "openai", Model: "gpt-4o"},
			{Role: roles.RoleProposer, Ordinal: 1, ProviderProfileID: "prof-gone", Provider: "openai", Model: "gpt-4o"},
		},
		Judge: []roles.ResolvedSlot{
			{Role: roles.RoleJudge, Ordinal: 0, ProviderProfileID: "prof-a", Provider: "openai", Model: "gpt-4o"},
		},
	}
	stage := &stagedProvider{name: "openai", values: []any{schema.Proposal{ID: "p1"}, planDecision()}}
	build := func(slot roles.ResolvedSlot) (provider.ModelProvider, error) {
		if slot.ProviderProfileID == "prof-gone" {
			return nil, errors.New("profile deleted")
		}
		return stage, nil
	}
	workflow, err := WorkflowFromResolved(resolved, build)
	require.NoError(t, err)
	require.Len(t, workflow.Proposers, 1, "unbuildable slots are dropped")
	require.Contains(t, string(workflow.CouncilSnapshot()), `"source":"global"`)

	plan, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Equal(t, 1, plan.Version)
}

func TestWorkflowFromResolvedRequiresJudge(t *testing.T) {
	resolved := roles.Resolved{
		Source:    roles.SourceLegacy,
		Proposers: []roles.ResolvedSlot{{Role: roles.RoleProposer, ProviderProfileID: "prof-a", Provider: "openai", Model: "gpt-4o"}},
	}
	_, err := WorkflowFromResolved(resolved, func(slot roles.ResolvedSlot) (provider.ModelProvider, error) {
		return &stagedProvider{name: "openai"}, nil
	})
	require.ErrorContains(t, err, "no runnable proposer or judge")
}
