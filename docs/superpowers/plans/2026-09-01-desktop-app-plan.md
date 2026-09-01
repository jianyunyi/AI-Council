# AI Council Desktop Application Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or **superpowers:executing-plans** to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Windows-downloadable AI Council desktop application bundling the React console, local Council/Runner services, protected secrets, and signed release artifacts without changing server deployment.

**Architecture:** Add a Go desktop runtime for loopback ports, session-token injection, child-process lifecycle, config directories, and graceful shutdown. Use Wails as the Windows shell and keep Next.js as the UI source with a desktop static build adapter. Reuse existing REST/gRPC contracts.

**Tech Stack:** Go 1.25+, Wails v2, Next.js 14/React 18, SQLite, Windows Credential Manager/DPAPI, GitHub Actions, Inno Setup.

---

## File map

- Create: `internal/desktop/config.go`, `ports.go`, `session.go`: OS paths, loopback ports, session tokens.
- Create: `internal/desktop/process.go`, `runtime.go`: child process lifecycle and health checks.
- Create: `internal/desktop/secrets_windows.go`, `secrets_other.go`: platform secret storage.
- Create: `cmd/aicouncil-desktop/main.go`, `wails.go`, `frontend_embed.go`: Wails bindings and embedded assets.
- Modify: `web/next.config.mjs`, `web/lib/api.ts`, `web/lib/events.ts`, task pages: desktop build and runtime injection.
- Create: `build/windows/installer.iss`, `build/version.json`, `.github/workflows/desktop.yml`.
- Test: `internal/desktop/*_test.go`, `cmd/aicouncil-desktop/main_test.go`, `web/lib/desktop.test.ts`, `web/e2e/desktop.spec.ts`.

### Task 1: Runtime configuration, ports and session token

**Files:** Create `internal/desktop/config.go`, `ports.go`, `session.go`; test `internal/desktop/config_test.go`, `ports_test.go`, `session_test.go`.

- [ ] **Step 1: Write failing tests** — test Windows config under `%LOCALAPPDATA%\\AI-Council`, non-Windows user config, restrictive directory creation, loopback-only listeners, and two unique tokens containing at least 32 random bytes.
- [ ] **Step 2: Verify red** — run `go test ./internal/desktop -run 'Test(Config|Port|Session)'`; expect failure because the package does not exist.
- [ ] **Step 3: Implement primitives** — define `Config`, `LoadConfig(map[string]string)`, `AllocateLoopbackPort() (net.Listener,error)`, and `NewSessionToken() (string,error)` using explicit `127.0.0.1` and `crypto/rand`.
- [ ] **Step 4: Verify green and commit** — run `gofmt -w internal/desktop; go test ./internal/desktop`; expect PASS; commit `feat: add desktop runtime configuration primitives`.

### Task 2: Service process manager and secret storage

**Files:** Create `internal/desktop/process.go`, `runtime.go`, `secrets_windows.go`, `secrets_other.go`; test `internal/desktop/process_test.go`, `runtime_test.go`, `secrets_test.go`.

- [ ] **Step 1: Write failing lifecycle tests** — fake child binaries must receive loopback addresses, random session token, SQLite directory and TLS/RBAC flags; readiness waits for both health checks; shutdown cancels and force-kills after a fixed timeout.
- [ ] **Step 2: Write failing secret-store tests** — cover `Put/Get/Delete`, typed missing-key errors, and absence of plaintext in SQLite or logs.
- [ ] **Step 3: Implement lifecycle** — implement `Runtime.Start(ctx)`, `WaitReady(ctx)`, `Status()`, and `Stop(ctx)` with `exec.CommandContext`, environment-only secret injection, bounded rotating logs and sanitized exit errors.
- [ ] **Step 4: Implement stores and commit** — use Windows Credential Manager/DPAPI behind `SecretStore`; non-Windows gets an encrypted-file test fallback; run `go test ./internal/desktop`; expect PASS; commit `feat: manage local council runner processes and secrets`.

### Task 3: Desktop-safe frontend build and runtime injection

