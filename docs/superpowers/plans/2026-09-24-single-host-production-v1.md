# 单机生产 V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AI Council 交付为可在一台 Windows 或 Linux 主机上通过 Docker Compose 安全部署的公网多用户版本。

**Architecture:** 保持 Caddy→Web/Council→Runner 的既有单机拓扑，SQLite 和制品使用宿主机持久化卷。以本地用户为唯一授权主体，密码、GitHub、Google 登录都映射到本地用户；所有工作区、Provider Key、任务、制品、审批和执行均执行所有权校验。部署、备份、监控和验收脚本组成可重复的发布闭环。

**Tech Stack:** Go、go-zero REST、gRPC、GORM/SQLite、Docker Compose、Caddy、React/Next.js、Vitest、Playwright、GitHub Actions。

---

### Task 1: 固化生产配置与 SQLite 恢复基础

**Files:**
- Modify: `internal/storage/sqlite/db.go`
- Modify: `internal/storage/sqlite/db_test.go`
- Create: `cmd/aicouncil-admin/main.go`
- Create: `cmd/aicouncil-admin/main_test.go`
- Modify: `deploy/.env.example`
- Create: `deploy/backup.ps1`
- Create: `deploy/restore-verify.ps1`
- Create: `docs/operations/single-host.md`

- [ ] **Step 1: 为 SQLite pragma 与完整性检查写失败测试**

在 `internal/storage/sqlite/db_test.go` 创建临时数据库，断言 `journal_mode` 为 `wal`、`foreign_keys` 为 `1`、`busy_timeout` 大于零，并让损坏数据库路径返回带 `integrity_check` 上下文的错误。

Run: `go test ./internal/storage/sqlite -run 'TestOpen' -count=1`

Expected: FAIL，因为 `Open` 尚未设置 busy timeout 或执行完整性检查。

- [ ] **Step 2: 实现最小的 SQLite 生产打开策略**

在 `Open` 成功连接后顺序执行：

```go
PRAGMA foreign_keys = ON
PRAGMA busy_timeout = 5000
PRAGMA journal_mode = WAL
PRAGMA synchronous = NORMAL
PRAGMA integrity_check
```

仅接受完整性检查第一行值为 `ok`；其余值返回可操作错误。保留 `:memory:` 的现有特殊行为，不为内存数据库设置 WAL。

- [ ] **Step 3: 验证 SQLite 生产打开策略**

Run: `go test ./internal/storage/sqlite -run 'TestOpen' -count=1`

Expected: PASS。

- [ ] **Step 4: 增加备份与恢复命令的失败测试**

让 `cmd/aicouncil-admin` 的 `backup --db --artifacts --out` 在缺失路径、无效输出目录和校验和不匹配时返回非零退出码；成功备份必须输出包含数据库、制品清单和 SHA-256 的 manifest。

Run: `go test ./cmd/aicouncil-admin -count=1`

Expected: FAIL，因为命令尚不存在。

- [ ] **Step 5: 实现备份、恢复校验与运维脚本**

实现管理员命令：SQLite 使用在线备份 API/一致性快照写入临时文件后原子移动；制品仅写 manifest（相对路径、大小、SHA-256），不把用户路径打印到日志。`backup.ps1` 调用该命令；`restore-verify.ps1` 将数据库恢复到明确提供的隔离目录，运行 `integrity_check` 并逐项校验 manifest。将密钥、卷目录、备份保留、恢复演练写入 `docs/operations/single-host.md`。

- [ ] **Step 6: 验证备份恢复并提交**

Run: `go test ./cmd/aicouncil-admin ./internal/storage/sqlite -count=1`

Expected: PASS。

```bash
git add internal/storage/sqlite cmd/aicouncil-admin deploy/backup.ps1 deploy/restore-verify.ps1 deploy/.env.example docs/operations/single-host.md
git commit -m "feat: add sqlite backup and recovery tooling"
```

### Task 2: 扩展本地账户为可验证的密码身份

