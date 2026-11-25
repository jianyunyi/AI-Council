package council

import (
	"context"
	"errors"
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/stretchr/testify/require"
)

// ---------- ValidatePlan / BuildExecutionPlan equivalence ----------

func libValidPlan() schema.ExecutionPlan {
	return schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 30}}}
}

func TestValidatePlanMatchesBuildExecutionPlan(t *testing.T) {
	validCommand := schema.Command{Executable: "go", TimeoutSeconds: 30}
	cases := []struct {
		name   string
		plan   schema.ExecutionPlan
		report schema.RedTeamReport
		code   string
		field  string
		msg    string
	}{
		{name: "valid", plan: libValidPlan()},
		{name: "valid patch only", plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: "src/main.go"}}}},
		{name: "blocking", plan: libValidPlan(), report: schema.RedTeamReport{Blocking: []string{"unsafe"}},
			code: PlanIssueBlockingFindings, msg: "red-team report contains blocking findings"},
		{name: "empty", plan: schema.ExecutionPlan{Version: 1},
			code: PlanIssueEmptyPlan, msg: "execution plan must include patches or commands"},
		{name: "version", plan: schema.ExecutionPlan{Version: 0, Commands: []schema.Command{validCommand}},
			code: PlanIssueInvalidVersion, field: "version", msg: "execution plan version must be positive"},
		{name: "patch path", plan: schema.ExecutionPlan{Version: 1, Patches: []schema.Patch{{Path: "/abs/outside.go"}}},
			code: PlanIssueUnsafePatchPath, field: "patches[0]", msg: "execution plan patch path must be a nonempty relative path"},
		{name: "blank executable", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: " ", TimeoutSeconds: 30}}},
			code: PlanIssueInvalidCommand, field: "commands[0]", msg: "execution plan commands require an executable and positive timeout"},
		{name: "nonpositive timeout", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 0}}},
			code: PlanIssueInvalidCommand, field: "commands[0]", msg: "execution plan commands require an executable and positive timeout"},
		{name: "absolute workdir", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{{Executable: "go", TimeoutSeconds: 30, WorkDir: "/abs"}}},
			code: PlanIssueUnsafeWorkDir, field: "commands[0]", msg: "execution plan command work directory must be empty, dot, or a relative path"},
		{name: "blank verification executable", plan: schema.ExecutionPlan{Version: 1, Commands: []schema.Command{validCommand}, VerificationCommands: []schema.Command{{Executable: "", TimeoutSeconds: 10}}},
			code: PlanIssueInvalidCommand, field: "verification_commands[0]", msg: "execution plan commands require an executable and positive timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidatePlan(tc.plan, tc.report)
			_, err := BuildExecutionPlan(schema.CouncilDecision{Plan: tc.plan}, tc.report, nil)
			if tc.code == "" {
				require.Empty(t, issues)
				require.NoError(t, err)
				return
			}
			require.NotEmpty(t, issues)
			require.Equal(t, tc.code, issues[0].Code)
			require.Equal(t, tc.field, issues[0].Field)
			require.Equal(t, tc.msg, issues[0].Message)
			require.Equal(t, tc.msg, issues[0].Error())
			require.Error(t, err)
			require.Equal(t, tc.msg, err.Error())
		})
	}
}

func TestValidatePlanOrdersIssuesLikeBuild(t *testing.T) {
	plan := libValidPlan()
	plan.Commands = []schema.Command{
		{Executable: "go", TimeoutSeconds: 30, WorkDir: "/abs"}, // workdir issue on commands[0]
		{Executable: "", TimeoutSeconds: 30},                    // executable issue on commands[1]
	}
	issues := ValidatePlan(plan, schema.RedTeamReport{})
	require.Len(t, issues, 2)
	require.Equal(t, PlanIssueUnsafeWorkDir, issues[0].Code)
	require.Equal(t, "commands[0]", issues[0].Field)
	require.Equal(t, PlanIssueInvalidCommand, issues[1].Code)
	require.Equal(t, "commands[1]", issues[1].Field)

	_, err := BuildExecutionPlan(schema.CouncilDecision{Plan: plan}, schema.RedTeamReport{}, nil)
	require.Error(t, err)
	require.Equal(t, issues[0].Message, err.Error())
}

