# AI Council

AI Council 是一个“多模型协同 + 人工批准执行”的本地优先开发工作台。它将 OpenAI、Anthropic、DeepSeek 等 Provider 归一化为同一契约，先并发产出独立方案，再匿名轮转互审，由 Judge 汇总并进行 Red-team 检查，最终生成需要人工确认的执行计划。

## 当前实现

- Go 核心状态机：`Draft → Analyzing → Proposing → Reviewing → Judging → RedTeam → AwaitingApproval → Executing → Verifying`。
- Provider 适配器：OpenAI Responses、Anthropic Messages、DeepSeek Chat Completions；429/鉴权错误统一归一化。
- GORM + SQLite：运行审计、版本化制品、模型调用用量；制品写入临时文件后原子替换并校验 SHA-256。
- Council：并发独立提案、确定性匿名别名、盲审、自审过滤、预算 meter、Judge/Red-team/执行计划。
- Runner 安全基础：一次性配对码、路径越界/敏感文件/大小限制、无 shell 的 argv 执行器、gRPC protobuf 契约。
- Next.js 控制台骨架：Provider、Workspace、Task 创建入口与类型化 API/SSE 客户端。

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

API Key 只应通过服务端配置或内存 Secret Vault 提供；不会写入 SQLite、制品 JSON、日志或浏览器 URL/localStorage。Runner 默认只读，执行请求必须携带与 run/workspace/plan 完全匹配的 approval hash。

## 注意

当前版本已完成安全核心与服务骨架；REST 生命周期编排、完整 gRPC Runner 实现、SSE 服务端和 Playwright E2E 将在后续迭代接入。由于环境关闭 CGO，SQLite 使用纯 Go 的 `glebarez/go-sqlite` 驱动；`go test -race` 需在启用 CGO 的机器上运行。
