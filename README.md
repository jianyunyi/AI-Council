# AI Council

## Windows 桌面版

Windows 安装包包含 Wails 桌面壳、Council 服务和 Workspace Runner。安装后从开始菜单启动，运行数据、DPAPI 保护的 Provider 密钥和限额日志保存在 `%LOCALAPPDATA%\AI-Council`，升级与卸载不会删除这些用户数据。

本地构建：先执行 `pnpm --dir web install --frozen-lockfile` 和 `pnpm --dir web desktop:build`，再构建三个 Windows 可执行文件至 `dist`，最后用 Inno Setup 执行 `iscc build/windows/installer.iss`。GitHub 标签构建会自动生成 EXE、安装包及 SHA-256 文件。

桌面壳的 `ExportDiagnostics(destination)` 可生成脱敏支持包；详见 [build/diagnostics.md](build/diagnostics.md)。

AI Council 是一个“多模型协同 + 人工批准执行”的本地优先开发工作台。它将 OpenAI、Anthropic、DeepSeek 等 Provider 归一化为同一契约，先并发产出独立方案，再匿名轮转互审，由 Judge 汇总并进行 Red-team 检查，最终生成需要人工确认的执行计划。

## 当前实现

- Go 核心状态机：`Draft → Analyzing → Proposing → Reviewing → Judging → RedTeam → AwaitingApproval → Executing → Verifying`。
- Provider 适配器：OpenAI Responses、Anthropic Messages、DeepSeek Chat Completions；429/鉴权错误统一归一化。
- GORM + SQLite：运行审计、版本化制品、模型调用用量；制品写入临时文件后原子替换并校验 SHA-256。
- Council：并发独立提案、确定性匿名别名、盲审、自审过滤、预算 meter、Judge/Red-team/执行计划。
- Runner 安全基础：一次性配对码、路径越界/敏感文件/大小限制、无 shell 的 argv 执行器、gRPC protobuf 契约。
- Next.js 控制台骨架：Provider、Workspace、Task 创建入口与类型化 API/SSE 客户端。
- REST 持久化生命周期：任务启动会调用可配置的 Council Workflow，生成计划/审批哈希并持久化；批准后通过真实 Runner gRPC 下发完整 patch、command 和 acceptance。
- Runner 生产能力：SQLite 幂等记录支持跨进程恢复和 running 抢占；gRPC Bearer Token interceptor；Workspace Git 状态探测；可选 TLS 证书热加载。
- 生产观测与权限：业务 Prometheus 指标、Grafana dashboard、SQLite 用户/角色/RBAC 中间件、Provider 重试和模型价格表成本估算。

## 本地运行

```powershell
$env:GOCACHE="$env:TEMP\ai-council-go-cache"
go test ./...
go vet ./...
go run ./cmd/council-server -listen 127.0.0.1:18080
go run ./cmd/workspace-runner -listen 127.0.0.1:18081
pnpm --dir web install
pnpm --dir web dev
```

配置真实 Council Workflow 时设置 `OPENAI_API_KEY`、`DEEPSEEK_API_KEY`、`ANTHROPIC_API_KEY`（可选）及对应 `*_MODEL`；服务启动后任务 `start` 会执行 Analyze → Propose → Review → Judge → Red-team。Runner gRPC 可通过 `-tls-cert`/`-tls-key` 开启证书热加载。

API Key 只应通过服务端配置或内存 Secret Vault 提供；不会写入 SQLite、制品 JSON、日志或浏览器 URL/localStorage。Runner 默认只读，执行请求必须携带与 run/workspace/plan 完全匹配的 approval hash。

## Execution safety boundaries

- 发送给已配置模型 Provider 的仅是 Runner 过滤后的、有边界的工作区快照；敏感、二进制、符号链接和超限文件均会排除。
- 执行前必须由人工基于计划 hash 明确批准；批准只可消费一次，重复执行会被拒绝。
- 执行失败后不会自动重新规划或重试，须由用户审查并发起新的流程。

## Web 登录与权限管理

使用 `--rbac` 开启 SQLite 用户、密码登录和按权限授权。`--rbac-role=operator` 仍可启用 RBAC，并为旧 Token bootstrap 指定角色；该参数不再要求所有请求具有同一角色。未启用 RBAC 时，现有 `--token` 静态 Bearer 模式和桌面启动方式保持原样；同时指定时以 RBAC 为准。

首次启动需要 `--rbac-bootstrap-subject` 与一种凭据：推荐通过 `COUNCIL_BOOTSTRAP_PASSWORD` 环境变量提供密码，也可使用 `--rbac-bootstrap-password`（命令行优先）。旧的 `--rbac-bootstrap-token` 仍可使用，但不能与密码或密码环境变量并存。缺少主体、缺少凭据或未启用 RBAC 的 bootstrap 配置都会拒绝启动。

密码 bootstrap 创建或修复 `admin` 角色，赋予管理和任务/工作区权限；Token bootstrap 对显式指定的兼容角色赋予同样权限（只使用 `--rbac` 时角色为 `admin`）。重复 bootstrap 保留已有密码、Token 和额外权限，不输出生成的访问 Token。已有 Token 用户如需浏览器登录，须显式执行密码 bootstrap 或由管理员设置密码。首次登录确认成功后，停止服务，移除 bootstrap 主体/凭据及环境变量，再以 `--rbac` 重启；正常重启只补齐权限目录，不重新授予已撤销的权限。

