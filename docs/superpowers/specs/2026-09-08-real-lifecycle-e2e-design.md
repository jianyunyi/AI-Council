# Real Council Lifecycle E2E Design

## Goal

Prove the production path from a bounded workspace snapshot through Council
planning, manual approval, Runner execution, and verification. The test must
show that an approved patch changes a real file exactly once.

## Scope

- Exercise the Council HTTP handlers to create, start, approve, execute, and
  read a task.
- Use SQLite for task and approval persistence.
- Use the real Runner gRPC service against a temporary workspace.
- Use a deterministic Provider fake only at the model-transport boundary.
- Assert an approved patch is written, normal and verification commands are
  recorded, final state is `SUCCEEDED`, and repeat execution returns HTTP 409.
- Document workspace-context filtering, manual approval, and no automatic
  replanning in the README.

## Design

The E2E fixture creates a temporary workspace containing a text file and a
temporary SQLite database. It starts an in-process gRPC server backed by the
real Runner service, then injects its generated client into the Council API.
The Council workflow uses deterministic Provider responses for proposal,
review, judgement, and red-team stages. The judgement response contains a
versioned patch, one ordinary command, and one verification command.

The test calls the HTTP handlers through `httptest`; it does not invoke
internal task-state methods directly. It reads the returned approval hash,
submits the explicit approval request, and executes the task. Assertions read
the workspace file and API task representation after the call. A second
execute call must be rejected without running the Runner again.

## Failure Semantics

The test fails if the API uses a mock Runner, if execution succeeds without a
Runner response, if the patch is not written, if verification is omitted, or
if a consumed approval can execute twice. Provider failure behavior remains
covered by the focused Council and REST tests rather than this success-path
fixture.

## Verification

Run `go test ./internal/e2e -count=1`, then the focused Council, Runner,
SQLite, and REST packages. Repository-wide Go verification remains conditional
on the local environment being able to download the locked Gin and Wails
modules.
