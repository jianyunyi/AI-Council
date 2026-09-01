# AI Council Production Security and Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver database-backed RBAC, native TLS certificate rotation, and production metrics/cost dashboards while preserving existing static-token and Caddy deployments.

**Architecture:** Extend the existing SQLite/GORM security and usage records instead of introducing an external identity or billing service. Keep authentication, TLS, metrics, and cost calculation behind small packages with HTTP/gRPC adapters; wire them into the two process entrypoints only after package-level tests pass. Ship each subsystem as an independently verified commit.

**Tech Stack:** Go 1.25+, GORM/SQLite, go-zero REST, Gin Runner gRPC, `crypto/tls`, Prometheus text exposition, React/Next.js, Vitest, JSON dashboard/config tests.

---

## File map

- `internal/storage/sqlite/models.go`, `db.go`: RBAC credentials, roles, permissions, token and provider usage schema/migrations.
- `internal/security/rbac/service.go`, new `password.go`, `token.go`: password hashing, token lifecycle, permission checks and bootstrap.
- `internal/transport/council/auth.go`, new `auth_routes.go`, `http.go`: request identity middleware and login/admin REST endpoints.
- `cmd/council-server/main.go`: select `static`, `rbac`, or `hybrid` mode and bootstrap the first administrator.
- `internal/security/tlsconfig/reloader.go`: atomic certificate/CA reload and reload metrics.
- `cmd/council-server/main.go`, `cmd/workspace-runner/main.go`, `internal/transport/council/http.go`: HTTP/gRPC TLS and mTLS wiring.
- `internal/observability/metrics/metrics.go`, new `prometheus.go`: low-cardinality counters/histograms and exposition.
- `internal/provider/cost.go`, `metered.go`, `internal/storage/sqlite/council_repository.go`: price overrides and durable cost usage.
- `web/lib/api.ts`, `web/lib/types.ts`, task pages: authenticated session and cost summary display.
- `deploy/grafana/aicouncil-dashboard.json`, `deploy/prometheus/alerts.yml`: dashboard and alert rules.

### Task 1: RBAC schema and credential lifecycle

**Files:**
- Modify: `internal/storage/sqlite/models.go`, `internal/storage/sqlite/db.go`
- Modify: `internal/security/rbac/service.go`
- Create: `internal/security/rbac/password.go`, `internal/security/rbac/token.go`
- Test: `internal/security/rbac/service_test.go`, `internal/storage/sqlite/rbac_repository_test.go`

- [ ] **Step 1: Write failing persistence tests**

Add tests that open a temporary SQLite database, create a user with a password, role, permission, and token, close/reopen the database, and assert the records and revoked/expired token state survive. Add a test that duplicate usernames and role-permission links are rejected.

- [ ] **Step 2: Run the focused tests and verify failure**

Run:

```powershell
go test ./internal/security/rbac ./internal/storage/sqlite -run 'Test(RBAC|Token|Password)'
```

Expected: FAIL because password, permission and token lifecycle APIs are not defined.

- [ ] **Step 3: Add normalized RBAC models and idempotent migration**

Add `PermissionRecord`, `RolePermissionRecord`, and `AccessTokenRecord` with composite unique indexes, token hash, expiry and revoked timestamps. Include them in `sqlite.Open` AutoMigrate. Keep the existing `UserRecord` fields for hybrid compatibility and add a nullable password hash.

- [ ] **Step 4: Implement Argon2id and opaque token services**

Implement:

```go
func HashPassword(password string) (string, error)
func VerifyPassword(encoded, password string) error
func (s *Service) IssueToken(ctx context.Context, userID string, ttl time.Duration) (plain string, record AccessTokenRecord, err error)
func (s *Service) RevokeToken(ctx context.Context, token string) error
func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error)
```

Store only SHA-256 token hashes; compare hashes with constant-time comparison and reject disabled, revoked, or expired credentials.

- [ ] **Step 5: Implement permission and bootstrap APIs**

Implement `CreateUser`, `SetPassword`, `CreateRole`, `GrantPermission`, `AssignRole`, `BootstrapAdmin`, and `AuthorizePermission`. Bootstrap must be idempotent and return the generated token only to the caller.

- [ ] **Step 6: Run tests and commit**

Run:

```powershell
go test ./internal/security/rbac ./internal/storage/sqlite
```

Expected: PASS. Commit as `feat: add persistent rbac credentials and permissions`.

### Task 2: RBAC HTTP lifecycle and route enforcement

