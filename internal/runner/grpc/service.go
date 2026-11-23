package grpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/aicouncil/aicouncil/internal/storage/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Service struct {
	runnerv1.UnimplementedWorkspaceRunnerServer
	root           string
	guard          *pathguard.Guard
	signer         *approval.Signer
	db             *gorm.DB
	executor       *command.Executor
	idem           *idempotency.Store
	collector      *runnercontext.Collector
	newTransaction func() transaction
	registry       *WorkspaceRegistry
}

type workspaceRuntime struct {
	root           string
	guard          *pathguard.Guard
	executor       *command.Executor
	collector      *runnercontext.Collector
	newTransaction func() transaction
}

type transaction interface {
	Apply(context.Context, []schema.Patch) ([]files.Snapshot, error)
	Restore() error
}

func NewService(root string) (*Service, error) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		return nil, err
	}
	return NewServiceWithDB(root, db)
}
func NewServiceWithDB(root string, db *gorm.DB) (*Service, error) {
	registry, err := NewWorkspaceRegistry(root, db)
	if err != nil {
		return nil, err
	}
	guard, err := pathguard.New(root, 16<<20)
	if err != nil {
		return nil, err
	}
	idem := idempotency.New()
	if db != nil {
		idem = idempotency.NewWithDB(db)
	}
	return &Service{root: guard.Root(), guard: guard, db: db, executor: command.NewExecutor(guard), idem: idem, collector: runnercontext.NewCollector(runnercontext.Limits{}), newTransaction: func() transaction { return files.NewTransaction(guard) }, registry: registry}, nil
}

// WithSigner installs the shared approval signing key. When nil, ExecuteApprovedPlan
// fails closed: an unsigned deployment must not execute approved plans.
func (s *Service) WithSigner(signer *approval.Signer) *Service { s.signer = signer; return s }

func (s *Service) RegisterWorkspace(ctx context.Context, req *runnerv1.RegisterWorkspaceRequest) (*runnerv1.RegisterWorkspaceResponse, error) {
	root, err := s.registry.Register(req.GetWorkspaceId(), req.GetRelativePath())
	if err != nil {
		if errors.Is(err, ErrWorkspacePathOutsideRoot) || errors.Is(err, ErrWorkspaceIDRebound) || errors.Is(err, ErrWorkspacePathAlreadyRegistered) {
			return nil, status.Error(codes.InvalidArgument, "workspace registration rejected")
		}
		return nil, status.Error(codes.Internal, "workspace registration failed")
	}
	stateRoot, isGit, dirty := workspaceState(ctx, root)
	resp := &runnerv1.RegisterWorkspaceResponse{Root: stateRoot, IsGit: isGit, Dirty: dirty}
	resp.DetectedStacks = detectedStacks(root)
	return resp, nil
}

func (s *Service) DescribeWorkspace(ctx context.Context, req *runnerv1.DescribeWorkspaceRequest) (*runnerv1.DescribeWorkspaceResponse, error) {
	runtime, err := s.runtimeForWorkspace(req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	root, isGit, dirty := workspaceState(ctx, runtime.root)
	resp := &runnerv1.DescribeWorkspaceResponse{Root: root, IsGit: isGit, Dirty: dirty}
	resp.DetectedStacks = detectedStacks(runtime.root)
	return resp, nil
}

func detectedStacks(root string) []string {
	var stacks []string
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		stacks = append(stacks, "go")
	}
	if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
		stacks = append(stacks, "node")
	}
	return stacks
}

func (s *Service) ReadWorkspaceContext(ctx context.Context, req *runnerv1.ReadWorkspaceContextRequest) (*runnerv1.ReadWorkspaceContextResponse, error) {
	runtime, err := s.runtimeForWorkspace(req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	snapshot, err := runtime.collector.Collect(ctx, runtime.root)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, err.Error())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "collect workspace context: %v", err)
	}
	root, isGit, dirty := workspaceState(ctx, runtime.root)
	resp := &runnerv1.ReadWorkspaceContextResponse{Root: root, IsGit: isGit, Dirty: dirty}
	for _, file := range snapshot.Files {
		resp.Files = append(resp.Files, &runnerv1.WorkspaceFile{Path: file.Path, Content: file.Content})
	}
	return resp, nil
}

func workspaceState(ctx context.Context, root string) (string, bool, bool) {
	isGit, dirty := false, false
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		isGit = true
		cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain")
		if out, e := cmd.Output(); e == nil {
			dirty = len(strings.TrimSpace(string(out))) > 0
		}
	}
	return root, isGit, dirty
}

