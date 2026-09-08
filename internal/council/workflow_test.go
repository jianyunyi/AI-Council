package council

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

type stagedProvider struct {
	name    string
	values  []any
	next    int
	request provider.Request
	err     error
}

func (p *stagedProvider) Name() string { return p.name }
func (p *stagedProvider) Generate(_ context.Context, request provider.Request) (provider.Response, error) {
	p.request = request
	if p.err != nil {
		return provider.Response{}, p.err
	}
	body, _ := json.Marshal(p.values[p.next])
	p.next++
	return provider.Response{Content: body}, nil
}

func TestWorkflowDeliberateWithProgressReportsOnlyCompletedStages(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	reviewer := &stagedProvider{name: "reviewer", values: []any{schema.PeerReview{Verdict: "approve"}}}
	judge := &stagedProvider{name: "judge", values: []any{schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}}}}
	redTeam := &stagedProvider{name: "red-team", err: errors.New("red-team unavailable")}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, reviewer, judge, redTeam), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		[]Seat{{ID: "reviewer", Provider: "reviewer", Model: "m"}},
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-team", Model: "m"},
	)

	var stages []WorkflowStage
	_, err := workflow.DeliberateWithProgress(context.Background(), "implement it", func(stage WorkflowStage) error {
		stages = append(stages, stage)
		return nil
	})

	require.ErrorContains(t, err, "red-team unavailable")
	require.Equal(t, []WorkflowStage{StageAnalyzing, StageProposing, StageReviewing, StageJudging}, stages)
}

func TestWorkflowDeliberateWithWorkspaceIncludesStructuredFilesInProposalBrief(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	reviewer := &stagedProvider{name: "reviewer", values: []any{schema.PeerReview{Verdict: "approve"}}}
	judge := &stagedProvider{name: "judge", values: []any{schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}}}}
	redTeam := &stagedProvider{name: "red-team", values: []any{schema.RedTeamReport{}}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, reviewer, judge, redTeam), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		[]Seat{{ID: "reviewer", Provider: "reviewer", Model: "m"}},
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-team", Model: "m"},
	)

	_, err := workflow.DeliberateWithWorkspace(context.Background(), "implement it", []schema.WorkspaceFile{{Path: "main.go", Content: "package main"}})

	require.NoError(t, err)
	require.Contains(t, proposer.request.Messages[0].Content, `"workspace_files":[{"path":"main.go","content":"package main"}]`)
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
