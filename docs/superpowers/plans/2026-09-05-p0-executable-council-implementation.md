# AI Council P0 可执行协商闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Council 从受限工作区上下文生成真实的结构化补丁、命令和验证命令，并通过一次性人工批准完成可恢复、不可重复的执行闭环。

**Architecture:** Runner 在现有 gRPC 服务中暴露只读工作区快照 RPC，并继续作为所有路径与命令安全校验的唯一边界。Council Engine 将快照放入 `TaskBrief`，Judge 输出包含 `ExecutionPlan` 的结构化决策；REST API 按实际完成的阶段持久化任务，并用 SQLite 事务消费批准后调用 Runner。Runner 在一次独立事务中应用补丁、运行计划命令和验证命令，返回成功或失败的明确结果。

**Tech Stack:** Go 1.25、gRPC/protobuf、Gin、go-zero、GORM/SQLite、现有 Go 测试与 Playwright。

---

## 文件结构

- `proto/runner/v1/runner.proto`：新增只读工作区上下文消息与验证命令字段。
- `internal/runner/context/*`：文件枚举、敏感路径过滤、文本和大小限制。
- `internal/runner/grpc/service.go`：实现上下文 RPC；每次执行使用独立文件事务；运行验证命令。
- `internal/council/schema/types.go`：使 Judge 决策显式包含候选执行计划和验证命令。
- `internal/council/engine.go`：验证模型 JSON 并从 Judge 决策构建非空计划。
- `internal/transport/council/routes.go`：真实阶段持久化、Runner 上下文调用、稳定执行请求 ID、一次性批准消费。
- `internal/storage/sqlite/*`：为审批消费与任务状态转换提供可检查的事务接口。

### Task 1: 定义并实现受限工作区上下文 RPC

**Files:**

- Modify: `proto/runner/v1/runner.proto`
- Create: `internal/runner/context/collector.go`
- Create: `internal/runner/context/collector_test.go`
- Modify: `internal/runner/grpc/service.go`
- Modify: `internal/runner/grpc/service_test.go`
- Regenerate: `internal/runner/rpc/generated/runner.pb.go`, `internal/runner/rpc/generated/runner_grpc.pb.go`

- [ ] **Step 1: 写入失败的 collector 测试**

在临时工作区创建 `main.go`、`.env`、`binary.bin` 和超限文本文件；断言收集结果只包含 `main.go`，路径是相对路径，且总字节数不超过限制。

```go
func TestCollectorExcludesSensitiveBinaryAndOversizedFiles(t *testing.T) {
    root := t.TempDir()
    require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\\n"), 0o600))
    require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=x"), 0o600))
    require.NoError(t, os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0o600))
    require.NoError(t, os.WriteFile(filepath.Join(root, "large.txt"), bytes.Repeat([]byte("x"), 33), 0o600))
    snapshot, err := contextpkg.NewCollector(contextpkg.Limits{MaxFiles: 10, MaxFileBytes: 32, MaxTotalBytes: 32}).Collect(context.Background(), root)
    require.NoError(t, err)
    require.Equal(t, []contextpkg.File{{Path: "main.go", Content: "package main\\n"}}, snapshot.Files)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/runner/context -run TestCollectorExcludesSensitiveBinaryAndOversizedFiles -count=1`

Expected: FAIL，因为 `internal/runner/context` 尚不存在。

- [ ] **Step 3: 实现最小收集器**

实现 `Limits`、`File`、`Snapshot` 和 `Collector.Collect`。使用 `filepath.WalkDir`，拒绝符号链接、`.git`、`.env*`、密钥扩展名和非 UTF-8/含 NUL 内容；只返回排序后的相对路径。默认限制为 200 个文件、每文件 64 KiB、总计 2 MiB。

```go
type Limits struct { MaxFiles, MaxFileBytes, MaxTotalBytes int }
type File struct { Path, Content string }
type Snapshot struct { Root string; Files []File }
func (c Collector) Collect(ctx context.Context, root string) (Snapshot, error)
```

- [ ] **Step 4: 运行 collector 测试确认通过**

Run: `go test ./internal/runner/context -count=1`

Expected: PASS。

- [ ] **Step 5: 扩展 protobuf 契约并生成代码**

新增 `ReadWorkspaceContext` RPC、`WorkspaceFile` 和读上下文请求/响应。给 `ExecuteApprovedPlanRequest` 添加 `repeated ApprovedCommand verification_commands`；不得把自然语言 `acceptance` 当作 shell 命令。运行：

