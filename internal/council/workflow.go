package council

import (
	"context"
	"errors"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

// Workflow adapts the multi-stage Engine to the task application port. It is
// intentionally configured with explicit seats so deployments can choose
// which providers act as proposers, reviewers, judge and red team.
type Workflow struct {
	Engine    *Engine
	Proposers []Seat
	Reviewers []Seat
	Judge     Seat
	RedTeam   Seat

	// JudgeFailovers and RedTeamFailovers are tried in order when the
	// primary seat fails (role assignment sets carry up to two each).
	JudgeFailovers   []Seat
	RedTeamFailovers []Seat

	onEvent  EventHook
	snapshot []byte
}

// EventHook receives runtime council events (currently council.slot_skipped)
// for persistence by the caller.
type EventHook func(eventType string, data map[string]any)

var (
	// ErrJudgeUnavailable fails a run when the judge primary and every
	// failover seat errored: there is no decision to execute.
	ErrJudgeUnavailable = errors.New("judge_unavailable")
	// ErrRedTeamUnavailable fails a run when a red team is assigned but
	// every red-team seat errored.
	ErrRedTeamUnavailable = errors.New("red_team_unavailable")
)

type Progress func(string) error

type WorkflowStage string

const (
	StageAnalyzing WorkflowStage = "ANALYZING"
	StageProposing WorkflowStage = "PROPOSING"
	StageReviewing WorkflowStage = "REVIEWING"
	StageJudging   WorkflowStage = "JUDGING"
	StageRedTeam   WorkflowStage = "REDTEAM"
)

func NewWorkflow(engine *Engine, proposers, reviewers []Seat, judge, redTeam Seat) *Workflow {
	return &Workflow{Engine: engine, Proposers: append([]Seat(nil), proposers...), Reviewers: append([]Seat(nil), reviewers...), Judge: judge, RedTeam: redTeam}
}

// WithFailovers attaches judge/red-team failover seats (in ordinal order).
func (w *Workflow) WithFailovers(judgeFailovers, redTeamFailovers []Seat) *Workflow {
	w.JudgeFailovers = append([]Seat(nil), judgeFailovers...)
	w.RedTeamFailovers = append([]Seat(nil), redTeamFailovers...)
	return w
}

// SetEventHook registers the sink for council runtime events.
func (w *Workflow) SetEventHook(hook EventHook) { w.onEvent = hook }

// SetCouncilSnapshot records the resolved-configuration snapshot the run
// started with; CouncilSnapshot returns it for persistence.
func (w *Workflow) SetCouncilSnapshot(snapshot []byte) {
	w.snapshot = append([]byte(nil), snapshot...)
}

func (w *Workflow) CouncilSnapshot() []byte { return w.snapshot }

func (w *Workflow) emitSlotSkipped(role string, failure SeatFailure) {
	if w.onEvent == nil {
		return
	}
	w.onEvent("council.slot_skipped", map[string]any{
		"role":                role,
		"ordinal":             failure.Seat.Ordinal,
		"provider_profile_id": failure.Seat.ProfileID,
		"model":               failure.Seat.Model,
		"reason":              failure.Err.Error(),
	})
}

func (w *Workflow) Analyze(_ context.Context, requirement string) error {
	if w == nil || w.Engine == nil {
		return errors.New("council engine is required")
	}
	if requirement == "" {
		return errors.New("requirement is required")
	}
	// Reviewers and red team are optional under role assignments (0..8 and
	// 0..1 cardinality); a run needs at least one proposer and a judge.
	if len(w.Proposers) == 0 || w.Judge.ID == "" {
		return errors.New("council workflow seats are incomplete")
	}
	return nil
}

func (w *Workflow) Deliberate(ctx context.Context, requirement string) (schema.ExecutionPlan, error) {
	return w.deliberate(ctx, schema.TaskBrief{Requirement: requirement})
}

func (w *Workflow) DeliberateWithProgress(ctx context.Context, requirement string, progress func(WorkflowStage) error) (schema.ExecutionPlan, error) {
	return w.deliberateProgress(ctx, schema.TaskBrief{Requirement: requirement}, func(stage string) error {
		return progress(WorkflowStage(stage))
	})
}

func (w *Workflow) DeliberateWithWorkspace(ctx context.Context, requirement string, files []schema.WorkspaceFile) (schema.ExecutionPlan, error) {
	return w.deliberate(ctx, schema.TaskBrief{Requirement: requirement, WorkspaceFiles: append([]schema.WorkspaceFile(nil), files...)})
}

func (w *Workflow) DeliberateWithWorkspaceProgress(ctx context.Context, requirement string, files []schema.WorkspaceFile, progress Progress) (schema.ExecutionPlan, error) {
	return w.deliberateProgress(ctx, schema.TaskBrief{Requirement: requirement, WorkspaceFiles: append([]schema.WorkspaceFile(nil), files...)}, progress)
}

func (w *Workflow) deliberate(ctx context.Context, brief schema.TaskBrief) (schema.ExecutionPlan, error) {
	return w.deliberateProgress(ctx, brief, nil)
}

func (w *Workflow) deliberateProgress(ctx context.Context, brief schema.TaskBrief, progress Progress) (schema.ExecutionPlan, error) {
	requirement := brief.Requirement
	if err := w.Analyze(ctx, requirement); err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress(string(StageAnalyzing)); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	proposals, failures, err := w.Engine.ProposeWithFailures(ctx, brief, w.Proposers)
	for _, failure := range failures {
		w.emitSlotSkipped("proposer", failure)
	}
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("PROPOSING"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	var reviews []Generated[schema.PeerReview]
	if len(w.Reviewers) > 0 {
		var reviewFailures []SeatFailure
		reviews, reviewFailures, err = w.Engine.ReviewWithFailures(ctx, proposals, w.Reviewers)
		for _, failure := range reviewFailures {
			w.emitSlotSkipped("reviewer", failure)
		}
		if err != nil {
			return schema.ExecutionPlan{}, err
		}
		if progress != nil {
			if err := progress("REVIEWING"); err != nil {
				return schema.ExecutionPlan{}, err
			}
		}
	}
	decision, err := w.judgeWithFailover(ctx, proposals, reviews)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("JUDGING"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	var report schema.RedTeamReport
	redTeamSeats := w.redTeamSeats()
	if len(redTeamSeats) > 0 {
		generated, err := w.redTeamWithFailover(ctx, decision.Value, redTeamSeats)
		if err != nil {
			return schema.ExecutionPlan{}, err
		}
		report = generated.Value
		if progress != nil {
			if err := progress("REDTEAM"); err != nil {
				return schema.ExecutionPlan{}, err
			}
		}
	}
	return BuildExecutionPlan(decision.Value, report, nil)
}

// slotSkippedHook emits council.slot_skipped only when another seat follows,
// matching the previous behavior where the final failure emitted nothing.
func (w *Workflow) slotSkippedHook(role string) FailoverHook {
	return func(failure SeatFailure, willFailover bool) {
		if willFailover {
			w.emitSlotSkipped(role, failure)
		}
	}
}

func (w *Workflow) judgeWithFailover(ctx context.Context, proposals []Generated[schema.Proposal], reviews []Generated[schema.PeerReview]) (Generated[schema.CouncilDecision], error) {
	seats := append([]Seat{w.Judge}, w.JudgeFailovers...)
	decision, _, err := JudgeProposals(ctx, w.Engine, proposals, reviews, seats, OnSeatFailure(w.slotSkippedHook("judge")))
	return decision, err
}

func (w *Workflow) redTeamSeats() []Seat {
	var seats []Seat
	if w.RedTeam.ID != "" {
		seats = append(seats, w.RedTeam)
	}
	return append(seats, w.RedTeamFailovers...)
}

func (w *Workflow) redTeamWithFailover(ctx context.Context, decision schema.CouncilDecision, seats []Seat) (Generated[schema.RedTeamReport], error) {
	report, _, err := RedTeamDecision(ctx, w.Engine, decision, seats, OnSeatFailure(w.slotSkippedHook("red_team")))
	return report, err
}

func (w *Workflow) ReviewExecution(_ context.Context, _ string, report schema.VerificationReport) error {
	if !report.Passed {
		return errors.New("verification failed")
	}
	return nil
}