**Files:**
- Modify: `internal/storage/sqlite/models.go`
- Modify: `internal/storage/sqlite/db.go`
- Create: `internal/security/identity/service.go`
- Create: `internal/security/identity/service_test.go`
- Create: `internal/transport/council/registration.go`
- Create: `internal/transport/council/registration_test.go`
- Modify: `cmd/council-server/auth_config.go`
- Modify: `cmd/council-server/main.go`
- Modify: `deploy/.env.example`

- [ ] **Step 1: 写账户验证与密码重置的失败测试**

测试注册创建 `email_verified_at=nil` 的用户、验证 token 只能使用一次、过期 token 返回统一无效响应、重置 token 不暴露账户是否存在、重置后旧 session 失效。

Run: `go test ./internal/security/identity ./internal/transport/council -run 'Test(Register|Verify|Reset)' -count=1`

Expected: FAIL，因为身份服务与路由尚不存在。

- [ ] **Step 2: 添加最小身份数据模型与服务**

新增 `IdentityRecord` 和 `IdentityTokenRecord`：token 只保存 SHA-256，字段包含 purpose、expires_at、consumed_at。服务公开 `RegisterEmail`、`VerifyEmail`、`RequestPasswordReset`、`ResetPassword`；始终返回统一的请求重置成功结果，不允许 handler 根据邮箱存在与否改变响应。

- [ ] **Step 3: 增加 SMTP 端口和公开路由**

定义 `Mailer` 接口：

```go
type Mailer interface {
    Send(ctx context.Context, to, subject, text string) error
}
```

在 `registration.go` 注册 `/api/v1/auth/register`、`/verify-email`、`/password-reset/request`、`/password-reset/confirm`。只有发送方来自 `SMTP_HOST`、`SMTP_PORT`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`SMTP_FROM` 的显式生产配置时才启用注册；开发/测试使用内存 Mailer。将这些公开 POST 路由加入 `isPublicRBACPath`。

- [ ] **Step 4: 让身份状态参与登录**

默认拒绝未经验证的密码账户登录；仅 `COUNCIL_BOOTSTRAP_SUBJECT` 可在启动创建时标记为已验证。密码重置后撤销该用户所有 `AccessTokenRecord`。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/security/identity ./internal/transport/council ./cmd/council-server -count=1`

Expected: PASS。

```bash
git add internal/security/identity internal/storage/sqlite internal/transport/council cmd/council-server deploy/.env.example
git commit -m "feat: add verified password accounts"
```

### Task 3: GitHub 与 Google OAuth 身份归一

**Files:**
- Create: `internal/security/oauth/service.go`
- Create: `internal/security/oauth/service_test.go`
- Create: `internal/transport/council/oauth_routes.go`
- Create: `internal/transport/council/oauth_routes_test.go`
- Modify: `internal/storage/sqlite/models.go`
- Modify: `internal/storage/sqlite/db.go`
- Modify: `cmd/council-server/auth_config.go`
- Modify: `deploy/.env.example`
- Modify: `web/app/login/page.tsx`
- Modify: `web/app/login/page.test.tsx`

- [ ] **Step 1: 写 OAuth state/PKCE 的失败测试**

测试授权 state 为单次、短时、与 provider 和回调意图绑定；回调 state 缺失/重复/过期都拒绝；可信 provider 返回相同 verified email 时复用同一 local user，绝不创建重复主体。

Run: `go test ./internal/security/oauth ./internal/transport/council -run 'TestOAuth' -count=1`

Expected: FAIL，因为 OAuth 服务尚不存在。

- [ ] **Step 2: 实现 OAuth 服务与持久身份关联**

新增唯一键 `(provider, provider_subject)` 的 identity 关联记录。GitHub 使用用户邮箱 API，Google 使用 OIDC ID token；仅接受 HTTPS issuer、匹配 audience、已验证邮箱。state 和 PKCE verifier 仅保存哈希，完成回调后立即消费。

- [ ] **Step 3: 接入回调路由与 Cookie session**

新增 `/api/v1/auth/oauth/:provider/start` 与 `/callback`，由服务端换 token、创建/关联本地用户并设置现有 HttpOnly/Secure/Strict session cookie。前端只渲染跳转链接，不接触 OAuth client secret。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/security/oauth ./internal/transport/council ./web/app/login -count=1`