```powershell
protoc --go_out=. --go-grpc_out=. proto/runner/v1/runner.proto
```

- [ ] **Step 6: 写入并运行失败的 RPC 测试**

在 `Service` 测试中用临时工作区调用 `ReadWorkspaceContext`，断言返回允许文件且没有 `.env`。

Run: `go test ./internal/runner/grpc -run TestReadWorkspaceContext -count=1`

Expected: FAIL，因为服务尚未实现新的 RPC。

- [ ] **Step 7: 实现 RPC 并确认通过**

`Service.ReadWorkspaceContext` 调用 Collector 并复用 `DescribeWorkspace` 的 Git 信息；不得因调用方提供的 workspace ID 改变 Runner 根目录。

Run: `go test ./internal/runner/grpc ./internal/runner/context -count=1`

Expected: PASS。

- [ ] **Step 8: 提交协议与上下文读取功能**

```powershell
git add proto/runner/v1/runner.proto internal/runner/context internal/runner/grpc/service.go internal/runner/grpc/service_test.go internal/runner/rpc/generated
git commit -m "feat: expose bounded runner workspace context"
```

### Task 2: 让 Council 决策产生可验证的执行计划

**Files:**

- Modify: `internal/council/schema/types.go`
- Modify: `internal/council/engine.go`
- Modify: `internal/council/engine_test.go`
- Modify: `internal/council/workflow.go`
- Modify: `internal/council/workflow_test.go`

- [ ] **Step 1: 写入失败的结构化决策测试**

构造带一个补丁和一个验证命令的 `CouncilDecision`；断言 `BuildExecutionPlan` 保留这些内容与传入验收标准。另断言空补丁、空命令的决策返回错误。

```go
func TestBuildExecutionPlanRejectsEmptyDecision(t *testing.T) {
    _, err := BuildExecutionPlan(schema.CouncilDecision{}, schema.RedTeamReport{}, []string{"go test ./..."})
    require.Error(t, err)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/council -run TestBuildExecutionPlanRejectsEmptyDecision -count=1`

Expected: FAIL，因为当前实现将空计划视为有效。

- [ ] **Step 3: 扩展 schema 并最小实现计划构建**

给 `CouncilDecision` 增加 `Plan schema.ExecutionPlan`，给 `ExecutionPlan` 增加 `VerificationCommands []Command`，给 `TaskBrief` 增加 `WorkspaceFiles []WorkspaceFile`，其中 `WorkspaceFile` 仅含 `Path` 与 `Content`。`BuildExecutionPlan` 复制 decision plan，覆盖任务验收标准，拒绝空补丁和命令、非正版本、空路径、空 executable、非正 timeout，以及有阻断项的红队报告。

```go
type CouncilDecision struct { SelectedAliases []string; Reasons []string; Rejected map[string][]string; PlanSummary []string; Plan ExecutionPlan `json:"plan"` }
type ExecutionPlan struct { Version int; Patches []Patch; Commands []Command; VerificationCommands []Command `json:"verification_commands"`; Acceptance []string; Recovery []string }
type WorkspaceFile struct { Path string `json:"path"`; Content string `json:"content"` }
```

- [ ] **Step 4: 给 workflow 写入失败测试**

使用假的 ModelProvider 依次返回提案、审查、带计划的决策与无阻断红队报告；断言 `Deliberate` 返回同一补丁、命令及验证命令。

Run: `go test ./internal/council -run TestWorkflowDeliberateReturnsJudgeExecutionPlan -count=1`

Expected: FAIL，直到 Judge schema 和 `BuildExecutionPlan` 均使用新字段。

- [ ] **Step 5: 更新 prompt 与 workflow 并确认通过**

保留现有 JSON schema 序列化机制；schema 包含 `Plan` 后 Judge prompt 自动要求该字段。将 `TaskBrief` 扩展为带受限文本快照，Propose prompt 只发送该快照。`Workflow.Deliberate` 只在 `BuildExecutionPlan` 成功后返回计划。

Run: `go test ./internal/council -count=1`

Expected: PASS。

- [ ] **Step 6: 提交 Council 可执行计划**

```powershell
git add internal/council/schema/types.go internal/council/engine.go internal/council/engine_test.go internal/council/workflow.go internal/council/workflow_test.go
git commit -m "feat: derive executable plans from council decisions"
```

