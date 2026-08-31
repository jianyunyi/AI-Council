# AI Council v0.1 Phase 3: Approval-Bound Workspace Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local Runner that grants read-only project analysis by default and performs file writes or commands only when the gRPC request exactly matches a user-approved snapshot.

**Architecture:** The Runner owns path resolution, sensitive-file policy, file snapshots, patch application, command execution, verification, and idempotency. Council Server owns approval creation. A versioned protobuf contract carries plan hashes and result evidence across the boundary.

**Tech Stack:** Go, gRPC/protobuf, Gin, GORM, SQLite, os/exec, go-git or Git CLI read-only operations, testify

---

## Locked file structure

```text
proto/runner/v1/runner.proto
internal/runner/rpc/generated/*
internal/approval/snapshot.go
internal/approval/snapshot_test.go
internal/runner/pairing/service.go
internal/runner/pairing/service_test.go
internal/runner/pathguard/guard.go
internal/runner/pathguard/guard_test.go
internal/runner/files/transaction.go
internal/runner/files/transaction_test.go
internal/runner/command/executor.go
internal/runner/command/executor_test.go
internal/runner/verification/service.go
internal/runner/verification/service_test.go
internal/runner/idempotency/store.go
internal/runner/grpc/service.go
internal/runner/grpc/service_test.go
```

### Task 1: Define the Runner gRPC protocol

**Files:**
- Create: `proto/runner/v1/runner.proto`
- Generate: `internal/runner/rpc/generated/runner.pb.go`
- Generate: `internal/runner/rpc/generated/runner_grpc.pb.go`

- [ ] **Step 1: Write the protocol**

```proto
syntax = "proto3";

package aicouncil.runner.v1;
option go_package = "github.com/aicouncil/aicouncil/internal/runner/rpc/generated;runnerv1";

service WorkspaceRunner {
  rpc DescribeWorkspace(DescribeWorkspaceRequest) returns (DescribeWorkspaceResponse);
  rpc ExecuteApprovedPlan(ExecuteApprovedPlanRequest) returns (ExecuteApprovedPlanResponse);
  rpc GetExecution(GetExecutionRequest) returns (ExecuteApprovedPlanResponse);
}

message DescribeWorkspaceRequest { string workspace_id = 1; }
message DescribeWorkspaceResponse { string root = 1; bool is_git = 2; bool dirty = 3; repeated string detected_stacks = 4; }
message ApprovedPatch { string path = 1; string unified_diff = 2; string before_hash = 3; }
message ApprovedCommand { string executable = 1; repeated string args = 2; string work_dir = 3; int32 timeout_seconds = 4; string purpose = 5; }
message ExecuteApprovedPlanRequest {
  string request_id = 1;
  string run_id = 2;
  string workspace_id = 3;
  int32 plan_version = 4;
  repeated ApprovedPatch patches = 5;
  repeated ApprovedCommand commands = 6;
  repeated string acceptance = 7;
  string approval_hash = 8;
}
message StepResult { string kind = 1; string name = 2; int32 exit_code = 3; string stdout = 4; string stderr = 5; int64 duration_ms = 6; string status = 7; }
message ExecuteApprovedPlanResponse { string request_id = 1; string status = 2; repeated StepResult steps = 3; string actual_diff = 4; string error_code = 5; }
message GetExecutionRequest { string request_id = 1; }
```

- [ ] **Step 2: Generate Go code**

Run:

```powershell
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
protoc --go_out=. --go_opt=module=github.com/aicouncil/aicouncil --go-grpc_out=. --go-grpc_opt=module=github.com/aicouncil/aicouncil proto/runner/v1/runner.proto
```

Expected: generated files appear under `internal/runner/rpc/generated`.

- [ ] **Step 3: Compile the contract**

Run: `go test ./internal/runner/rpc/generated`

Expected: PASS with no test files.

- [ ] **Step 4: Commit**

```powershell
git add proto internal/runner/rpc go.mod go.sum
git commit -m "feat: define workspace runner grpc contract"
```

### Task 2: Pair the local Runner with one-time credentials

