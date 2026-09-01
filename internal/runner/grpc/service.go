package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/command"
	"github.com/aicouncil/aicouncil/internal/runner/files"
	"github.com/aicouncil/aicouncil/internal/runner/idempotency"
	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	runnerv1.UnimplementedWorkspaceRunnerServer
	root        string
	guard       *pathguard.Guard
	executor    *command.Executor
	transaction *files.Transaction
	idem        *idempotency.Store
}

func NewService(root string) (*Service, error) {
	guard, err := pathguard.New(root, 16<<20)
	if err != nil {
		return nil, err
	}
	return &Service{root: guard.Root(), guard: guard, executor: command.NewExecutor(guard), transaction: files.NewTransaction(guard), idem: idempotency.New()}, nil
}
func (s *Service) DescribeWorkspace(context.Context, *runnerv1.DescribeWorkspaceRequest) (*runnerv1.DescribeWorkspaceResponse, error) {
	return &runnerv1.DescribeWorkspaceResponse{Root: s.root, DetectedStacks: []string{"go"}}, nil
}
func (s *Service) GetExecution(_ context.Context, req *runnerv1.GetExecutionRequest) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	if v, ok := s.idem.Get(req.RequestId); ok {
		return v, nil
	}
	return nil, status.Error(codes.NotFound, "execution not found")
}
func (s *Service) ExecuteApprovedPlan(ctx context.Context, req *runnerv1.ExecuteApprovedPlanRequest) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	if req.RequestId == "" {
		return nil, status.Error(codes.InvalidArgument, "request_id is required")
	}
	if saved, ok := s.idem.Get(req.RequestId); ok {
		return saved, nil
	}
	release, err := s.idem.Begin(req.RequestId)
	if err != nil {
		return nil, status.Error(codes.Aborted, "execution already in progress")
	}
	defer release()
	plan := schema.ExecutionPlan{Version: int(req.PlanVersion), Acceptance: req.Acceptance}
	for _, p := range req.Patches {
		plan.Patches = append(plan.Patches, schema.Patch{Path: p.Path, UnifiedDiff: p.UnifiedDiff, BeforeHash: p.BeforeHash})
	}
	for _, c := range req.Commands {
		plan.Commands = append(plan.Commands, schema.Command{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int(c.TimeoutSeconds), Purpose: c.Purpose})
	}
	if err := approval.Verify(req.ApprovalHash, req.RunId, req.WorkspaceId, plan); err != nil {
		return nil, status.Error(codes.PermissionDenied, "approval mismatch")
	}
	response := &runnerv1.ExecuteApprovedPlanResponse{RequestId: req.RequestId, Status: "SUCCEEDED"}
	patchesApplied := len(plan.Patches) > 0
	if patchesApplied {
		if _, err := s.transaction.Apply(ctx, plan.Patches); err != nil {
			response.Status = "FAILED"
			response.ErrorCode = "patch_failed"
			s.idem.Complete(req.RequestId, response)
			return response, nil
		}
	}
	for _, c := range plan.Commands {
		result, runErr := s.executor.Run(ctx, command.Spec{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, OutputLimit: 1 << 20})
		step := &runnerv1.StepResult{Kind: "command", Name: c.Executable, ExitCode: int32(result.ExitCode), Stdout: result.Stdout, Stderr: result.Stderr, DurationMs: result.Duration.Milliseconds(), Status: "SUCCEEDED"}
		if runErr != nil || result.TimedOut || result.ExitCode != 0 {
			step.Status = "FAILED"
			response.Status = "FAILED"
			response.ErrorCode = fmt.Sprintf("command_failed:%s", c.Executable)
			if patchesApplied {
				_ = s.transaction.Restore()
			}
		}
		response.Steps = append(response.Steps, step)
	}
	s.idem.Complete(req.RequestId, response)
	return response, nil
}