标准权限为 `workspace:read`、`workspace:write`、`task:read`、`task:write`、`task:approve`、`task:execute`、`admin:users`、`admin:roles`、`admin:permissions` 和 `admin:*`。`admin:*` 只覆盖 `admin:` 命名空间；普通角色需明确分配业务权限。管理员可从 `/admin/users` 管理用户和角色，`/login` 登录，`/account` 查看当前账户与退出。后端接口分别位于 `/api/v1/auth/*` 和 `/api/v1/admin/*`。

浏览器登录使用名为 `aicouncil_session` 的 `HttpOnly; Secure; SameSite=Strict` Cookie，期限为 8 小时；会话 Token 不返回给浏览器 JavaScript，也不放入 URL、localStorage 或 sessionStorage。桌面、CLI 和自动化仍可携带 `Authorization: Bearer ...`；服务端优先使用有效 Bearer，再尝试 Cookie。用户禁用、会话撤销或过期会使认证失效。前端和 API 必须经同一个域名、协议和端口访问。

### 生产：HTTPS 同域 Compose 部署

生产 Compose 栈已包含 Caddy、Next.js Web、Council 和 Workspace Runner；Caddy 是唯一公开入口，TLS 在此终止。Council 和 Runner 不发布主机端口，Runner 位于私有网络，Council 仅额外拥有调用配置模型 Provider 所需的出站网络。

完整的先决条件、密钥与证书路径、验证、启动、首次登录、升级、优雅停机、数据卷备份和 metrics 安全边界见 [Compose 生产部署指南](docs/deployment/compose-production.md)。生产环境不要设置 `AUTH_COOKIE_SECURE=false`。后端 `/shutdown` 和 `/metrics` 不经过公开 Caddy 路由暴露。

### GitHub Actions CI 与发布准备

仓库的 `ci` 工作流会在 push 和 pull request 时自动运行 Go 全量测试与 vet、Web Vitest 与构建、Linux Chromium Playwright E2E、Compose 构建配置校验，以及 Council、Runner、Web 三个本地镜像构建。镜像构建只验证 Dockerfile 和 Compose 配置：工作流不会推送镜像、不会部署到服务器，也不会读取 Provider API Key。

需要人工确认某个已验证提交具备发布条件时，在 GitHub 网页依次进入 **Actions → ci → Run workflow → master → Run workflow**。这会在其余检查通过后进入 `release-readiness`。该任务使用 `production` Environment；仓库管理员必须先在 **Settings → Environments → production** 配置 required reviewers，GitHub 才会在这里暂停并等待人工审批。审批任务仅写入发布准备摘要，仍不会发布镜像或执行部署。

CI 的“发布准备”与本地/服务器 Compose 部署是两件事：前者验证提交并可请求人工审核；后者仍须由已获授权的运维人员按部署指南准备证书与密钥，并显式运行 `docker compose ... up -d --build`。

### 本地：HTTP 同域开发

仅本地 HTTP 开发时，在 Council 终端显式设置 `AUTH_COOKIE_SECURE=false`。任何其他值（含未设置）都会保留 Secure。使用独立开发数据库，并保持前端与 API 共用下面代理的 `http://localhost:8088`：

```powershell
$env:AUTH_COOKIE_SECURE = 'false'
$env:COUNCIL_BOOTSTRAP_PASSWORD = (Get-Credential -UserName admin -Message '设置本地管理员密码').GetNetworkCredential().Password
go run ./cmd/council-server --listen 127.0.0.1:8080 --db .data/council-dev.db --rbac --rbac-bootstrap-subject admin
# 首次登录后按 Ctrl+C，移除 bootstrap 环境变量和主体参数再重启：
Remove-Item Env:COUNCIL_BOOTSTRAP_PASSWORD
go run ./cmd/council-server --listen 127.0.0.1:8080 --db .data/council-dev.db --rbac
```

另一个终端运行 `pnpm --dir web dev --hostname 127.0.0.1 --port 3000`。将以下内容保存为本地 `Caddyfile.local`，再运行 `caddy run --config Caddyfile.local --adapter caddyfile`：

```caddyfile
http://localhost:8088 {
  handle /api/* {
    reverse_proxy 127.0.0.1:8080
  }
  handle /healthz {
    reverse_proxy 127.0.0.1:8080
  }
  handle {
    reverse_proxy 127.0.0.1:3000
  }
}
```

通过 `http://localhost:8088/login` 登录、`http://localhost:8088/account` 查看账户、`http://localhost:8088/admin/users` 管理权限。直接访问 Web 的 3000 端口不会把 `/api/*` 代理到 Council；关闭本地开发后移除 `AUTH_COOKIE_SECURE` 环境变量。

## 注意

Playwright 测试已纳入项目，但 Windows 当前环境可能无法启动 Chromium（`spawn UNKNOWN`）；请在 CI/Linux 或可用浏览器运行时执行完整浏览器 E2E。由于环境关闭 CGO，SQLite 使用纯 Go 的 `glebarez/go-sqlite` 驱动；`go test -race` 需在启用 CGO 的机器上运行。
