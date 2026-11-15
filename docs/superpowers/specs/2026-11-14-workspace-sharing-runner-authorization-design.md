# Workspace sharing and Runner authorization design

## Goal

Allow a workspace owner to share a workspace with another registered member, and make Runner enforce the workspace boundary independently of REST authorization. A guessed or stale workspace ID must never select the Runner's default directory or another workspace.

## Current constraints

- Council stores workspace and task owners in SQLite. Workspace reads and task actions currently compare only the resource owner to the authenticated user.
- The Runner gRPC service currently ignores `workspace_id` and always reads or mutates the one root passed to `--workspace-root`.
- Compose mounts a named `workspace-data` volume at `/workspace`, but the Runner command does not currently set that path as its root. Council accepts a root string, but does not register it with Runner.
- The Runner uses a shared Council-to-Runner bearer token. It authenticates the Council process, not individual end users; per-user permissions therefore must be checked in Council and Runner must validate the registered workspace ID and root mapping.

## Access model

`WorkspaceShareRecord` stores a unique `(workspace_id, user_id)` grant, access level (`viewer` or `editor`), grantor user ID, and timestamps. The workspace owner is implicit full access and does not need a share row. Only the owner can create, update, or revoke shares. The target must be an active local user. The REST API accepts a user subject and resolves it to the internal user ID; responses never expose secrets.

| Operation | Owner | Editor | Viewer |
|---|---:|---:|---:|
| List and inspect shared workspace | Yes | Yes | Yes |
| Read tasks and artifacts in workspace | Yes | Yes | Yes |
| Create task and start Council analysis | Yes | Yes | No |
| Approve, execute, reject, or cancel a task in workspace | Yes | Yes | No |
| Add, change, revoke workspace shares | Yes | No | No |
| Access another user's Provider Keys | No | No | No |

Workspace access grants task access only within that workspace. A task retains its creator as `OwnerUserID`; sharing does not transfer ownership. All task and artifact endpoints resolve the task's workspace and evaluate the caller's effective workspace role. Revoking a share takes effect on the next REST request. Existing event streams re-check authorization while streaming and close after access is revoked.

## Runner workspace registry

The configured Runner root is the sole filesystem boundary. Compose starts Runner with `/workspace` as its workspace root. Council workspace creation registers a relative path and opaque workspace ID through an authenticated `RegisterWorkspace` gRPC operation. The directory must already exist under `/workspace` (operators mount or provision repositories there). Runner canonicalizes the joined path, verifies it remains beneath the configured root, rejects traversal and symlinks that escape the root, and persists the ID-to-canonical-root mapping in Runner SQLite. A canonical path may be bound to only one workspace ID, preventing two owners from registering the same files under separate permissions.

`DescribeWorkspace`, `ReadWorkspaceContext`, and `ExecuteApprovedPlan` resolve the request ID through this registry. Unknown IDs are rejected with `NotFound`; no method falls back to the Runner's process root. The Runner creates the path guard, context collector, command executor, and transaction for the resolved root per request. Registry entries remain after Council restart. Re-registering an existing ID with a different canonical path is rejected. Workspace deletion/unregistration is not part of this slice, so stale mappings are harmless and cannot be selected without a corresponding Council-owned record and authorization.

The existing Council-to-Runner token continues to authenticate the service boundary. All Runner methods, including registration and reads, require that token in production; a missing configured token is rejected at startup for the production Compose profile. Per-user share decisions remain in Council, which resolves all end-user requests before making an RPC. Runner's registry is the final filesystem containment layer, not a substitute for Council RBAC.

## REST and persistence

- Add `GET /api/v1/workspaces/:id/shares`, `PUT /api/v1/workspaces/:id/shares/:subject` with `{access:"viewer"|"editor"}`, and `DELETE /api/v1/workspaces/:id/shares/:subject`.
- Sharing routes require ownership, not merely `workspace:write`; other members cannot grant onward access.
- Workspace and task REST operations call one effective-access helper. It returns not-found for resources the caller cannot access, preserving cross-account enumeration resistance.
- Workspace creation validates and registers with Runner first, then persists the Council workspace only after successful registration. If the Council save fails, the workspace is not returned as created; the orphan Runner mapping is harmless and remains unreachable through REST. Operator cleanup of stale registry entries is an operational task until an explicit unregister lifecycle is designed.
- Workspace paths accepted by the public API are relative to the Runner root. Absolute paths, drive paths, traversal, and paths resolving outside the root are rejected before RPC; Runner independently repeats the containment check.
- Existing workspace rows have no Runner mapping because the old service did not register IDs. They must be re-registered during startup migration only when their stored paths can be safely related to the configured Runner root. Otherwise startup reports the affected workspace IDs and requires operator correction; it must not silently map them to the new root.

## Failure behavior and audit

- Unknown user subject, invalid share level, inactive account, non-owner grant/revoke, unknown workspace, or out-of-root path receives a safe client error without exposing filesystem details.
- Runner timeout/unavailable errors fail workspace creation; Council does not persist a usable workspace record.
- Runner logs and responses do not include bearer token values or workspace file contents. Security logs record actor, workspace ID, target user ID, and grant/revoke action.
- A share revoked during an active task cannot undo an already-running process. REST denies subsequent operations and Runner execution remains limited to that registered workspace path. The UI should state that revocation blocks new requests but does not terminate work already executing.

## Web interface

The workspace page shows owner/editor/viewer access for each accessible workspace. Owners can search/select existing members, grant or change viewer/editor access, and revoke access. Non-owners see the current grants but no management controls. Empty, loading, and permission-denied states use existing app components. No API Key plaintext or workspace file contents are added to the sharing UI.

## Verification

- SQLite tests cover unique grants, role updates, revocation, and owner full access.
- REST tests cover viewer/editor permissions for workspace, task, artifact, SSE, and share-management routes, including cross-user denial and revocation taking effect immediately.
- Runner tests cover registration persistence across service recreation, unknown IDs, path traversal, absolute paths, symlink escape, ID-to-path rebinding, and isolation between two registered directories for describe/context/execute.
- gRPC integration tests assert every method rejects a missing or incorrect service token and rejects unknown workspace IDs.
- End-to-end test shares a workspace, runs a Council task as the editor through approval and Runner, revokes the share, then verifies editor access is denied while the owner retains access.
- Web tests cover owner grant/revoke controls, viewer read-only controls, and share list refresh.
- Run `go test ./...`, `corepack pnpm --dir web test`, `corepack pnpm --dir web build`, and `deploy/validate-compose.ps1`.

## Scope exclusions

This change does not add public links, organization/team nesting, per-task collaborator grants, filesystem upload, arbitrary absolute host paths, Runner multi-host scheduling, or forced cancellation of in-flight execution on share revocation.
