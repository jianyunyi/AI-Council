# Real Council Lifecycle E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove that an approved Council plan reaches the real Runner through REST and gRPC exactly once.

**Architecture:** An in-process Runner gRPC server works on a temporary workspace. A persistent Council API uses its generated client and a deterministic Provider-backed workflow. HTTP handler calls perform create, start, approve, execute, and read operations against SQLite.

**Tech Stack:** Go, httptest, gRPC, GORM/SQLite, existing Council workflow and Runner service.

---

### Task 1: Prove the real lifecycle

**Files:**
- Modify: `internal/e2e/council_test.go`

- [ ] **Step 1: Write the failing real lifecycle test**

Add `TestCouncilApprovalExecutionVerificationLifecycle`. Create `message.txt` with `before\n`, then invoke the Council handlers to create, start, approve, and execute a task.

```go
require.Equal(t, http.StatusOK, execute(t, api, id).Code)
require.Equal(t, "council\n", readFile(t, filepath.Join(root, "message.txt")))
require.Equal(t, "SUCCEEDED", getTask(t, api, id).State)
```

- [ ] **Step 2: Confirm the test fails before fixture wiring**

Run: `go test ./internal/e2e -run TestCouncilApprovalExecutionVerificationLifecycle -count=1`

Expected: FAIL because the current E2E adapter bypasses the HTTP lifecycle.

- [ ] **Step 3: Add a real Runner fixture**

Create `runnergrpc.NewService(root)`, register it on an in-process `grpc.Server`, dial it with a generated `runnerv1.WorkspaceRunnerClient`, and inject that client through `NewPersistentAPI(db).WithRunnerClient(client)`. Clean up server and client with `t.Cleanup`. Do not mock the Runner.

- [ ] **Step 4: Add deterministic Provider responses**

Create a test-only Provider returning JSON in this order: proposal, approving review, decision, red-team report. The decision contains this patch and two `go version` commands, one normal and one verification command.

```go
schema.Patch{Path: "message.txt", UnifiedDiff: "@@ -1 +1 @@\n-before\n+council\n", BeforeHash: sha256Hex("before\n")}
```

Construct `council.NewWorkflow(...)` from that registry and supply it with `api.WithCouncil(workflow)`.

- [ ] **Step 5: Make the test pass using HTTP only**

Use `httptest` and route handlers for task creation, start, approval with returned hash, execution, and readback. Assert Runner steps include a verification step.

Run: `go test ./internal/e2e -run TestCouncilApprovalExecutionVerificationLifecycle -count=1`

Expected: PASS.

- [ ] **Step 6: Assert exactly-once consumption**

Call execute a second time and assert HTTP 409 and unchanged file content.

```go
require.Equal(t, http.StatusConflict, execute(t, api, id).Code)
require.Equal(t, "council\n", readFile(t, filepath.Join(root, "message.txt")))
```

- [ ] **Step 7: Run E2E regression**

Run: `go test ./internal/e2e -count=1`

Expected: PASS.

### Task 2: Document runtime boundaries

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document five execution boundaries**

Add `## Execution safety boundaries` covering: bounded Runner-filtered workspace snapshots only; exclusion of sensitive, binary, symlink, and oversized files; explicit approval with plan hash; one-time approval consumption; and no automatic replan/retry after failure.

- [ ] **Step 2: Verify documentation**

Run: `rg -n "Execution safety boundaries|consumed once|automatic" README.md`

Expected: the heading and execution rules are present.

### Task 3: Verify the phase

**Files:**
- Modify: none

- [ ] **Step 1: Run focused backend verification**

Run: `go test ./internal/approval ./internal/app/task ./internal/council ./internal/e2e ./internal/runner/files ./internal/runner/grpc ./internal/storage/sqlite ./internal/transport/council -count=1`

Expected: PASS.

- [ ] **Step 2: Check formatting and diff integrity**

Run: `gofmt -w internal/e2e/council_test.go` followed by `git diff --check`.

Expected: both exit with code 0.

- [ ] **Step 3: Record repository-wide dependency blocker if present**

Run: `go test ./... -count=1`.

Expected: cached packages pass. If Gin/Wails cannot download because the host blocks the module proxy, record the exact environment error and do not change dependencies or project proxy settings.
