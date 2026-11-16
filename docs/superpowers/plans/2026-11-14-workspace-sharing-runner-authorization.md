# Workspace Sharing and Runner Authorization Implementation Plan

> **For agentic workers:** Execute each checked task in order. Preserve existing uncommitted user changes; do not commit in this implementation pass.

**Goal:** Add owner-managed viewer/editor workspace shares and make Runner resolve every request through a persistent, path-contained workspace registry.

**Architecture:** Council stores shares and checks effective workspace access on each REST operation. Runner stores opaque workspace ID to canonical-root bindings in its own SQLite database, accepts registrations only from authenticated Council gRPC calls, and rejects unknown IDs. A shared workspace does not expose a user's Provider Keys or transfer task ownership.

**Tech Stack:** Go, Gin/go-zero REST transport, gRPC/protobuf, GORM/SQLite, Next.js/React, Vitest.

---

## File map

- `internal/storage/sqlite/models.go`, `db.go`: persistent share and Runner workspace registry records.
- `proto/runner/v1/runner.proto`, `internal/runner/rpc/generated/*`: registration RPC and generated client/server code.
- `internal/runner/grpc/service.go`, tests: per-workspace runtime resolution, canonical path containment, registration persistence.
- `cmd/workspace-runner/main.go`, `deploy/docker-compose.yml`: configure Runner's allowed root as `/workspace`.
- `internal/transport/council/routes.go`, `sse.go`, `auth.go`, new `workspace_shares.go`: safe relative workspace registration and effective access enforcement.
- `internal/storage/sqlite/*_test.go`, `internal/transport/council/*_test.go`: persistence, permissions, revocation, and lifecycle cases.
- `web/lib/api.ts`, `web/app/workspaces/page.tsx`, new page test: owner share controls and shared-workspace read-only behavior.
- `internal/e2e/*`: share → editor task → approval → Runner → revoke access scenario.
- `docs/operations/single-host.md`: workspace directory provisioning and upgrade notes.

## Task 1: Add Runner workspace registry persistence and path validation

**Files:**
- Modify: `internal/storage/sqlite/models.go`
- Modify: `internal/storage/sqlite/db.go`
- Create: `internal/runner/grpc/workspace_registry.go`
- Create: `internal/runner/grpc/workspace_registry_test.go`

- [ ] Add `RunnerWorkspaceRecord` keyed by `WorkspaceID`, with unique canonical root and creation timestamp; include it in SQLite AutoMigrate.
- [ ] Write tests for registration under root, duplicate path rejection, rebinding an existing ID rejection, unknown ID, traversal, absolute path, symlink escape, and persistence after reopening DB.
- [ ] Run `go test ./internal/runner/grpc -run 'TestWorkspaceRegistry' -count=1` and verify it fails because registry APIs do not exist.
- [ ] Implement a registry around the configured root. Accept only an existing relative directory, canonicalize root and candidate with `EvalSymlinks`, enforce `filepath.Rel` containment, reject empty IDs and duplicate canonical paths, and persist the binding. `Resolve` returns not-found for unknown IDs.
- [ ] Run the focused registry tests and `go test ./internal/storage/sqlite ./internal/runner/grpc -count=1`.

## Task 2: Add authenticated RegisterWorkspace RPC and make Runner multi-workspace

**Files:**
- Modify: `proto/runner/v1/runner.proto`
- Regenerate: `internal/runner/rpc/generated/runner.pb.go`, `runner_grpc.pb.go`
- Modify: `internal/runner/grpc/service.go`, `service_test.go`, `auth.go`
- Modify: `cmd/workspace-runner/main.go`

- [ ] Add `RegisterWorkspace(RegisterWorkspaceRequest) returns (RegisterWorkspaceResponse)`; request fields are `workspace_id` and `relative_path`, response fields are canonical `root` and detected Git state.
- [ ] Add a gRPC test proving unknown IDs are rejected for describe, context-read, and execute; prove registration persists across `Service` reconstruction and different IDs use different roots.
- [ ] Run `protoc --go_out=. --go_opt=module=github.com/aicouncil/aicouncil --go-grpc_out=. --go-grpc_opt=module=github.com/aicouncil/aicouncil proto/runner/v1/runner.proto`.
- [ ] Change `Service` to resolve a registered workspace runtime per request. A runtime owns its canonical root, `pathguard.Guard`, command executor, context collector, and transaction factory. Never use the startup root as a fallback for an unknown ID.
- [ ] Keep execution approval hashing bound to the same `WorkspaceId`; call idempotency lookup only after validating that ID resolves.
- [ ] Update existing service tests to register explicit temporary workspaces. Verify unknown IDs return `NotFound` and resolved paths never cross between test workspaces.
- [ ] Run `go test ./internal/runner/grpc ./internal/runner/pathguard ./internal/runner/command ./internal/runner/idempotency -count=1`.

## Task 3: Register Council workspaces safely and keep legacy rows explicit

**Files:**
- Modify: `internal/transport/council/routes.go`, `routes_test.go`
- Modify: `cmd/council-server/main.go`, `auth_config.go`, tests
- Modify: `deploy/docker-compose.yml`
- Modify: `docs/operations/single-host.md`