func TestBuildExecutionPlanSetsAcceptanceAfterValidation(t *testing.T) {
	plan, err := BuildExecutionPlan(schema.CouncilDecision{Plan: libValidPlan()}, schema.RedTeamReport{}, []string{"x", "y"})
	require.NoError(t, err)
	require.Equal(t, []string{"x", "y"}, plan.Acceptance)
}

// ---------- ReviewProposal ----------

func libProposal() Generated[schema.Proposal] {
	return Generated[schema.Proposal]{Seat: Seat{ID: "author-seat", Provider: "author-prov", Model: "m"}, Value: schema.Proposal{ID: "p1"}}
}

func TestReviewProposalExcludesAuthor(t *testing.T) {
	authorProv := &stagedProvider{name: "author-prov", values: []any{schema.PeerReview{Verdict: "self"}}}
	reviewerProv := &stagedProvider{name: "rev-prov", values: []any{schema.PeerReview{Verdict: "approve"}}}
	engine := NewEngine(provider.NewRegistry(authorProv, reviewerProv), nil, Limits{})

	reviews, failures, err := ReviewProposal(context.Background(), engine, libProposal(), []Seat{
		{ID: "author-seat", Provider: "author-prov", Model: "m"},
		{ID: "rev-1", Provider: "rev-prov", Model: "m"},
	})
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Len(t, reviews, 1)
	require.Equal(t, "rev-1", reviews[0].Seat.ID)
	require.Equal(t, "rev-1", reviews[0].Value.ReviewerSeatID)
	require.Equal(t, 0, authorProv.next, "author provider must never be called")
}

func TestReviewProposalOnlyAuthorReturnsNoEligibleReviewers(t *testing.T) {
	authorProv := &stagedProvider{name: "author-prov", values: []any{schema.PeerReview{}}}
	engine := NewEngine(provider.NewRegistry(authorProv), nil, Limits{})

	_, _, err := ReviewProposal(context.Background(), engine, libProposal(), []Seat{
		{ID: "author-seat", Provider: "author-prov", Model: "m"},
	})
	require.ErrorIs(t, err, ErrNoEligibleReviewers)
	require.Equal(t, 0, authorProv.next)
}

func TestReviewProposalDropsFailedReviewers(t *testing.T) {
	downProv := &stagedProvider{name: "down-prov", err: errors.New("reviewer one down")}
	okProv := &stagedProvider{name: "ok-prov", values: []any{schema.PeerReview{Verdict: "approve"}}}
	engine := NewEngine(provider.NewRegistry(downProv, okProv), nil, Limits{})

	reviews, failures, err := ReviewProposal(context.Background(), engine, libProposal(), []Seat{
		{ID: "rev-down", Provider: "down-prov", Model: "m"},
		{ID: "rev-ok", Provider: "ok-prov", Model: "m"},
	})
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, "rev-ok", reviews[0].Seat.ID)
	require.Len(t, failures, 1)
	require.Equal(t, "rev-down", failures[0].Seat.ID)
	require.ErrorContains(t, failures[0].Err, "reviewer one down")
}

func TestReviewProposalAllReviewersFail(t *testing.T) {
	downA := &stagedProvider{name: "down-a", err: errors.New("down a")}
	downB := &stagedProvider{name: "down-b", err: errors.New("down b")}
	engine := NewEngine(provider.NewRegistry(downA, downB), nil, Limits{})

	_, failures, err := ReviewProposal(context.Background(), engine, libProposal(), []Seat{
		{ID: "rev-a", Provider: "down-a", Model: "m"},
		{ID: "rev-b", Provider: "down-b", Model: "m"},
	})
	require.Error(t, err)
	require.Len(t, failures, 2)
}

func TestReviewProposalGuards(t *testing.T) {
	_, _, err := ReviewProposal(context.Background(), nil, libProposal(), []Seat{{ID: "rev", Provider: "x", Model: "m"}})
	require.ErrorContains(t, err, "council engine is required")

	engine := NewEngine(provider.NewRegistry(), nil, Limits{})
	_, _, err = ReviewProposal(context.Background(), engine, libProposal(), nil)
	require.ErrorContains(t, err, "proposals and reviewers are required")
}

// ---------- JudgeProposals ----------

type libHookCall struct {
	seatID       string
	willFailover bool
}

