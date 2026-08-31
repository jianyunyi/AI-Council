# AI Council v0.1 Phase 4: Web Console and End-to-End Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the Council through a resumable go-zero API and deliver the Next.js console for provider setup, workspace selection, deliberation, approval, execution, and verification.

**Architecture:** The go-zero transport maps HTTP requests to application services and emits ordered SSE events stored in SQLite. The Next.js client uses typed fetch functions and an EventSource-compatible reconnect loop. Playwright drives one complete fake-provider task against a disposable local repository.

**Tech Stack:** Go, go-zero REST, GORM, SSE, Next.js App Router, React, TypeScript, pnpm, Vitest, Testing Library, Playwright

---

## Locked file structure

```text
internal/app/task/service.go
internal/app/task/service_test.go
internal/storage/sqlite/approval_repository.go
internal/transport/council/routes.go
internal/transport/council/routes_test.go
internal/transport/council/sse.go
internal/transport/council/sse_test.go
internal/storage/sqlite/event_repository.go
web/package.json
web/app/layout.tsx
web/app/page.tsx
web/app/providers/page.tsx
web/app/workspaces/page.tsx
web/app/tasks/new/page.tsx
web/app/tasks/[id]/page.tsx
web/components/task/task-workbench.tsx
web/components/task/approval-panel.tsx
web/lib/api.ts
web/lib/events.ts
web/lib/types.ts
web/tests/*.test.tsx
web/e2e/council.spec.ts
samples/go-service/*
samples/next-app/*
README.md
```

### Task 1: Add the application service and resumable event store

**Files:**
- Create: `internal/storage/sqlite/event_repository.go`
- Create: `internal/storage/sqlite/approval_repository.go`
- Create: `internal/app/task/service_test.go`
- Create: `internal/app/task/service.go`

- [ ] **Step 1: Write failing lifecycle tests**

Use fake Council and Runner ports. Assert that creating a task writes `task.created`; starting analysis progresses through each Council state; requesting execution without approval returns `ErrApprovalRequired`; approval records the snapshot hash and plan version before gRPC execution; a second approval for a changed plan invalidates the first; failed verification transitions once through Replanning and a second failure becomes Failed; events have strictly increasing per-run sequence numbers.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/app/task -v`

Expected: FAIL because `Service` is undefined.

- [ ] **Step 3: Implement the application ports and service**

```go
type WorkspaceDescription struct { Root string; IsGit bool; Dirty bool; DetectedStacks []string }
type ApprovedExecution struct { RequestID, RunID, WorkspaceID, ApprovalHash string; Plan schema.ExecutionPlan }
type Event struct { RunID string; Sequence int64; Type string; Data json.RawMessage; CreatedAt time.Time }
type CouncilPort interface { Analyze(context.Context, string) error; Deliberate(context.Context, string) (schema.ExecutionPlan, error); ReviewExecution(context.Context, string, schema.VerificationReport) error }
type RunnerPort interface { Describe(context.Context, string) (WorkspaceDescription, error); Execute(context.Context, ApprovedExecution) (schema.VerificationReport, error) }
type EventPort interface { Append(context.Context, string, string, any) (Event, error); After(context.Context, string, int64, int) ([]Event, error) }

type Service struct { runs RunPort; approvals ApprovalPort; events EventPort; council CouncilPort; runner RunnerPort; maxReplans int }
```

Persist approvals with an append-only record:

```go
type ApprovalRecord struct { ID string `gorm:"primaryKey"`; RunID string `gorm:"index"`; PlanVersion int; SnapshotHash string; Decision string; Actor string; CreatedAt time.Time; InvalidatedAt *time.Time }
```

`ApprovalPort.Current(runID)` returns only an approved, non-invalidated record matching the current plan version. Reject and plan-replacement actions append audit events and invalidate the prior approval instead of updating its decision in place.

Every public operation loads current state, checks it, performs the domain action, commits the state transition plus audit event, and only then emits the user-facing event. Use idempotency keys on start, approve, and execute operations.

- [ ] **Step 4: Run lifecycle tests**

Run: `go test -race ./internal/app/task -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/app internal/storage/sqlite
git commit -m "feat: orchestrate task lifecycle and ordered events"
```

### Task 2: Expose REST endpoints and resumable SSE

**Files:**
- Create: `internal/transport/council/routes_test.go`
- Create: `internal/transport/council/routes.go`
- Create: `internal/transport/council/sse_test.go`
- Create: `internal/transport/council/sse.go`
- Modify: `internal/transport/council/http.go`
- Modify: `cmd/council-server/main.go`

- [ ] **Step 1: Write failing API tests**

Cover request validation and status codes for provider test, workspace registration, task creation, task read, start, approve, reject, execute, cancel, artifact retrieval, and event stream. SSE tests pass `Last-Event-ID: 7`, assert the first emitted event has ID 8, and assert heartbeats are comments rather than fake domain events.

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/transport/council -run 'Route|SSE' -v`