**Files:**
- Create: `internal/runner/pairing/service_test.go`
- Create: `internal/runner/pairing/service.go`
- Modify: `internal/transport/runner/http.go`
- Modify: `cmd/workspace-runner/main.go`

- [ ] **Step 1: Write failing pairing tests**

Use a fake clock and deterministic random reader. Assert that a six-character pairing code expires after five minutes, can be exchanged only once, returns a 32-byte URL-safe bearer token, never appears in a response after exchange, and rejects non-loopback HTTP requests. Assert the gRPC unary interceptor accepts `authorization: Bearer <token>` and rejects missing or incorrect tokens.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/runner/pairing ./internal/transport/runner -run Pair -v`

Expected: FAIL because the pairing service and routes do not exist.

- [ ] **Step 3: Implement one-time pairing**

```go
type Service struct {
	mu sync.Mutex
	now func() time.Time
	random io.Reader
	codeHash [32]byte
	expiresAt time.Time
	tokenHash [32]byte
}

func (s *Service) Start() (string, time.Time, error) {
	code, err := randomCode(s.random, 6)
	if err != nil { return "", time.Time{}, err }
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codeHash = sha256.Sum256([]byte(code))
	s.expiresAt = s.now().Add(5 * time.Minute)
	s.tokenHash = [32]byte{}
	return code, s.expiresAt, nil
}

func (s *Service) Exchange(code string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	providedHash := sha256.Sum256([]byte(code))
	if s.now().After(s.expiresAt) || subtle.ConstantTimeCompare(s.codeHash[:], providedHash[:]) != 1 { return "", ErrInvalidCode }
	token, err := randomToken(s.random, 32)
	if err != nil { return "", err }
	s.codeHash = [32]byte{}
	s.tokenHash = sha256.Sum256([]byte(token))
	return token, nil
}
```

Expose `POST /pairing/start` and `POST /pairing/exchange` only when `RemoteAddr` is loopback. Keep the token only in process memory in v0.1. Add a gRPC unary interceptor that validates the constant-time token hash before dispatch.

- [ ] **Step 4: Run pairing tests**

Run: `go test -race ./internal/runner/pairing ./internal/transport/runner -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add cmd/workspace-runner internal/runner/pairing internal/transport/runner
git commit -m "feat: pair local runner with one-time credentials"
```

### Task 3: Create canonical approval snapshots

**Files:**
- Create: `internal/approval/snapshot_test.go`
- Create: `internal/approval/snapshot.go`

- [ ] **Step 1: Write failing tamper tests**

```go
package approval

import (
	"testing"

	"github.com/aicouncil/aicouncil/internal/council/schema"
	"github.com/stretchr/testify/require"
)

func TestVerifyRejectsEveryMaterialChange(t *testing.T) {
	plan := schema.ExecutionPlan{Version: 3, Patches: []schema.Patch{{Path: "main.go", UnifiedDiff: "patch", BeforeHash: "before"}}, Commands: []schema.Command{{Executable: "go", Args: []string{"test", "./..."}, WorkDir: ".", TimeoutSeconds: 60}}}
	hash, err := Hash("run-1", "workspace-1", plan)
	require.NoError(t, err)
	require.NoError(t, Verify(hash, "run-1", "workspace-1", plan))

	plan.Commands[0].Args = []string{"env"}
	require.ErrorIs(t, Verify(hash, "run-1", "workspace-1", plan), ErrHashMismatch)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/approval -v`

Expected: FAIL because `Hash` and `Verify` do not exist.

- [ ] **Step 3: Implement canonical hashing**

```go
package approval

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/aicouncil/aicouncil/internal/council/schema"
)

var ErrHashMismatch = errors.New("approval hash mismatch")

type payload struct { RunID string `json:"run_id"`; WorkspaceID string `json:"workspace_id"`; Plan schema.ExecutionPlan `json:"plan"` }