**Files:** Modify `web/next.config.mjs`, `web/lib/api.ts`, `web/lib/events.ts`, `web/app/tasks/[id]/page.tsx`; create `web/lib/desktop.ts`, `web/app/tasks/view/page.tsx`; test `web/lib/desktop.test.ts`, `web/lib/api.test.ts`.

- [ ] **Step 1: Write failing frontend tests** — cover desktop API base/session injection, `Authorization: Bearer` on REST and SSE reconnects, web-mode relative `/api/v1`, and arbitrary task IDs through `/tasks/view?id=...`.
- [ ] **Step 2: Implement runtime bridge** — add browser-safe `getDesktopRuntime()`, centralize base URL/auth headers, and update SSE to use the same base/token without logging secrets.
- [ ] **Step 3: Adapt static routing** — enable `output: 'export'` only when `DESKTOP_BUILD=1`; retain `/tasks/[id]` for server mode and redirect desktop task creation to `/tasks/view?id=...`.
- [ ] **Step 4: Verify and commit** — run Vitest for desktop/API tests and `$env:DESKTOP_BUILD='1'; node web/node_modules/next/dist/bin/next build web`; expect PASS with static output; commit `feat: support desktop frontend runtime injection`.

### Task 4: Wails shell and embedded assets

**Files:** Create `cmd/aicouncil-desktop/main.go`, `wails.go`, `frontend_embed.go`; modify `go.mod`, `go.sum`, `web/package.json`; test `cmd/aicouncil-desktop/main_test.go`.

- [ ] **Step 1: Write failing shell tests** — bindings `Start`, `Stop`, `Status`, `OpenWorkspace`, `SaveProviderKey` must not return secrets; reject non-loopback URLs; close must stop the runtime.
- [ ] **Step 2: Add Wails bindings** — create a single-window Wails app, bind `DesktopApp`, embed `web/out` with `//go:embed`, and pass runtime URL/token during `OnStartup`; keep Wails isolated to the desktop command.
- [ ] **Step 3: Wire lifecycle** — start Council/Runner, wait for health, show recoverable startup errors, and gracefully stop active work before children.
- [ ] **Step 4: Verify and commit** — run `go test ./cmd/aicouncil-desktop ./internal/desktop`, static Next build, and `go build ./cmd/aicouncil-desktop`; expect PASS; commit `feat: add wails desktop shell`.

### Task 5: Installer, diagnostics and release CI

**Files:** Create `build/windows/installer.iss`, `build/version.json`, `build/diagnostics.md`, `.github/workflows/desktop.yml`; modify `README.md`; test `build/config_test.go`, `web/e2e/desktop.spec.ts`.

- [ ] **Step 1: Write configuration tests** — validate installer executable/version/data-directory/uninstaller metadata and workflow steps for tests, desktop build, installer, SHA-256 and artifact upload.
- [ ] **Step 2: Add installer and diagnostics** — install under Program Files, preserve `%LOCALAPPDATA%\\AI-Council` on upgrade, create shortcuts, support silent install, and export only sanitized config/status/bounded logs.
- [ ] **Step 3: Add CI/documentation** — Windows runner builds Wails/installer; Ubuntu runs Go, frontend and Playwright; document portable vs installer, secret storage, loopback behavior, upgrade/uninstall and Chromium limitation.
- [ ] **Step 4: Final verification and commit** — run `go test ./...`, `go vet ./...`, Vitest and `git diff --check`; expect all exit 0; commit `chore: package desktop releases and diagnostics`, push branch, and tag only after Windows CI succeeds.

## Plan self-review

- Wails shell, local process lifecycle, loopback security, Session Token, secret storage, static frontend, installer, diagnostics, updates and CI are covered by Tasks 1-5.
- Existing server commands, REST/gRPC contracts, Caddy TLS and browser Web mode remain unchanged.
- No secrets enter SQLite/logs/localStorage; human approval, Runner pathguard and user-selected Workspace remain mandatory.
- Every task names files, TDD steps, commands, expected results and commit boundaries; no unresolved placeholders remain.