Expected: PASS。

```bash
git add internal/security/oauth internal/storage/sqlite internal/transport/council cmd/council-server deploy/.env.example web/app/login
git commit -m "feat: add github and google oauth login"
```

### Task 4: 实施资源所有权、成员权限与密钥隔离

**Files:**
- Modify: `internal/storage/sqlite/models.go`
- Modify: `internal/storage/sqlite/*_repository.go`
- Create: `internal/security/ownership/service.go`
- Create: `internal/security/ownership/service_test.go`
- Modify: `internal/transport/council/routes.go`
- Modify: `internal/transport/council/rbac_routes.go`
- Modify: `internal/transport/council/routes_test.go`
- Modify: `web/lib/api.ts`
- Modify: `web/app/workspaces/page.tsx`
- Modify: `web/app/providers/page.tsx`

- [ ] **Step 1: 写跨用户越权的失败测试**

创建 owner、member、outsider 三个主体。断言 outsider 无法列出、读取、下载、批准、执行或取消 owner task，无法读取 owner workspace 的制品，无法得到 Provider API Key 明文；member 只有明确 workspace grant 后可读写。

Run: `go test ./internal/security/ownership ./internal/transport/council -run 'TestOwnership' -count=1`

Expected: FAIL，因为现有权限只按能力而不是资源归属判断。

- [ ] **Step 2: 添加所有权和工作区授权模型**

为 workspace/task/artifact/provider profile 保存 `owner_user_id`；新增 `WorkspaceGrantRecord{WorkspaceID, UserID, Role}`，角色固定为 `owner`、`editor`、`viewer`。迁移将既有记录归属 bootstrap admin，迁移前无 bootstrap admin 则拒绝生产启动，避免无主数据可被任意成员认领。

- [ ] **Step 3: 在每个资源路由执行二次授权**

在 task、artifact、workspace 和 provider handler 中从 `IdentityFromRequest` 取得用户，调用 ownership service，再调用业务服务。列表端点只返回已授权资源。Provider API Key 的更新只接受新明文，并以 KMS/主密钥派生的 AEAD 密文持久化；GET 只返回 id、provider、模型和 masked 标识。

- [ ] **Step 4: 更新成员端界面并验证**

前端显示当前可访问工作区，不加载或缓存 API Key 明文；权限不足显示现有通用错误。为资源 403、脱敏 provider 响应与授权工作区写 Vitest 测试。

- [ ] **Step 5: 验证并提交**

Run: `go test ./internal/security/ownership ./internal/transport/council ./internal/storage/sqlite && corepack pnpm --dir web test -- app/workspaces/page.test.tsx app/providers/page.test.tsx`

Expected: PASS。

```bash
git add internal/storage/sqlite internal/security/ownership internal/transport/council web/lib/api.ts web/app/workspaces web/app/providers
git commit -m "feat: scope council resources to members"
```

### Task 5: 将公开 HTTP 面收敛为可观测、限流的生产 API

**Files:**
- Create: `internal/observability/logging/http.go`
- Create: `internal/observability/logging/http_test.go`
- Create: `internal/security/ratelimit/limiter.go`
- Create: `internal/security/ratelimit/limiter_test.go`
- Modify: `internal/observability/metrics/metrics.go`
- Modify: `internal/transport/council/auth.go`
- Modify: `internal/transport/council/routes.go`
- Modify: `cmd/council-server/main.go`
- Modify: `deploy/Caddyfile`

- [ ] **Step 1: 写请求关联、限流与指标的失败测试**

断言每个响应有 `X-Request-ID`，日志字段包含 request_id、method、path、status、duration；登录、任务创建、审批、执行超过配置配额时返回 429；`/metrics` 包含状态、认证、执行、Provider、SQLite 指标且不能由公网 Caddy 路由访问。

Run: `go test ./internal/observability/logging ./internal/security/ratelimit ./internal/transport/council -run 'Test(RequestID|Rate|Metrics)' -count=1`

