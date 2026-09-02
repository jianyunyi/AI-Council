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

## 注意

Playwright 测试已纳入项目，但 Windows 当前环境可能无法启动 Chromium（`spawn UNKNOWN`）；请在 CI/Linux 或可用浏览器运行时执行完整浏览器 E2E。由于环境关闭 CGO，SQLite 使用纯 Go 的 `glebarez/go-sqlite` 驱动；`go test -race` 需在启用 CGO 的机器上运行。
