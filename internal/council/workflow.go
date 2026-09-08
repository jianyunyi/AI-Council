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
	proposals, err := w.Engine.Propose(ctx, brief, w.Proposers)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("PROPOSING"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	reviews, err := w.Engine.Review(ctx, proposals, w.Reviewers)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("REVIEWING"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	decision, err := w.Engine.Judge(ctx, proposals, reviews, w.Judge)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("JUDGING"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	redTeam, err := w.Engine.RedTeam(ctx, decision.Value, w.RedTeam)
	if err != nil {
		return schema.ExecutionPlan{}, err
	}
	if progress != nil {
		if err := progress("REDTEAM"); err != nil {
			return schema.ExecutionPlan{}, err
		}
	}
	return BuildExecutionPlan(decision.Value, redTeam.Value, nil)
}

func (w *Workflow) ReviewExecution(_ context.Context, _ string, report schema.VerificationReport) error {
	if !report.Passed {
		return errors.New("verification failed")
	}
	return nil
}