Expected: FAIL，因为这些中间件与维度化指标尚不存在。

- [ ] **Step 2: 实现有限内存限流和 JSON 日志中间件**

实现有界 key 数量、按窗口清理的 token bucket。key 为 `ip:<client-ip>` 与 `user:<identity.UserID>`，未认证请求只使用 IP。日志不可记录 authorization、cookie、密码、Provider Key、OAuth code/state 或任务输入全文。

- [ ] **Step 3: 接入 Prometheus 指标与内部 metrics 路由**

将原子计数扩展为带受控 label 的 counters/histograms；`/metrics` 仅由 Compose 内网的独立 scrape 端口/管理网络访问，Caddy 不增加其 handle。`/healthz` 无依赖检查，`/readyz` 检查 SQLite 与 Runner 可用性。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/observability/... ./internal/security/ratelimit ./internal/transport/council ./cmd/council-server -count=1`

Expected: PASS。

```bash
git add internal/observability internal/security/ratelimit internal/transport/council cmd/council-server deploy/Caddyfile
git commit -m "feat: add production api safeguards"
```

### Task 6: 加固 Runner 进程恢复与执行策略

**Files:**
- Modify: `internal/runner/idempotency/store.go`
- Modify: `internal/runner/idempotency/store_test.go`
- Modify: `internal/runner/grpc/auth.go`
- Modify: `internal/runner/grpc/service.go`
- Modify: `internal/runner/grpc/service_test.go`
- Modify: `internal/runner/command/executor.go`
- Modify: `internal/runner/command/executor_test.go`
- Modify: `cmd/workspace-runner/main.go`
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: 写跨进程幂等恢复的失败测试**

用两个独立 store 实例打开相同 SQLite 文件；第一个写入 running 记录并模拟进程退出，第二个必须返回同一 run 的受控失败/可恢复状态，且绝不再次启动命令。测试错误 token、缺 token 和非本地网络地址均被 gRPC interceptor 拒绝。

Run: `go test ./internal/runner/idempotency ./internal/runner/grpc -run 'Test(Persistent|Token)' -count=1`

Expected: FAIL，因为运行中记录的跨进程恢复策略不完整。

- [ ] **Step 2: 实现稳定执行记录与超时策略**

将幂等条目持久化为 `pending|running|succeeded|failed|cancelled`，记录开始/结束时间、请求摘要、验证摘要和安全错误码。重启时把过期 running 记录转换为 `failed`，错误码 `runner_interrupted`；相同幂等键返回这一终态，不重复执行。

- [ ] **Step 3: 强化 Runner Compose 隔离**

Runner 不发布端口，使用只读根文件系统、`no-new-privileges`、明确可写 data/workspace 临时卷、CPU/内存/pids 限制，并使用内部 network。任何会破坏已有 workspace 文件事务语义的只读挂载均不加入。

- [ ] **Step 4: 验证并提交**

Run: `go test ./internal/runner/... ./internal/e2e -count=1`

Expected: PASS。

```bash
git add internal/runner cmd/workspace-runner deploy/docker-compose.yml
git commit -m "feat: harden runner recovery and isolation"
```

### Task 7: 交付单机 Compose 发布闭环

**Files:**
- Modify: `deploy/docker-compose.yml`
- Modify: `deploy/Caddyfile`
- Modify: `deploy/.env.example`
- Modify: `deploy/validate-compose.ps1`
- Create: `deploy/preflight.ps1`
- Create: `deploy/deploy.ps1`
- Create: `deploy/rollback.ps1`
- Create: `deploy/monitoring/prometheus.yml`
- Create: `deploy/monitoring/grafana-dashboard.json`
- Modify: `README.md`
- Modify: `docs/operations/single-host.md`

- [ ] **Step 1: 写部署配置拒绝条件的失败测试**

让 `preflight.ps1` 拒绝默认 bootstrap 密码、短 Runner token、缺 SMTP/OAuth 回调 URL、公网暴露 Runner/Council 端口、未设置域名/TLS 和数据库卷挂载。断言 `docker compose config --quiet` 仍通过合法测试环境。

Run: `powershell -ExecutionPolicy Bypass -File deploy/preflight.ps1 -EnvFile deploy/.env.invalid`

Expected: FAIL，并列出每个缺失或不安全配置名，不回显值。

- [ ] **Step 2: 实现预检、部署、回滚脚本**

`preflight.ps1` 只检查配置和宿主机能力；`deploy.ps1` 顺序执行预检、`aicouncil-admin backup`、`docker compose pull/build`、`up -d --remove-orphans`、等待 `/readyz` 与 Web health；失败时调用 `rollback.ps1` 恢复上一版本标签。记录每次部署 manifest 到备份卷。

- [ ] **Step 3: 配置监控、日志轮转与持久化卷**

Compose 增加仅内部网络可访问的 Prometheus；Grafana dashboard 覆盖 HTTP、认证、协商/执行、Provider、SQLite 与磁盘指标。所有容器使用 JSON 日志的大小/文件轮转；Caddy 暴露 `/healthz`，不暴露 `/metrics`。

- [ ] **Step 4: 写操作手册**

在 `docs/operations/single-host.md` 给出 Windows Docker Desktop 与 Linux Docker Engine 的差异、DNS/TLS、最小防火墙规则、`deploy.env` 创建方式、SMTP/OAuth 回调、备份、恢复、升级、回滚与紧急停机步骤。README 只链接此手册，不复制密钥示例。

- [ ] **Step 5: 验证并提交**

Run: `powershell -ExecutionPolicy Bypass -File deploy/validate-compose.ps1`

Expected: PASS，且没有 secret 回显。

```bash
git add deploy README.md docs/operations/single-host.md
git commit -m "feat: add single-host production deployment"
```

### Task 8: 生产验收 CI 与端到端闭环

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`
- Create: `internal/e2e/production_lifecycle_test.go`
- Modify: `web/e2e/rbac.spec.ts`
- Modify: `web/e2e/council.spec.ts`
- Create: `deploy/smoke-test.ps1`