func Hash(runID, workspaceID string, plan schema.ExecutionPlan) (string, error) {
	raw, err := json.Marshal(payload{RunID: runID, WorkspaceID: workspaceID, Plan: plan})
	if err != nil { return "", err }
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func Verify(want, runID, workspaceID string, plan schema.ExecutionPlan) error {
	got, err := Hash(runID, workspaceID, plan)
	if err != nil { return err }
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 { return ErrHashMismatch }
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/approval -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/approval
git commit -m "feat: bind execution to approval snapshot hashes"
```

### Task 4: Enforce workspace roots and sensitive-file rules

**Files:**
- Create: `internal/runner/pathguard/guard_test.go`
- Create: `internal/runner/pathguard/guard.go`

- [ ] **Step 1: Write failing boundary tests**

Use `t.TempDir()` to create a workspace, a normal Go file, `.env`, `id_rsa`, a file above the root, and a symlink or Windows directory junction when supported. Assert the guard allows the Go file and rejects traversal, absolute outside paths, sensitive names, links escaping root, database extensions, and files above the configured size.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/runner/pathguard -v`

Expected: FAIL because `Guard` is undefined.

- [ ] **Step 3: Implement guarded resolution**

```go
type Guard struct { root string; maxBytes int64 }

func New(root string, maxBytes int64) (*Guard, error) {
	abs, err := filepath.Abs(root)
	if err != nil { return nil, err }
	real, err := filepath.EvalSymlinks(abs)
	if err != nil { return nil, err }
	return &Guard{root: filepath.Clean(real), maxBytes: maxBytes}, nil
}

func (g *Guard) ResolveRead(relative string) (string, error) {
	if filepath.IsAbs(relative) { return "", ErrOutsideWorkspace }
	candidate := filepath.Join(g.root, filepath.Clean(relative))
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil { return "", err }
	rel, err := filepath.Rel(g.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) { return "", ErrOutsideWorkspace }
	if isSensitive(rel) { return "", ErrSensitivePath }
	info, err := os.Stat(real)
	if err != nil { return "", err }
	if info.Size() > g.maxBytes { return "", ErrFileTooLarge }
	return real, nil
}
```

Implement a separate `ResolveWrite` that validates the parent real path, rejects sensitive paths, and allows a not-yet-created final file only inside the root.

- [ ] **Step 4: Run boundary tests**

Run: `go test ./internal/runner/pathguard -v`

Expected: PASS; unsupported link creation is skipped with the OS reason.

- [ ] **Step 5: Commit**

```powershell
git add internal/runner/pathguard
git commit -m "feat: enforce workspace and sensitive path boundaries"
```

### Task 5: Apply patches transactionally without overwriting user work

**Files:**
- Create: `internal/runner/files/transaction_test.go`
- Create: `internal/runner/files/transaction.go`

- [ ] **Step 1: Write failing file-transaction tests**

Cover: before-hash mismatch rejects before any write; two-file patch with second failure restores the first; a successful patch returns before/after hashes; restore affects only task files; pre-existing uncommitted content remains intact; a non-Git directory behaves the same.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/runner/files -v`

Expected: FAIL because `Transaction` is undefined.

- [ ] **Step 3: Implement snapshots and atomic replacement**

```go
type Snapshot struct { Path string; Existed bool; Mode fs.FileMode; Content []byte; Hash string }
type Transaction struct { guard *pathguard.Guard; snapshots []Snapshot }

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func replaceFile(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aicouncil-*")
	if err != nil { return err }
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil { tmp.Close(); return err }
	if err = tmp.Sync(); err != nil { tmp.Close(); return err }
	if err = tmp.Chmod(mode); err != nil { tmp.Close(); return err }
	if err = tmp.Close(); err != nil { return err }
	return os.Rename(name, path)
}
```

Use a Go unified-diff parser to compute new content in memory. Verify all `BeforeHash` values and parse all patches before the first write. If a write fails, restore prior snapshots in reverse order. Never call destructive Git restore commands.

- [ ] **Step 4: Run file tests**

Run: `go test ./internal/runner/files -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/runner/files go.mod go.sum
git commit -m "feat: add transactional approved patch application"
```

### Task 6: Execute approved argv without invoking a shell

**Files:**
- Create: `internal/runner/command/executor_test.go`
- Create: `internal/runner/command/executor.go`

- [ ] **Step 1: Write failing process tests**

Use a small Go test helper process. Assert executable plus argv are preserved literally, shell metacharacters are not expanded, work directories remain inside the workspace, timeout kills the child, combined output is capped, environment excludes known secret variables, and cancellation returns a typed result.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/runner/command -v`

Expected: FAIL because `Executor` is undefined.

- [ ] **Step 3: Implement direct execution**

```go
type Spec struct { Executable string; Args []string; WorkDir string; Timeout time.Duration; OutputLimit int64 }
type Result struct { ExitCode int; Stdout, Stderr string; Duration time.Duration; TimedOut bool }

func (e *Executor) Run(ctx context.Context, spec Spec) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	workDir, err := e.guard.ResolveDirectory(spec.WorkDir)
	if err != nil { return Result{}, err }
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = workDir
	cmd.Env = e.safeEnvironment()
	stdout, stderr := newLimitBuffers(spec.OutputLimit), newLimitBuffers(spec.OutputLimit)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	err = cmd.Run()
	return normalizeResult(err, stdout.String(), stderr.String(), time.Since(started), ctx.Err()), nil
}
```

Do not add `cmd.exe`, `powershell`, `sh`, or `bash` automatically. If a future plan explicitly approves a shell, it must appear as the executable and its script content must be part of the approval hash.

- [ ] **Step 4: Run process tests**

Run: `go test ./internal/runner/command -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/runner/command
git commit -m "feat: execute approved commands without shell expansion"
```

### Task 7: Add idempotent gRPC execution and deterministic verification

**Files:**
- Create: `internal/runner/idempotency/store.go`
- Create: `internal/runner/verification/service_test.go`
- Create: `internal/runner/verification/service.go`
- Create: `internal/runner/grpc/service_test.go`
- Create: `internal/runner/grpc/service.go`
- Modify: `cmd/workspace-runner/main.go`

- [ ] **Step 1: Write failing service tests**

Assert: an invalid approval hash is rejected before writes; repeated `request_id` returns the saved response and does not run twice; simultaneous duplicate requests have one winner; actual Diff outside approved paths fails verification; Go detection selects `go test ./...` only when it appears in the approved plan; Node detection selects only approved pnpm commands; Runner restart can return a stored completed response.

- [ ] **Step 2: Verify failure**

Run: `go test -race ./internal/runner/... -run 'Approval|Idempotent|Verification|Restart' -v`

Expected: FAIL because the service and idempotency store do not exist.

- [ ] **Step 3: Implement the orchestration boundary**

`ExecuteApprovedPlan` must perform these operations in order:

```go
func (s *Service) ExecuteApprovedPlan(ctx context.Context, req *runnerv1.ExecuteApprovedPlanRequest) (*runnerv1.ExecuteApprovedPlanResponse, error) {
	if saved, ok := s.idempotency.Get(req.RequestId); ok { return saved, nil }
	release, err := s.idempotency.Begin(req.RequestId)
	if err != nil { return nil, status.Error(codes.Aborted, "execution already in progress") }
	defer release()
	plan, err := decodePlan(req)
	if err != nil { return nil, status.Error(codes.InvalidArgument, "invalid plan") }
	if err := approval.Verify(req.ApprovalHash, req.RunId, req.WorkspaceId, plan); err != nil { return nil, status.Error(codes.PermissionDenied, "approval mismatch") }
	result := s.executeAndVerify(ctx, req, plan)
	if err := s.idempotency.Complete(req.RequestId, result); err != nil { return nil, status.Error(codes.Internal, "cannot persist execution result") }
	return result, nil
}
```

Use SQLite unique `request_id` plus a transaction for `Begin`. Persist normalized results before returning. Verification compares actual changed paths with approved paths and records every command result; AI review occurs later in Council Server and cannot override deterministic failures.

- [ ] **Step 4: Run all Phase 3 checks**

Run:

```powershell
go test -race ./internal/approval/... ./internal/runner/...
go vet ./...
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit**

```powershell
git add cmd/workspace-runner internal/runner go.mod go.sum
git commit -m "feat: add secure idempotent workspace execution"
```

## Phase 3 completion gate

Run `go test -race ./...`. Start the Runner against a disposable sample repository. Demonstrate that read-only description works, execution without approval fails, a tampered command fails, one approved patch runs once, duplicate delivery returns the stored result, and pre-existing changes remain intact.