Expected: FAIL because routes are absent.

- [ ] **Step 3: Register the exact v1 routes**

```text
POST   /api/v1/providers/test
POST   /api/v1/workspaces
GET    /api/v1/workspaces
POST   /api/v1/tasks
GET    /api/v1/tasks/:id
POST   /api/v1/tasks/:id/start
POST   /api/v1/tasks/:id/approve
POST   /api/v1/tasks/:id/reject
POST   /api/v1/tasks/:id/execute
POST   /api/v1/tasks/:id/cancel
GET    /api/v1/tasks/:id/artifacts/:artifactId
GET    /api/v1/tasks/:id/events
```

Use one response envelope:

```go
type Response[T any] struct { Data T `json:"data"`; Error *APIError `json:"error,omitempty"`; RequestID string `json:"request_id"` }
type APIError struct { Code string `json:"code"`; Message string `json:"message"`; Fields map[string]string `json:"fields,omitempty"` }
```

SSE writes `id`, `event`, and JSON `data`, flushes each event, sends a `: heartbeat` comment every 15 seconds, and resumes strictly after `Last-Event-ID`.

- [ ] **Step 4: Run transport tests**

Run: `go test -race ./internal/transport/council -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add cmd/council-server internal/transport/council
git commit -m "feat: expose council rest and resumable sse api"
```

### Task 3: Scaffold the Next.js console and typed API client

**Files:**
- Create: `web/*` from scaffold
- Create: `web/lib/types.ts`
- Create: `web/lib/api.ts`
- Create: `web/lib/events.ts`
- Create: `web/lib/api.test.ts`

- [ ] **Step 1: Scaffold with pnpm**

Run:

```powershell
pnpm create next-app@latest web --ts --eslint --app --no-src-dir --use-pnpm --import-alias "@/*" --no-tailwind
Set-Location web
pnpm add zod
pnpm add -D vitest jsdom @testing-library/react @testing-library/jest-dom @vitejs/plugin-react vite-tsconfig-paths
```

Expected: `web/pnpm-lock.yaml` exists and `pnpm lint` passes.

- [ ] **Step 2: Write a failing API client test**

```ts
import { describe, expect, it, vi } from "vitest";
import { createTask } from "./api";

describe("createTask", () => {
  it("returns typed data and surfaces API errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { id: "task-1", state: "DRAFT" }, request_id: "req-1" }), { status: 201 })));
    await expect(createTask({ workspaceId: "ws-1", requirement: "add health check", acceptance: ["tests pass"] })).resolves.toMatchObject({ id: "task-1" });
  });
});
```

- [ ] **Step 3: Verify failure**

Run: `pnpm --dir web vitest run lib/api.test.ts`

Expected: FAIL because `createTask` is undefined.

- [ ] **Step 4: Implement typed clients**

Define Zod schemas for `Task`, `ArtifactSummary`, `ApprovalView`, `VerificationReport`, and the response envelope. `api.ts` validates all JSON before returning it. `events.ts` tracks the last event ID, reconnects with it as a query parameter when EventSource headers are unavailable, deduplicates by sequence, and exposes an unsubscribe function.

- [ ] **Step 5: Run frontend unit checks**

Run:

```powershell
pnpm --dir web vitest run
pnpm --dir web lint
pnpm --dir web build
```

Expected: all commands exit 0.

- [ ] **Step 6: Commit**

```powershell
git add web
git commit -m "feat: scaffold typed Next.js council console"
```

### Task 4: Build provider, workspace, and task creation pages

**Files:**
- Create: `web/app/providers/page.tsx`
- Create: `web/app/workspaces/page.tsx`
- Create: `web/app/tasks/new/page.tsx`
- Create: `web/components/setup/provider-form.tsx`
- Create: `web/components/setup/workspace-form.tsx`
- Create: `web/components/task/task-form.tsx`
- Create: `web/tests/setup-pages.test.tsx`

- [ ] **Step 1: Write failing interaction tests**

Render each form and verify: API keys use password inputs and are never copied into URL/localStorage; provider test shows a normalized success/error; workspace form displays the Runner-returned canonical root and Git state before authorization; task form requires requirement and at least one acceptance criterion; budget and quorum have bounded defaults.

- [ ] **Step 2: Verify failure**

Run: `pnpm --dir web vitest run tests/setup-pages.test.tsx`

Expected: FAIL because pages and forms do not exist.

- [ ] **Step 3: Implement the setup flow**

Use controlled React forms with Zod validation. Submit API keys directly to `/providers/test`, then send only the returned ephemeral `profile_id` in subsequent requests. Default task values are quorum 2, max rounds 2, max replans 1, and a visible user-entered cost cap. Workspace authorization requires confirming the canonical path returned by Runner.