- [ ] **Step 1: 写真实授权执行闭环的失败测试**

构造 admin、owner、member 与 outsider；走注册/验证或受控测试登录，创建 workspace/provider/task，运行 Council，验证未经批准执行返回 409/403，批准后 Runner 执行并写验证制品，outsider 全程只能得到 403。重启 Runner 后重复 idempotency key 不重复执行。

Run: `go test ./internal/e2e -run TestProductionLifecycle -count=1`

Expected: FAIL，因为生产身份与所有权闭环尚未完全存在。

- [ ] **Step 2: 实现测试 fixtures 和 smoke test**

测试中使用 mock Provider 与临时 SQLite/制品卷；`smoke-test.ps1` 仅使用部署创建的专用 smoke 用户与一次性 workspace，完成 health、HTTPS、登录、审批受控拒绝和管理员 metrics 可达性检查，结束时清理 workspace。

- [ ] **Step 3: 扩展 CI 与发布工作流**

CI 增加 Go race（适用包）、漏洞扫描、Compose preflight、生产 lifecycle 和 Playwright Linux E2E。release 工作流只在 `v*` tag 触发，构建不可变镜像标签、生成 SBOM、运行 smoke image；发布到服务器仍由受保护环境的显式人工批准触发，不在普通 push 自动部署。

- [ ] **Step 4: 最终验证并提交**

Run: `go test ./... && corepack pnpm --dir web test && corepack pnpm --dir web build`

Expected: PASS。

```bash
git add .github internal/e2e web/e2e deploy/smoke-test.ps1
git commit -m "ci: verify production release lifecycle"
```

## Plan self-review

- 规格中的部署、两类登录、成员资源权限、加密 Key、审批执行、SQLite 恢复、监控、限流、CI、发布和验收都映射到了 Task 1–8。
- 计划不含待定占位或延后实现描述；每个任务都写出失败验证、最小实现、成功验证和独立提交边界。
- 非目标 Kubernetes、PostgreSQL、S3/MinIO、多机 Runner 与无人审批执行均未出现在任务中。
