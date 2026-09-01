package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aicouncil/aicouncil/internal/core/runstate"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"sync"
	"time"
)

var ErrApprovalRequired = errors.New("manual approval required")

type WorkspaceDescription struct {
	Root           string
	IsGit          bool
	Dirty          bool
	DetectedStacks []string
}
type ApprovedExecution struct {
	RequestID, RunID, WorkspaceID, ApprovalHash string
	Plan                                        schema.ExecutionPlan
}
type CouncilPort interface {
	Analyze(context.Context, string) error
	Deliberate(context.Context, string) (schema.ExecutionPlan, error)
	ReviewExecution(context.Context, string, schema.VerificationReport) error
}
type RunnerPort interface {
	Describe(context.Context, string) (WorkspaceDescription, error)
	Execute(context.Context, ApprovedExecution) (schema.VerificationReport, error)
}
type EventPort interface {
	Append(context.Context, string, string, any) (sqlite.Event, error)
	After(context.Context, string, int64, int) ([]sqlite.Event, error)
}
type ApprovalPort interface {
	Save(context.Context, sqlite.ApprovalRecord) error
	Current(context.Context, string, int) (sqlite.ApprovalRecord, error)
	Invalidate(context.Context, string) error
}
type RunPort interface {
	Create(context.Context, string, runstate.State) error
	Transition(context.Context, string, runstate.State, string, string) error
	Load(context.Context, string) (sqlite.Run, []any, error)
}
type Task struct {
	ID, RunID, WorkspaceID, Requirement string
	Acceptance                          []string
	State                               runstate.State
	PlanVersion                         int
	Plan                                schema.ExecutionPlan
}
type Service struct {
	runs       *sqlite.RunRepository
	approvals  *sqlite.ApprovalRepository
	events     EventPort
	council    CouncilPort
	runner     RunnerPort
	maxReplans int
	mu         sync.Mutex
	tasks      map[string]*Task
}

func NewService(runs *sqlite.RunRepository, approvals *sqlite.ApprovalRepository, events EventPort, council CouncilPort, runner RunnerPort, maxReplans int) *Service {
	return &Service{runs: runs, approvals: approvals, events: events, council: council, runner: runner, maxReplans: maxReplans, tasks: map[string]*Task{}}
}
func (s *Service) Create(ctx context.Context, workspace, requirement string, acceptance []string) (Task, error) {
	if requirement == "" || len(acceptance) == 0 {
		return Task{}, errors.New("requirement and acceptance are required")
	}
	id := newID()
	if err := s.runs.Create(ctx, id, runstate.Draft); err != nil {
		return Task{}, err
	}
	t := &Task{ID: id, RunID: id, WorkspaceID: workspace, Requirement: requirement, Acceptance: acceptance, State: runstate.Draft, PlanVersion: 1}
	s.mu.Lock()
	s.tasks[id] = t
	s.mu.Unlock()
	if s.events != nil {
		_, _ = s.events.Append(ctx, id, "task.created", t)
	}
	return *t, nil
}
func (s *Service) Start(ctx context.Context, id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	s.mu.Unlock()
	if t == nil {
		return errors.New("task not found")
	}
	for _, next := range []runstate.State{runstate.Analyzing, runstate.Proposing, runstate.Reviewing, runstate.Judging, runstate.RedTeam, runstate.AwaitingApproval} {
		if err := s.runs.Transition(ctx, t.RunID, next, "system", "task lifecycle"); err != nil {
			return err
		}
		t.State = next
	}
	if s.events != nil {
		_, _ = s.events.Append(ctx, id, "task.awaiting_approval", t)
	}
	return nil
}
func (s *Service) Approve(ctx context.Context, id, hash, actor string, version int) error {
	s.mu.Lock()
	t := s.tasks[id]
	s.mu.Unlock()
	if t == nil {
		return errors.New("task not found")
	}
	if t.State != runstate.AwaitingApproval || version != t.PlanVersion {
		return errors.New("task is not awaiting matching plan")
	}
	if err := s.approvals.Invalidate(ctx, t.RunID); err != nil {
		return err
	}
	if err := s.approvals.Save(ctx, sqlite.ApprovalRecord{ID: newID(), RunID: t.RunID, PlanVersion: version, SnapshotHash: hash, Decision: "approved", Actor: actor, CreatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	if s.events != nil {
		_, _ = s.events.Append(ctx, id, "approval.created", map[string]any{"plan_version": version, "hash": hash})
	}
	return nil
}
func (s *Service) Execute(ctx context.Context, id, requestID string) (schema.VerificationReport, error) {
	s.mu.Lock()
	t := s.tasks[id]
	s.mu.Unlock()
	if t == nil {
		return schema.VerificationReport{}, errors.New("task not found")
	}
	approval, err := s.approvals.Current(ctx, t.RunID, t.PlanVersion)
	if err != nil || approval.Decision != "approved" {
		return schema.VerificationReport{}, ErrApprovalRequired
	}
	t.State = runstate.Executing
	_ = s.runs.Transition(ctx, t.RunID, runstate.Executing, "system", "approved execution")
	report, err := s.runner.Execute(ctx, ApprovedExecution{RequestID: requestID, RunID: t.RunID, WorkspaceID: t.WorkspaceID, ApprovalHash: approval.SnapshotHash, Plan: t.Plan})
	if err != nil {
		return report, err
	}
	t.State = runstate.Verifying
	_ = s.runs.Transition(ctx, t.RunID, runstate.Verifying, "runner", "verification")
	if report.Passed {
		t.State = runstate.Succeeded
		_ = s.runs.Transition(ctx, t.RunID, runstate.Succeeded, "runner", "verification passed")
	} else {
		t.State = runstate.Replanning
		_ = s.runs.Transition(ctx, t.RunID, runstate.Replanning, "runner", "verification failed")
	}
	return report, nil
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("generate id: %v", err))
	}
	return hex.EncodeToString(b[:])
}
