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
}

func NewWorkflow(engine *Engine, proposers, reviewers []Seat, judge, redTeam Seat) *Workflow {
	return &Workflow{Engine: engine, Proposers: append([]Seat(nil), proposers...), Reviewers: append([]Seat(nil), reviewers...), Judge: judge, RedTeam: redTeam}
}

func (w *Workflow) Analyze(_ context.Context, requirement string) error {
	if w == nil || w.Engine == nil {
		return errors.New("council engine is required")
	}
	if requirement == "" {
		return errors.New("requirement is required")
	}
	if len(w.Proposers) == 0 || len(w.Reviewers) == 0 || w.Judge.ID == "" || w.RedTeam.ID == "" {
		return errors.New("council workflow seats are incomplete")
	}
	return nil
}

func (w *Workflow) Deliberate(ctx context.Context, requirement string) (schema.ExecutionPlan, error) {
	if err := w.Analyze(ctx, requirement); err != nil {
		return schema.ExecutionPlan{}, err
	}
	brief := schema.TaskBrief{Requirement: requirement}
	proposals, err := w.Engine.Propose(ctx, brief, w.Proposers)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	reviews, err := w.Engine.Review(ctx, proposals, w.Reviewers)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	decision, err := w.Engine.Judge(ctx, proposals, reviews, w.Judge)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	redTeam, err := w.Engine.RedTeam(ctx, decision.Value, w.RedTeam)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	return BuildExecutionPlan(decision.Value, redTeam.Value, nil)
}

func (w *Workflow) ReviewExecution(_ context.Context, _ string, report schema.VerificationReport) error {
	if !report.Passed {
		return errors.New("verification failed")
	}
	return nil
}