func libHookRecorder(calls *[]libHookCall) FailoverOption {
	return OnSeatFailure(func(failure SeatFailure, willFailoverr bool) {
		*calls = append(*calls, libHookCall{seatID: failure.Seat.ID, willFailover: willFailoverr})
	})
}

func TestJudgeProposalsFailoverOrderAndHook(t *testing.T) {
	judgeA := &stagedProvider{name: "judge-a", err: errors.New("judge a down")}
	judgeB := &stagedProvider{name: "judge-b", err: errors.New("judge b down")}
	judgeC := &stagedProvider{name: "judge-c", values: []any{planDecision()}}
	engine := NewEngine(provider.NewRegistry(judgeA, judgeB, judgeC), nil, Limits{})
	proposals := []Generated[schema.Proposal]{{Seat: Seat{ID: "p1", Provider: "proposer", Model: "m"}}}

	var calls []libHookCall
	decision, failures, err := JudgeProposals(context.Background(), engine, proposals, nil,
		[]Seat{
			{ID: "j1", Provider: "judge-a", Model: "m"},
			{ID: "j2", Provider: "judge-b", Model: "m"},
			{ID: "j3", Provider: "judge-c", Model: "m"},
		}, libHookRecorder(&calls))
	require.NoError(t, err)
	require.Equal(t, "j3", decision.Seat.ID)
	require.Len(t, failures, 2)
	require.Equal(t, "j1", failures[0].Seat.ID)
	require.Equal(t, "j2", failures[1].Seat.ID)
	require.Equal(t, []libHookCall{{seatID: "j1", willFailover: true}, {seatID: "j2", willFailover: true}}, calls)
}

func TestJudgeProposalsAllFailWrapsLastError(t *testing.T) {
	judgeA := &stagedProvider{name: "judge-a", err: errors.New("judge a down")}
	judgeB := &stagedProvider{name: "judge-b", err: errors.New("judge b down")}
	engine := NewEngine(provider.NewRegistry(judgeA, judgeB), nil, Limits{})
	proposals := []Generated[schema.Proposal]{{Seat: Seat{ID: "p1", Provider: "proposer", Model: "m"}}}

	var calls []libHookCall
	_, failures, err := JudgeProposals(context.Background(), engine, proposals, nil,
		[]Seat{
			{ID: "j1", Provider: "judge-a", Model: "m"},
			{ID: "j2", Provider: "judge-b", Model: "m"},
		}, libHookRecorder(&calls))
	require.ErrorIs(t, err, ErrJudgeUnavailable)
	require.ErrorContains(t, err, "judge b down")
	require.Len(t, failures, 2)
	require.Equal(t, []libHookCall{{seatID: "j1", willFailover: true}, {seatID: "j2", willFailover: false}}, calls)
}

func TestJudgeProposalsNoSeats(t *testing.T) {
	engine := NewEngine(provider.NewRegistry(), nil, Limits{})
	_, _, err := JudgeProposals(context.Background(), engine, nil, nil, nil)
	require.ErrorIs(t, err, ErrJudgeUnavailable)
	require.ErrorContains(t, err, "no seats configured")
}

func TestJudgeProposalsSkipsProposerSeat(t *testing.T) {
	judgeA := &stagedProvider{name: "judge-a", values: []any{planDecision()}}
	judgeB := &stagedProvider{name: "judge-b", values: []any{planDecision()}}
	engine := NewEngine(provider.NewRegistry(judgeA, judgeB), nil, Limits{})
	proposals := []Generated[schema.Proposal]{{Seat: Seat{ID: "shared", Provider: "proposer", Model: "m"}}}

	decision, failures, err := JudgeProposals(context.Background(), engine, proposals, nil,
		[]Seat{
			{ID: "shared", Provider: "judge-a", Model: "m"},
			{ID: "j2", Provider: "judge-b", Model: "m"},
		})
	require.NoError(t, err)
	require.Equal(t, "j2", decision.Seat.ID)
	require.Len(t, failures, 1)
	require.Equal(t, "shared", failures[0].Seat.ID)
	require.ErrorContains(t, failures[0].Err, "judge seat must not be a proposer")
	require.Equal(t, 0, judgeA.next, "proposer's judge seat must fail before any provider call")
}

// ---------- RedTeamDecision ----------

