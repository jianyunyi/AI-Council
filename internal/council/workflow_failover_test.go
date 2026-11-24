package council

import (
	"context"
	"errors"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

func planDecision() schema.CouncilDecision {
	return schema.CouncilDecision{Plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, TimeoutSeconds: 30}}}}
}

type eventCapture struct{ events []map[string]any }

func (c *eventCapture) hook() EventHook {
	return func(_ string, data map[string]any) { c.events = append(c.events, data) }
}

func (c *eventCapture) roles() []string {
	roles := make([]string, 0, len(c.events))
	for _, event := range c.events {
		roles = append(roles, event["role"].(string))
	}
	return roles
}

func TestWorkflowJudgeFailoverUsesNextSeatAndEmitsSkip(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judgeDown := &stagedProvider{name: "judge-down", err: errors.New("judge primary down")}
	judgeBackup := &stagedProvider{name: "judge-backup", values: []any{planDecision()}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judgeDown, judgeBackup), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge-down", Model: "m", ProfileID: "prof-judge", Ordinal: 0},
		Seat{},
	).WithFailovers([]Seat{{ID: "judge-2", Provider: "judge-backup", Model: "m", ProfileID: "prof-judge-2", Ordinal: 1}}, nil)
	capture := &eventCapture{}
	workflow.SetEventHook(capture.hook())

	plan, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Equal(t, 1, plan.Version)
	require.Equal(t, []string{"judge"}, capture.roles())
	require.Equal(t, "prof-judge", capture.events[0]["provider_profile_id"])
}

func TestWorkflowJudgeUnavailableWhenAllJudgeSeatsFail(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judgeDown := &stagedProvider{name: "judge-down", err: errors.New("down")}
	judgeDown2 := &stagedProvider{name: "judge-down-2", err: errors.New("also down")}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judgeDown, judgeDown2), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge-down", Model: "m"},
		Seat{},
	).WithFailovers([]Seat{{ID: "judge-2", Provider: "judge-down-2", Model: "m"}}, nil)

	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.ErrorIs(t, err, ErrJudgeUnavailable)
	require.ErrorContains(t, err, "judge_unavailable")
}

func TestWorkflowSkipsFailedProposerAndReviewerSlots(t *testing.T) {
	good := &stagedProvider{name: "good", values: []any{schema.Proposal{ID: "p1"}, schema.Proposal{ID: "p1"}}}
	bad := &stagedProvider{name: "bad", err: errors.New("proposer down")}
	reviewerGood := &stagedProvider{name: "reviewer-good", values: []any{schema.PeerReview{Verdict: "approve"}, schema.PeerReview{Verdict: "approve"}}}
	reviewerBad := &stagedProvider{name: "reviewer-bad", err: errors.New("reviewer down")}
	judge := &stagedProvider{name: "judge", values: []any{planDecision()}}
	redTeam := &stagedProvider{name: "red-team", values: []any{schema.RedTeamReport{}}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(good, bad, reviewerGood, reviewerBad, judge, redTeam), nil, Limits{}),
		[]Seat{{ID: "proposer-good", Provider: "good", Model: "m", ProfileID: "prof-good"}, {ID: "proposer-bad", Provider: "bad", Model: "m", ProfileID: "prof-bad"}},
		[]Seat{{ID: "reviewer-good", Provider: "reviewer-good", Model: "m"}, {ID: "reviewer-bad", Provider: "reviewer-bad", Model: "m", ProfileID: "prof-rbad"}},
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-team", Model: "m"},
	)
	capture := &eventCapture{}
	workflow.SetEventHook(capture.hook())

	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"proposer", "reviewer"}, capture.roles())
}

func TestWorkflowRunsWithoutReviewersOrRedTeam(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judge := &stagedProvider{name: "judge", values: []any{planDecision()}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judge), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{},
	)
	require.NoError(t, workflow.Analyze(context.Background(), "implement it"))
	plan, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Equal(t, 1, plan.Version)
}

func TestWorkflowRedTeamFailover(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judge := &stagedProvider{name: "judge", values: []any{planDecision()}}
	redDown := &stagedProvider{name: "red-down", err: errors.New("red down")}
	redBackup := &stagedProvider{name: "red-backup", values: []any{schema.RedTeamReport{}}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judge, redDown, redBackup), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-down", Model: "m", ProfileID: "prof-red"},
	).WithFailovers(nil, []Seat{{ID: "red-team-2", Provider: "red-backup", Model: "m", ProfileID: "prof-red-2", Ordinal: 1}})
	capture := &eventCapture{}
	workflow.SetEventHook(capture.hook())

	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Equal(t, []string{"red_team"}, capture.roles())
}

func TestWorkflowAllProposersFailingFailsRun(t *testing.T) {
	bad := &stagedProvider{name: "bad", err: errors.New("down")}
	judge := &stagedProvider{name: "judge", values: []any{planDecision()}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(bad, judge), nil, Limits{}),
		[]Seat{{ID: "proposer-bad", Provider: "bad", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{},
	)
	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.ErrorContains(t, err, "all proposer seats failed")
}