**Files:**
- Modify: `internal/transport/council/auth.go`, `internal/transport/council/http.go`
- Create: `internal/transport/council/auth_routes.go`
- Modify: `cmd/council-server/main.go`
- Test: `internal/transport/council/auth_test.go`, new `auth_routes_test.go`, `cmd/council-server/main_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Test `POST /api/v1/auth/login`, `GET /api/v1/auth/me`, logout/revocation, admin user/role creation, and a normal user receiving 403 for approval/execution routes. Assert missing credentials return 401 and insufficient permissions return 403.

- [ ] **Step 2: Run tests to verify failure**

```powershell
go test ./internal/transport/council ./cmd/council-server -run 'Test(RBAC|Auth|Admin)'
```

Expected: FAIL because login/admin routes and identity context are missing.

- [ ] **Step 3: Add identity-aware middleware**

Parse `Authorization: Bearer`, select static/RBAC/hybrid mode, attach `rbac.Identity` to request context, and expose `RequirePermission("resource:action")`. Keep `/metrics` and health routes outside business authorization, but never bypass authentication for task mutation routes.

- [ ] **Step 4: Add auth and admin handlers**

Implement JSON handlers for login, logout, me, users, roles, permissions. Login accepts username/password and returns `{token, expiresAt, user}`; logout revokes the presented token. Admin handlers require `admin:*` and never return password hashes or token hashes.

- [ ] **Step 5: Apply least-privilege route matrix**

Require `workspace:write` for workspace creation, `task:write` for task creation/start, `task:read` for reads/SSE, `task:approve` for approval, and `task:execute` for execution. Preserve static-token behavior under `AUTH_MODE=static` and map the configured static token to the configured compatibility role under `hybrid`.

- [ ] **Step 6: Wire bootstrap and configuration**

Add flags/env values for `AUTH_MODE`, `RBAC_DB`, `RBAC_BOOTSTRAP_USERNAME`, `RBAC_BOOTSTRAP_PASSWORD`, and token TTL. Fail startup when RBAC mode has no database or bootstrap credentials and no existing admin is present.

- [ ] **Step 7: Run tests, vet, and commit**

```powershell
go test ./internal/transport/council ./cmd/council-server
go vet ./internal/transport/council ./cmd/council-server
```

Expected: PASS. Commit as `feat: wire rbac authentication through rest api`.

### Task 3: Native TLS rotation for HTTP and gRPC

**Files:**
- Modify: `internal/security/tlsconfig/reloader.go`
- Modify: `internal/transport/council/http.go`, `cmd/council-server/main.go`, `cmd/workspace-runner/main.go`
- Create: `internal/security/tlsconfig/ca.go`
- Test: `internal/security/tlsconfig/reloader_test.go`, `internal/transport/council/http_tls_test.go`, `internal/runner/grpc/tls_test.go`

- [ ] **Step 1: Write failing reload and mTLS tests**

Generate two local certificate pairs in tests. Assert the first certificate is served, replacing both files changes the certificate for the next handshake, and writing an invalid pair leaves the old certificate active. Add a client-certificate test that rejects an untrusted client.

- [ ] **Step 2: Run focused tests to verify failure**

```powershell
go test ./internal/security/tlsconfig ./internal/transport/council ./internal/runner/grpc -run 'Test(TLS|Certificate|MTLS)'
```

Expected: FAIL for CA configuration and gRPC mTLS wiring.

- [ ] **Step 3: Extend the reloader**

Add `NewWithCA(certFile, keyFile, caFile string, clientAuth tls.ClientAuthType)`, atomic CA pool storage, `LastReload`, `ReloadErrors`, and a `Reload()` method. Keep `GetCertificate` serving the previous certificate when a changed file is invalid.

- [ ] **Step 4: Wire HTTP and gRPC options**

Use `rest.WithTLSConfig(reloader.Config())` for Council and `credentials.NewTLS` for Runner. Add CA/client-auth flags to both binaries and configure the Council Runner client with server-name verification and optional client certificates. Keep existing Caddy/static-token paths unchanged when flags are empty.

- [ ] **Step 5: Expose TLS health signals and verify**

Expose certificate expiry and reload-error values through the metrics package and a protected `/health/tls` endpoint. Run the focused tests, then `go test ./...` and `go vet ./...`; commit as `feat: add native tls rotation and mtls support`.

### Task 4: Prometheus business metrics

**Files:**
- Modify: `internal/observability/metrics/metrics.go`
- Modify: `internal/transport/council/logging.go`, `internal/transport/council/routes.go`, `internal/runner/grpc/service.go`, `internal/security/rbac/service.go`, `internal/security/tlsconfig/reloader.go`
- Test: `internal/observability/metrics/metrics_test.go`, affected package tests

- [ ] **Step 1: Write exposition and cardinality tests**

Assert the `/metrics` response contains counters and histogram buckets for Council phases, Runner executions, HTTP latency, auth failures, permission denials, TLS reload errors, and provider retries. Assert task/user IDs never occur as label values.

- [ ] **Step 2: Implement low-cardinality metric primitives**

Use Prometheus-compatible counters/histograms with labels limited to `provider`, `model`, `phase`, `status`, and `method`. Preserve existing metric names as aliases where already consumed by dashboards.

- [ ] **Step 3: Instrument business boundaries**

Record phase duration and outcome around Council workflow, Runner idempotency/verification, provider retries, HTTP/SSE requests, RBAC failures, and TLS reload events. Ensure increments occur exactly once per request even on error paths.

- [ ] **Step 4: Run tests and commit**

```powershell
go test ./internal/observability/metrics ./internal/transport/council ./internal/runner/grpc ./internal/security/rbac ./internal/security/tlsconfig
```

Expected: PASS. Commit as `feat: expose production prometheus metrics`.

### Task 5: Durable provider cost accounting and REST summary

**Files:**
- Modify: `internal/provider/cost.go`, `internal/provider/metered.go`
- Modify: `internal/storage/sqlite/models.go`, `internal/storage/sqlite/council_repository.go`
- Modify: `internal/transport/council/routes.go`
- Modify: `web/lib/api.ts`, `web/lib/types.ts`, `web/components/task/task-workbench.tsx`
- Test: `internal/provider/cost_test.go`, `internal/storage/sqlite/cost_repository_test.go`, `internal/transport/council/cost_routes_test.go`, `web/lib/api.test.ts`

- [ ] **Step 1: Write failing price and persistence tests**

Cover exact micro-cost calculation, configured price override, unknown-model usage with `unknown` cost status, SQLite restart recovery, and `GET /api/v1/tasks/{id}/cost` aggregation by provider/model.

- [ ] **Step 2: Add configurable price table and cost records**

Define `PriceTable` loading from a JSON file or environment override, validate non-negative per-million prices, and add a `ProviderUsageRecord` keyed by task/run/provider/model/request. Persist token counts, estimated micros, known/unknown status and timestamps.

- [ ] **Step 3: Persist metered responses at the Council boundary**

Keep provider wrappers responsible for calculating cost, then record usage once per invocation in the Council repository. Never store API keys or raw provider prompts in the usage record.

- [ ] **Step 4: Add REST and web summary**

Return total input/output tokens, known cost micros, unknown-model count, and per-provider breakdown. Render the summary in the task workbench without exposing secrets or requiring browser localStorage.

- [ ] **Step 5: Run tests and commit**

```powershell
go test ./internal/provider ./internal/storage/sqlite ./internal/transport/council
node web/node_modules/vitest/vitest.mjs run web/lib/api.test.ts
```

Expected: PASS. Commit as `feat: persist provider usage and task cost summaries`.

### Task 6: Grafana dashboard, alerts, documentation and CI validation

**Files:**
- Create: `deploy/grafana/aicouncil-dashboard.json`
- Create: `deploy/prometheus/alerts.yml`
- Modify: `README.md`, `.github/workflows/ci.yml`
- Test: new `deploy/observability/config_test.go` or a Go JSON/YAML validation test

- [ ] **Step 1: Write configuration validation test**

Load the dashboard and alert files, assert every referenced metric exists in the metrics registry, every alert has an expression/severity/summary, and JSON/YAML syntax is valid.

- [ ] **Step 2: Add dashboard panels and alert rules**

Include Council success/phase latency, Provider errors/retries/cost, Runner success/verification/idempotency, API latency, RBAC denials, and TLS expiry/reload errors. Add alerts for high error rate, sustained retries, verification failures, and certificates expiring within 14 days.

- [ ] **Step 3: Document production configuration**

Document RBAC bootstrap, `AUTH_MODE`, TLS/mTLS rotation, price overrides, metrics scraping, dashboard import, and the Windows Playwright limitation. Explicitly state which secrets are never persisted.

- [ ] **Step 4: Add CI gates and perform final verification**

CI must run `go test ./...`, `go vet ./...`, frontend Vitest, dashboard/config validation, and Playwright on Linux. Run locally:

```powershell
go test ./...
go vet ./...
node web/node_modules/vitest/vitest.mjs run
git diff --check
```

Expected: all commands exit 0. Commit as `chore: add production observability dashboards and ci gates`, then push `master`.

## Plan self-review checklist

- RBAC spec coverage: models, Argon2id, token lifecycle, bootstrap, modes, route permissions, audit identity and restart persistence are covered by Tasks 1-2.
- TLS spec coverage: HTTP/gRPC, CA/mTLS, hot reload, rollback on invalid files, metrics and tests are covered by Task 3.
- Observability spec coverage: low-cardinality Prometheus metrics, durable cost accounting, configurable prices, REST/web summary, dashboard and alerts are covered by Tasks 4-6.
- Compatibility constraints: static Bearer Token, Caddy termination, SQLite, secret handling and Windows Playwright limitation are explicitly preserved.
- No unresolved placeholders remain; each task names files, commands, expected outcomes and a commit boundary.