func (s *Service) runtimeForWorkspace(id string) (*workspaceRuntime, error) {
	root, err := s.registry.Resolve(id)
	if err != nil {
		if errors.Is(err, ErrWorkspaceNotRegistered) {
			return nil, status.Error(codes.NotFound, "workspace not registered")
		}
		return nil, status.Error(codes.PermissionDenied, "workspace path is no longer safe")
	}
	if filepath.Clean(root) == filepath.Clean(s.root) {
		return &workspaceRuntime{root: root, guard: s.guard, executor: s.executor, collector: s.collector, newTransaction: s.newTransaction}, nil
	}
	guard, err := pathguard.New(root, 16<<20)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "workspace path is unavailable")
	}
	return &workspaceRuntime{root: root, guard: guard, executor: command.NewExecutor(guard), collector: runnercontext.NewCollector(runnercontext.Limits{}), newTransaction: func() transaction { return files.NewTransaction(guard) }}, nil
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
	runtime, err := s.runtimeForWorkspace(req.GetWorkspaceId())
	if err != nil {
		return nil, err
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
	for _, c := range req.VerificationCommands {
		plan.VerificationCommands = append(plan.VerificationCommands, schema.Command{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, TimeoutSeconds: int(c.TimeoutSeconds), Purpose: c.Purpose})
	}
	if plan.Patches == nil {
		plan.Patches = []schema.Patch{}
	}
	if plan.Commands == nil {
		plan.Commands = []schema.Command{}
	}
	if s.signer == nil {
		return nil, status.Error(codes.FailedPrecondition, "approval signing key is not configured")
	}
	if req.ExpiresAt <= 0 || time.Now().Unix() > req.ExpiresAt {
		return nil, status.Error(codes.PermissionDenied, "approval expired")
	}
	if strings.TrimSpace(req.Nonce) == "" {
		return nil, status.Error(codes.PermissionDenied, "approval nonce required")
	}
	if err := s.signer.Verify(req.ApprovalHash, req.RunId, req.WorkspaceId, int(req.PlanVersion), plan, req.ExpiresAt, req.Nonce); err != nil {
		return nil, status.Error(codes.PermissionDenied, "approval signature invalid")
	}
	// Consume the one-time nonce atomically; a duplicate insert means replay.
	if s.db != nil {
		if err := s.db.WithContext(ctx).Create(&sqlite.ApprovalNonceRecord{Nonce: req.Nonce, ConsumedAt: time.Now().UTC()}).Error; err != nil {
			return nil, status.Error(codes.PermissionDenied, "approval nonce already used")
		}
	}
	response := &runnerv1.ExecuteApprovedPlanResponse{RequestId: req.RequestId, Status: "SUCCEEDED"}
	transaction := runtime.newTransaction()
	patchesApplied := len(plan.Patches) > 0
	if patchesApplied {
		if _, err := transaction.Apply(ctx, plan.Patches); err != nil {
			response.Status = "FAILED"
			response.ErrorCode = "patch_failed"
			var rollbackErr *files.RollbackError
			if errors.As(err, &rollbackErr) {
				response.ErrorCode = "rollback_failed:patch_failed"
			}
			s.idem.Complete(req.RequestId, response)
			return response, nil
		}
	}
	runCommands := func(commands []schema.Command, kind string) bool {
		for _, c := range commands {
			result, runErr := runtime.executor.Run(ctx, command.Spec{Executable: c.Executable, Args: c.Args, WorkDir: c.WorkDir, Timeout: time.Duration(c.TimeoutSeconds) * time.Second, OutputLimit: 1 << 20})
			step := &runnerv1.StepResult{Kind: kind, Name: c.Executable, ExitCode: int32(result.ExitCode), Stdout: result.Stdout, Stderr: result.Stderr, DurationMs: result.Duration.Milliseconds(), Status: "SUCCEEDED"}
			if runErr != nil || result.TimedOut || result.ExitCode != 0 {
				step.Status = "FAILED"
				response.Status = "FAILED"
				response.ErrorCode = fmt.Sprintf("%s_failed:%s", kind, c.Executable)
				response.Steps = append(response.Steps, step)
				if patchesApplied {
					if err := transaction.Restore(); err != nil {
						response.ErrorCode = fmt.Sprintf("rollback_failed:%s", response.ErrorCode)
					}
				}
				return false
			}
			response.Steps = append(response.Steps, step)
		}
		return true
	}
	if runCommands(plan.Commands, "command") {
		runCommands(plan.VerificationCommands, "verification")
	}
	s.idem.Complete(req.RequestId, response)
	return response, nil
}