### Task 3: 使 Runner 以独立事务运行计划和验证命令

**Files:**

- Modify: `internal/runner/grpc/service.go`
- Modify: `internal/runner/grpc/service_test.go`
- Modify: `internal/runner/files/transaction.go`
- Modify: `internal/runner/files/transaction_test.go`

- [ ] **Step 1: 写入失败的执行与验证测试**

创建包含补丁、普通命令和失败验证命令的批准请求。断言响应为 `FAILED`，包含 kind 为 `verification` 的步骤，且补丁内容恢复为执行前内容。

```go
func TestExecuteApprovedPlanRestoresPatchWhenVerificationFails(t *testing.T) {
    response, err := service.ExecuteApprovedPlan(ctx, request)
    require.NoError(t, err)
    require.Equal(t, "FAILED", response.Status)
    require.Equal(t, original, readFile(t, target))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/runner/grpc -run TestExecuteApprovedPlanRestoresPatchWhenVerificationFails -count=1`

Expected: FAIL，因为请求尚无验证命令且服务不会运行它们。

- [ ] **Step 3: 让文件事务成为每请求实例**

从 `Service` 删除共享 `transaction` 字段；在 `ExecuteApprovedPlan` 内用 `files.NewTransaction(s.guard)` 创建本次事务。这样并发请求不共享快照。

- [ ] **Step 4: 运行普通与验证命令**

将 protobuf 的 `VerificationCommands` 转换为 `schema.ExecutionPlan.VerificationCommands`，在普通命令成功后运行它们，并设置 `StepResult.Kind = "verification"`。任一步失败、超时或非零退出时将响应设为 `FAILED`、恢复本次事务并调用 `idem.Complete`。

- [ ] **Step 5: 运行 Runner 测试确认通过**

Run: `go test ./internal/runner/grpc ./internal/runner/files ./internal/runner/idempotency -count=1`

Expected: PASS。

- [ ] **Step 6: 提交 Runner 执行修复**

```powershell
git add internal/runner/grpc/service.go internal/runner/grpc/service_test.go internal/runner/files/transaction.go internal/runner/files/transaction_test.go
git commit -m "fix: verify approved plans in isolated runner transactions"
```

### Task 4: 以真实阶段与一次性批准编排 REST 生命周期

**Files:**

- Modify: `internal/transport/council/routes.go`
- Modify: `internal/transport/council/routes_test.go`
- Modify: `internal/storage/sqlite/approval_repository.go`
- Modify: `internal/storage/sqlite/event_approval_repository_test.go`

- [ ] **Step 1: 写入失败的真实状态序列测试**

用有序 fake Council 和 fake Runner 调用 `start`，断言数据库事件按 `ANALYZING`、`PROPOSING`、`REVIEWING`、`JUDGING`、`REDTEAM`、`AWAITING_APPROVAL` 顺序出现，且每个状态只在其实际调用成功后写入。

```go
func TestStartTaskPersistsOnlyCompletedCouncilStages(t *testing.T) {
    api := persistentAPIWithStageCouncil(t, failAt("REVIEWING"))
    recorder := startTask(t, api, taskID)
    require.Equal(t, http.StatusBadGateway, recorder.Code)
    require.Equal(t, []string{"ANALYZING", "PROPOSING"}, persistedStates(t, api, taskID))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/transport/council -run TestStartTaskPersistsOnlyCompletedCouncilStages -count=1`

Expected: FAIL，因为当前 `startTask` 在 `Deliberate` 后批量伪造所有阶段。

- [ ] **Step 3: 拆分 Council workflow 的阶段端口**

在 `internal/transport/council/routes.go` 定义 `stageCouncil`：`DeliberateWithStages(context.Context, schema.TaskBrief, func(string) error) (schema.ExecutionPlan, error)`。`Workflow.DeliberateWithStages` 在 Proposal、Review、Judge、RedTeam 每次成功后依次调用回调；REST 回调使用一个返回错误的 `transitionTask` 助手，先更新 SQLite，再更新内存缓存、追加事件。任何持久化错误立刻返回，禁止静默忽略。

- [ ] **Step 4: 写入失败的一次性执行测试**

批准并成功执行同一任务一次后，用相同审批哈希再次调用 `execute`，断言 HTTP 409 且 fake Runner 只收到一次调用。

