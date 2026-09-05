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

## Web 登录与权限管理

使用 `--rbac` 开启 SQLite 用户、密码登录和按权限授权。`--rbac-role=operator` 仍可启用 RBAC，并为旧 Token bootstrap 指定角色；该参数不再要求所有请求具有同一角色。未启用 RBAC 时，现有 `--token` 静态 Bearer 模式和桌面启动方式保持原样；同时指定时以 RBAC 为准。

首次启动需要 `--rbac-bootstrap-subject` 与一种凭据：推荐通过 `COUNCIL_BOOTSTRAP_PASSWORD` 环境变量提供密码，也可使用 `--rbac-bootstrap-password`（命令行优先）。旧的 `--rbac-bootstrap-token` 仍可使用，但不能与密码或密码环境变量并存。缺少主体、缺少凭据或未启用 RBAC 的 bootstrap 配置都会拒绝启动。

密码 bootstrap 创建或修复 `admin` 角色，赋予管理和任务/工作区权限；Token bootstrap 对显式指定的兼容角色赋予同样权限（只使用 `--rbac` 时角色为 `admin`）。重复 bootstrap 保留已有密码、Token 和额外权限，不输出生成的访问 Token。已有 Token 用户如需浏览器登录，须显式执行密码 bootstrap 或由管理员设置密码。首次登录确认成功后，停止服务，移除 bootstrap 主体/凭据及环境变量，再以 `--rbac` 重启；正常重启只补齐权限目录，不重新授予已撤销的权限。

标准权限为 `workspace:read`、`workspace:write`、`task:read`、`task:write`、`task:approve`、`task:execute`、`admin:users`、`admin:roles`、`admin:permissions` 和 `admin:*`。`admin:*` 只覆盖 `admin:` 命名空间；普通角色需明确分配业务权限。管理员可从 `/admin/users` 管理用户和角色，`/login` 登录，`/account` 查看当前账户与退出。后端接口分别位于 `/api/v1/auth/*` 和 `/api/v1/admin/*`。

浏览器登录使用名为 `aicouncil_session` 的 `HttpOnly; Secure; SameSite=Strict` Cookie，期限为 8 小时；会话 Token 不返回给浏览器 JavaScript，也不放入 URL、localStorage 或 sessionStorage。桌面、CLI 和自动化仍可携带 `Authorization: Bearer ...`；服务端优先使用有效 Bearer，再尝试 Cookie。用户禁用、会话撤销或过期会使认证失效。前端和 API 必须经同一个域名、协议和端口访问。

### 生产：HTTPS 同域部署

`deploy/Caddyfile` 将 `/api/*`、`/healthz` 和 `/metrics` 转发到 Council，其余路径转发到 Web。TLS 在 Caddy 终止时，即使 Council 上游使用 HTTP，Cookie 也默认保持 Secure。`/healthz` 与 RBAC 模式下的 `/metrics` 是公开观测入口；需要限制指标可见性时，在代理或网络层增加访问限制。后端 `/shutdown` 不经此公开代理暴露。

下面是在同一台主机运行三个进程的 PowerShell 示例。先在终端 A 启动 Council（数据库应持久化并备份；用系统服务或密钥管理器注入生产密码）：

```powershell
$env:COUNCIL_BOOTSTRAP_PASSWORD = (Get-Credential -UserName admin -Message '设置首次管理员密码').GetNetworkCredential().Password
go run ./cmd/council-server --listen 127.0.0.1:8080 --db .data/council.db --rbac --rbac-bootstrap-subject admin
# 首次登录验证后按 Ctrl+C，然后仅保留普通启动参数：
Remove-Item Env:COUNCIL_BOOTSTRAP_PASSWORD
go run ./cmd/council-server --listen 127.0.0.1:8080 --db .data/council.db --rbac
```

终端 B 启动 Web：

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
pnpm --dir web exec next start --hostname 127.0.0.1 --port 3000
```

终端 C 为你的域名提供已签发的证书（将路径替换为实际文件）：

```powershell
$env:TLS_CERT = 'C:\certs\council.example.com.crt'
$env:TLS_KEY = 'C:\certs\council.example.com.key'
$env:COUNCIL_UPSTREAM = '127.0.0.1:8080'
$env:WEB_UPSTREAM = '127.0.0.1:3000'
caddy run --config deploy/Caddyfile --adapter caddyfile
```

将该域名解析到主机后，访问 `https://council.example.com/login`。生产环境不要设置 `AUTH_COOKIE_SECURE=false`。如 Council 自身终止 TLS，也可提供 `--tls-cert`/`--tls-key`，Cookie 配置对两种服务构造方式一致。

现有 `deploy/docker-compose.yml` 只包含 Council 和 Runner，尚未定义 Web 或 Caddy，不能单独提供上述同域控制台。容器部署需另行添加运行 Next.js 的 `web:3000` 服务及加载该 Caddyfile 的代理服务，将它们与 `council:8080` 接入同一网络并挂载证书；Caddyfile 默认使用这些服务名，也可通过 `WEB_UPSTREAM`/`COUNCIL_UPSTREAM` 覆盖。给 Council 服务添加 `--rbac` 和一次性 bootstrap 配置；Compose 中原有的 `AICOUNCIL_TOKEN` 环境变量不会自动开启 Web RBAC。

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
  @observability path /healthz /metrics
  handle @observability {
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
