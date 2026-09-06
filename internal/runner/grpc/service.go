package grpc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/approval"
	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/aicouncil/aicouncil/internal/runner/command"
	runnercontext "github.com/aicouncil/aicouncil/internal/runner/context"
	"github.com/aicouncil/aicouncil/internal/runner/files"
	"github.com/aicouncil/aicouncil/internal/runner/idempotency"
	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Service struct {
	runnerv1.UnimplementedWorkspaceRunnerServer
	root        string
	guard       *pathguard.Guard
	executor    *command.Executor
	transaction *files.Transaction
	idem        *idempotency.Store
	collector   *runnercontext.Collector
}

func NewService(root string) (*Service, error) {
	return NewServiceWithDB(root, nil)
}
func NewServiceWithDB(root string, db *gorm.DB) (*Service, error) {
	guard, err := pathguard.New(root, 16<<20)
	if err != nil {
		return nil, err
	}
	idem := idempotency.New()
	if db != nil {
		idem = idempotency.NewWithDB(db)
	}
	return &Service{root: guard.Root(), guard: guard, executor: command.NewExecutor(guard), transaction: files.NewTransaction(guard), idem: idem, collector: runnercontext.NewCollector(runnercontext.Limits{})}, nil
}
func (s *Service) DescribeWorkspace(ctx context.Context, _ *runnerv1.DescribeWorkspaceRequest) (*runnerv1.DescribeWorkspaceResponse, error) {
	root, isGit, dirty := s.workspaceState(ctx)
	resp := &runnerv1.DescribeWorkspaceResponse{Root: root, IsGit: isGit, Dirty: dirty}
	if _, err := os.Stat(s.root + string(os.PathSeparator) + "go.mod"); err == nil {
		resp.DetectedStacks = append(resp.DetectedStacks, "go")
	}
	if _, err := os.Stat(s.root + string(os.PathSeparator) + "package.json"); err == nil {
		resp.DetectedStacks = append(resp.DetectedStacks, "node")
	}
	return resp, nil
}

func (s *Service) ReadWorkspaceContext(ctx context.Context, _ *runnerv1.ReadWorkspaceContextRequest) (*runnerv1.ReadWorkspaceContextResponse, error) {
	snapshot, err := s.collector.Collect(ctx, s.root)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "collect workspace context: %v", err)
	}
	root, isGit, dirty := s.workspaceState(ctx)
	resp := &runnerv1.ReadWorkspaceContextResponse{Root: root, IsGit: isGit, Dirty: dirty}
	for _, file := range snapshot.Files {
		resp.Files = append(resp.Files, &runnerv1.WorkspaceFile{Path: file.Path, Content: file.Content})
	}
	return resp, nil
}

func (s *Service) workspaceState(ctx context.Context) (root string, isGit bool, dirty bool) {
	root = s.root
	if _, err := os.Stat(s.root + string(os.PathSeparator) + ".git"); err == nil {
		isGit = true
		cmd := exec.CommandContext(ctx, "git", "-C", s.root, "status", "--porcelain")
		if out, e := cmd.Output(); e == nil {
			dirty = len(strings.TrimSpace(string(out))) > 0
		}
	}
	return root, isGit, dirty
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
	if plan.Acceptance == nil {
		plan.Acceptance = []string{}
	}
	for _, p := range req.Patches {
		plan.Patches = append(plan.Patches, schema.Patch{Path: p.Path, UnifiedDiff: p.UnifiedDiff, BeforeHash: p.BeforeHash})
	}
	for _, c := range req.Commands {
		plan.Commands = append(plan.Commands, schema.Command{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int(c.TimeoutSeconds), Purpose: c.Purpose})
	}
	if plan.Patches == nil {
		plan.Patches = []schema.Patch{}
	}
	if plan.Commands == nil {
		plan.Commands = []schema.Command{}
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
