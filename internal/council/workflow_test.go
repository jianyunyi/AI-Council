package council

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

type stagedProvider struct {
	name   string
	values []any
	next   int
}

func (p *stagedProvider) Name() string { return p.name }
func (p *stagedProvider) Generate(_ context.Context, _ provider.Request) (provider.Response, error) {
	body, _ := json.Marshal(p.values[p.next])
	p.next++
	return provider.Response{Content: body}, nil
}

func TestWorkflowDeliberateReturnsJudgePlanAfterValidation(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	reviewer := &stagedProvider{name: "reviewer", values: []any{schema.PeerReview{Verdict: "approve"}}}
	judgePlan := schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}
	judge := &stagedProvider{name: "judge", values: []any{schema.CouncilDecision{Plan: judgePlan}}}
	redTeam := &stagedProvider{name: "red-team", values: []any{schema.RedTeamReport{}}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, reviewer, judge, redTeam), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		[]Seat{{ID: "reviewer", Provider: "reviewer", Model: "m"}},
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-team", Model: "m"},
	)

	got, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Equal(t, judgePlan.Commands, got.Commands)
}