- [ ] **Step 4: Run tests and build**

Run:

```powershell
pnpm --dir web vitest run tests/setup-pages.test.tsx
pnpm --dir web build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add web/app web/components web/tests
git commit -m "feat: add provider workspace and task setup"
```

### Task 5: Build the live workbench, approval panel, and verification report

**Files:**
- Create: `web/app/tasks/[id]/page.tsx`
- Create: `web/components/task/task-workbench.tsx`
- Create: `web/components/task/timeline.tsx`
- Create: `web/components/task/artifact-view.tsx`
- Create: `web/components/task/approval-panel.tsx`
- Create: `web/components/task/verification-report.tsx`
- Create: `web/tests/task-workbench.test.tsx`

- [ ] **Step 1: Write failing workbench tests**

Assert the three-column layout renders current state, artifact, and usage; proposal cards show aliases during review and provider names only in administrative metadata; SSE sequence duplicates are ignored; approval displays every changed path, full Diff, argv, work directory, timeout, risk, cost, tests, and recovery; approval is disabled if artifact version changes; failed verification offers Replan, Keep Changes, and Restore This Run actions.

- [ ] **Step 2: Verify failure**

Run: `pnpm --dir web vitest run tests/task-workbench.test.tsx`

Expected: FAIL because workbench components do not exist.

- [ ] **Step 3: Implement the workbench**

The server component loads the initial task and artifact summaries. The client workbench subscribes to SSE and updates a reducer keyed by event sequence. Approval requires checking a confirmation box immediately beside the immutable snapshot hash and sends `{plan_version, approval_hash}`. Do not use browser confirmation dialogs as the security boundary; the server validates state and hash again.

- [ ] **Step 4: Run frontend checks**

Run:

```powershell
pnpm --dir web vitest run
pnpm --dir web lint
pnpm --dir web build
```

Expected: all commands exit 0.

- [ ] **Step 5: Commit**

```powershell
git add web
git commit -m "feat: add live council approval workbench"
```

### Task 6: Add samples, end-to-end security scenarios, and operator documentation

**Files:**
- Create: `samples/go-service/go.mod`
- Create: `samples/go-service/main.go`
- Create: `samples/go-service/main_test.go`
- Create: `samples/next-app/package.json`
- Create: `samples/next-app/src/*`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/council.spec.ts`
- Create: `internal/e2e/council_test.go`
- Create: `README.md`

- [ ] **Step 1: Create deterministic sample repositories**

The Go sample exposes `/healthz` but lacks `/readyz`; its failing acceptance test expects `/readyz`. The Next sample has a server action with an intentionally missing input check and a failing unit test. Keep samples dependency-light and committed so E2E runs never modify product source.

- [ ] **Step 2: Write failing end-to-end tests**

The Go E2E test starts fake provider servers, Council Server, and Runner on random loopback ports; registers a copied sample workspace; creates a task; waits for AwaitingApproval; proves the file is unchanged; approves; executes; and asserts tests pass. Add cases for tampered argv, outside-root patch, duplicate request ID, browser reconnect from an event ID, provider quorum loss, and preservation of a pre-existing dirty file.

The Playwright test performs the same happy path through the browser and inspects the approval Diff before clicking Approve.

- [ ] **Step 3: Verify initial failures**

Run:

```powershell
go test ./internal/e2e -v
pnpm --dir web playwright test
```

Expected: FAIL until process wiring and fixtures are complete.

- [ ] **Step 4: Complete process wiring and documentation**

Update both commands to accept config files for database path, artifact path, loopback addresses, Runner pairing token, provider base URLs, and redaction rules. Add `README.md` with prerequisites, `pnpm install`, `go test ./...`, local startup commands, key-lifetime behavior, workspace authorization, approval semantics, data directory, recovery instructions, and troubleshooting for the currently broken global npm by using pnpm exclusively.

- [ ] **Step 5: Run the full release gate**

Run:

```powershell
go test -race ./...
go vet ./...
pnpm --dir web vitest run
pnpm --dir web lint
pnpm --dir web build
pnpm --dir web playwright test
git diff --check
```

Expected: every command exits 0; E2E proves no write before approval and one successful approved change.

- [ ] **Step 6: Commit**

```powershell
git add README.md cmd internal samples web
git commit -m "feat: complete AI Council v0.1 end-to-end workflow"
```

## Phase 4 completion gate

Start Council Server, Workspace Runner, and Web Console locally. Complete one Go and one Next.js task with fake providers, then one manually authorized smoke run with real provider keys. Confirm logs and the SQLite database contain no key material. Record the final verification commands and outcomes in the release notes before tagging v0.1.0.