- [ ] Extend the Council Runner client interface with `RegisterWorkspace` and update route test doubles.
- [ ] Write tests that absolute paths and traversal are rejected; Runner registration errors do not persist or return a workspace; successful registration persists Runner's canonical root and workspace ID.
- [ ] Change workspace creation to require a Runner client, accept a relative path, allocate the workspace ID, register with Runner first, and persist the Council record only after successful registration. Keep orphan registry entries unreachable if Council persistence fails.
- [ ] Start Compose Runner with `--workspace-root /workspace`. Do not expose this gRPC service outside the internal Compose network.
- [ ] At Council startup, detect workspace rows created before registry support. Re-register only rows whose stored canonical paths are safely inside the configured Runner root; fail startup with workspace IDs for unsafe paths. Never silently map these rows to the default root.
- [ ] Document how operators provision repositories under the workspace volume and how to resolve legacy paths.
- [ ] Run `go test ./cmd/council-server ./internal/transport/council ./internal/runner/grpc -count=1` and `deploy/validate-compose.ps1`.

## Task 4: Implement workspace sharing persistence and effective access checks

**Files:**
- Modify: `internal/storage/sqlite/models.go`, `db.go`
- Create: `internal/security/workspaceaccess/service.go`, `service_test.go`
- Modify: `internal/transport/council/routes.go`, `sse.go`, `ownership_test.go`

- [ ] Add unique `(workspace_id,user_id)` share records with `viewer|editor`, grantor ID, and timestamps; add transactional create/update, list, revoke, and effective-access queries.
- [ ] Test owner full access, viewer read-only, editor write access, disabled/nonexistent users, share-level update, and immediate revocation.
- [ ] Add a single effective-access helper that first checks ownership, then the workspace share; unknown or unauthorized IDs return not-found.
- [ ] Apply it to workspace listing/detail, task read/create/start/approve/reject/execute/cancel, artifact download, and SSE. Resolve task workspace before checking access. Shared tasks remain owned by their original creator.
- [ ] For SSE, re-check authorization before sending each event and close the connection once access is revoked.
- [ ] Run `go test ./internal/security/workspaceaccess ./internal/transport/council ./internal/storage/sqlite -count=1`.

## Task 5: Add owner-only share REST endpoints and RBAC policy

**Files:**
- Create: `internal/transport/council/workspace_shares.go`, `workspace_shares_test.go`
- Modify: `internal/transport/council/http.go`, `auth.go`, `routes.go`

- [ ] Add `GET /api/v1/workspaces/:id`, `GET /api/v1/workspaces/:id/shares`, `PUT /api/v1/workspaces/:id/shares/:subject`, and `DELETE /api/v1/workspaces/:id/shares/:subject`.
- [ ] Resolve target subjects to active user IDs. Require actual workspace ownership for all share-management operations; editor shares cannot grant onward access.
- [ ] Enforce workspace access for detail and share listing; permit viewers to list grants, but only owners may mutate grants.
- [ ] Map REST methods to `workspace:read|write`; keep resource ownership/share checks in handlers after RBAC permission checks.
- [ ] Test cross-owner denial, viewer/editor limits, owner-only grants, unknown subjects, invalid access roles, and immediate access loss after revoke.
- [ ] Run `go test ./internal/transport/council ./internal/security/workspaceaccess -count=1`.

## Task 6: Add workspace share management UI

**Files:**
- Modify: `web/lib/api.ts`
- Modify: `web/app/workspaces/page.tsx`
- Create: `web/app/workspaces/page.test.tsx`

- [ ] Add typed APIs for workspace detail, list shares, create/update share, and revoke share.
- [ ] Write tests first: owner can grant/change/revoke, viewer cannot see mutation controls, shared workspace renders the current role, and revoked access refreshes the list.
- [ ] Run `corepack pnpm --dir web test -- app/workspaces/page.test.tsx` and confirm the new tests fail before implementation.
- [ ] Implement member subject input, `viewer|editor` selector, current grant list, revoke controls, empty/loading/error states, and shared-role read-only controls using existing page styles.
- [ ] Show a concise notice that revocation blocks later requests but does not terminate work already executing.
- [ ] Run the focused Vitest suite and `corepack pnpm --dir web build`.

## Task 7: Verify full share-to-Runner lifecycle and compatibility

**Files:**
- Modify: `internal/e2e/*`
- Modify: `internal/transport/council/*_test.go`, `internal/runner/grpc/*_test.go`
- Modify: `docs/operations/single-host.md`

- [ ] Add an integrated test: owner registers an isolated workspace and grants editor; editor creates a task; task is manually approved and executed through gRPC Runner; owner revokes access; editor can no longer list/read workspace, task, artifact, events, or invoke new actions; owner retains access.
- [ ] Add two-workspace Runner isolation check: register separate roots, execute a safe test command or patch in one, and prove the other root is unchanged.
- [ ] Run `go test ./...`.
- [ ] Run `corepack pnpm --dir web test`, `corepack pnpm --dir web build`, `deploy/validate-compose.ps1`, and `git diff --check`.
- [ ] Review the migration and operational note for existing workspace rows and confirm no request falls back to the Runner process root.

## Scope controls

- Do not add public share links, teams, organization hierarchy, per-task sharing, upload, multi-host scheduling, arbitrary absolute paths, or forced termination of already-running work.
- Do not alter provider-key ownership or reveal Provider Key plaintext in workspace APIs.
- Preserve unrelated existing changes and do not commit during this implementation pass.