```go
func TestExecuteTaskConsumesApprovalExactlyOnce(t *testing.T) {
    approve(t, api, taskID)
    require.Equal(t, http.StatusOK, execute(t, api, taskID).Code)
    require.Equal(t, http.StatusConflict, execute(t, api, taskID).Code)
    require.Equal(t, 1, runner.executeCalls)
}
```

- [ ] **Step 5: 原子消费批准并执行稳定请求 ID**

在 `ApprovalRepository` 增加 `Consume(ctx, runID string, planVersion int, hash string) (bool, error)`：单个数据库事务内查找未失效、匹配版本与哈希的 approved record，标记为 consumed。`executeTask` 只允许 `AWAITING_APPROVAL`，先 `Consume`，再转 `EXECUTING`。请求 ID 固定为 `taskID + ":" + strconv.Itoa(planVersion)`；Runner 成功后转 `VERIFYING` 再转 `SUCCEEDED`，否则转 `FAILED`。

- [ ] **Step 6: 将真实计划下发给 Runner**

把 `Plan.VerificationCommands` 映射到 `ExecuteApprovedPlanRequest.VerificationCommands`。在启动阶段通过 `ReadWorkspaceContext` 取得快照并将其传给 Council；没有配置 Council 或 Runner context client 时返回明确错误，不再生成空计划。

- [ ] **Step 7: 运行 REST 和 SQLite 测试确认通过**

Run: `go test ./internal/transport/council ./internal/storage/sqlite -count=1`

Expected: PASS。

- [ ] **Step 8: 提交真实 REST 编排**

```powershell
git add internal/transport/council/routes.go internal/transport/council/routes_test.go internal/storage/sqlite/approval_repository.go internal/storage/sqlite/event_approval_repository_test.go
git commit -m "fix: persist real council stages and consume approvals"
```

### Task 5: 证明完整闭环并更新操作文档

**Files:**

- Modify: `internal/e2e/council_test.go`
- Modify: `web/e2e/council.spec.ts`
- Modify: `README.md`

- [ ] **Step 1: 写入失败的真实闭环 E2E**

创建临时 Git 工作区和受控 Provider，令 Judge 返回一份修改文件的补丁、一条普通命令和一条成功验证命令。通过 Council API 创建、启动、批准和执行任务；断言文件变更、Runner 步骤、最终 `SUCCEEDED` 与第二次执行的 409。

```go
func TestCouncilApprovalExecutionVerificationLifecycle(t *testing.T) {
    id := createStartAndApprove(t, api, root)
    require.Equal(t, "SUCCEEDED", getTask(t, api, id).State)
    require.Contains(t, readFile(t, filepath.Join(root, "message.txt")), "council")
    require.Equal(t, http.StatusConflict, execute(t, api, id).Code)
}
```

- [ ] **Step 2: 运行 E2E 确认失败**

Run: `go test ./internal/e2e -run TestCouncilApprovalExecutionVerificationLifecycle -count=1`

Expected: FAIL，直到 Task 1–4 全部接通。

- [ ] **Step 3: 实现夹具并验证通过**

夹具必须使用真实 Runner 服务、真实审批哈希和真实 SQLite；仅 Provider 传输可使用确定性 fake，禁止 mock Runner 或 REST 状态转换。

Run: `go test ./internal/e2e -count=1`

Expected: PASS。

- [ ] **Step 4: 更新 README 操作边界**

记录：发送给模型的是受限工作区文本快照；计划在批准前可查看；执行只能一次；失败不会自动重规划；敏感文件、二进制和超限文件不会发送给 Provider。

- [ ] **Step 5: 运行仓库级验证**

```powershell
go test ./...
go vet ./...
pnpm --dir web test
pnpm --dir web build
git diff --check
```

Expected: 所有命令退出码为 0。

- [ ] **Step 6: 提交 P0 闭环**

```powershell
git add internal/e2e/council_test.go web/e2e/council.spec.ts README.md
git commit -m "test: prove executable council approval lifecycle"
```

## 计划自审

- 协议、受限源码读取、真实结构化计划、验证命令、审批一次性消费和端到端证明均有独立任务。
- 所有生产行为在写实现前都有一个明确的失败测试和运行命令。
- 未引入自动重规划、后台队列、多 Runner、发布或可观测性等规格外功能。
- 每个提交边界包含可独立验证的功能，不依赖未列出的类型或模糊步骤。