func TestRedTeamDecisionFailover(t *testing.T) {
	redA := &stagedProvider{name: "red-a", err: errors.New("red a down")}
	redB := &stagedProvider{name: "red-b", values: []any{schema.RedTeamReport{Recommendation: "ok"}}}
	engine := NewEngine(provider.NewRegistry(redA, redB), nil, Limits{})

	report, failures, err := RedTeamDecision(context.Background(), engine, schema.CouncilDecision{},
		[]Seat{
			{ID: "rt1", Provider: "red-a", Model: "m"},
			{ID: "rt2", Provider: "red-b", Model: "m"},
		})
	require.NoError(t, err)
	require.Equal(t, "rt2", report.Seat.ID)
	require.Len(t, failures, 1)
	require.Equal(t, "rt1", failures[0].Seat.ID)
}

func TestRedTeamDecisionAllFail(t *testing.T) {
	redA := &stagedProvider{name: "red-a", err: errors.New("red a down")}
	redB := &stagedProvider{name: "red-b", err: errors.New("red b down")}
	engine := NewEngine(provider.NewRegistry(redA, redB), nil, Limits{})

	_, _, err := RedTeamDecision(context.Background(), engine, schema.CouncilDecision{},
		[]Seat{
			{ID: "rt1", Provider: "red-a", Model: "m"},
			{ID: "rt2", Provider: "red-b", Model: "m"},
		})
	require.ErrorIs(t, err, ErrRedTeamUnavailable)
	require.ErrorContains(t, err, "red b down")
}

// ---------- Workflow regression: slot_skipped emission unchanged ----------

func TestWorkflowJudgeAllSeatsFailEmitsOneSkipEvent(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judgeDown := &stagedProvider{name: "judge-down", err: errors.New("judge primary down")}
	judgeDown2 := &stagedProvider{name: "judge-down-2", err: errors.New("judge backup down")}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judgeDown, judgeDown2), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge-down", Model: "judge-model", ProfileID: "prof-judge", Ordinal: 3},
		Seat{},
	).WithFailovers([]Seat{{ID: "judge-2", Provider: "judge-down-2", Model: "judge-model-2", ProfileID: "prof-judge-2", Ordinal: 4}}, nil)
	capture := &eventCapture{}
	workflow.SetEventHook(capture.hook())

	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.ErrorIs(t, err, ErrJudgeUnavailable)
	require.Len(t, capture.events, 1, "only the failure followed by a failover emits an event")
	event := capture.events[0]
	require.Equal(t, "judge", event["role"])
	require.Equal(t, 3, event["ordinal"])
	require.Equal(t, "prof-judge", event["provider_profile_id"])
	require.Equal(t, "judge-model", event["model"])
	require.Equal(t, "judge primary down", event["reason"])
}

func TestWorkflowRedTeamFailoverEmitsSkipEventFields(t *testing.T) {
	proposer := &stagedProvider{name: "proposer", values: []any{schema.Proposal{ID: "p1"}}}
	judge := &stagedProvider{name: "judge", values: []any{planDecision()}}
	redDown := &stagedProvider{name: "red-down", err: errors.New("red primary down")}
	redBackup := &stagedProvider{name: "red-backup", values: []any{schema.RedTeamReport{}}}
	workflow := NewWorkflow(NewEngine(provider.NewRegistry(proposer, judge, redDown, redBackup), nil, Limits{}),
		[]Seat{{ID: "proposer", Provider: "proposer", Model: "m"}},
		nil,
		Seat{ID: "judge", Provider: "judge", Model: "m"},
		Seat{ID: "red-team", Provider: "red-down", Model: "red-model", ProfileID: "prof-red", Ordinal: 2},
	).WithFailovers(nil, []Seat{{ID: "red-team-2", Provider: "red-backup", Model: "red-model-2", ProfileID: "prof-red-2", Ordinal: 5}})
	capture := &eventCapture{}
	workflow.SetEventHook(capture.hook())

	_, err := workflow.Deliberate(context.Background(), "implement it")
	require.NoError(t, err)
	require.Len(t, capture.events, 1)
	event := capture.events[0]
	require.Equal(t, "red_team", event["role"])
	require.Equal(t, 2, event["ordinal"])
	require.Equal(t, "prof-red", event["provider_profile_id"])
	require.Equal(t, "red-model", event["model"])
	require.Equal(t, "red primary down", event["reason"])
}
