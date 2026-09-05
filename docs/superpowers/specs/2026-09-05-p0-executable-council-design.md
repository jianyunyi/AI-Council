# AI Council P0 可执行协商闭环设计

**状态：** 已确认，待实施

## 目标

让用户配置的第三方模型 API 在受限工作区快照的基础上生成真实、可审查的补丁和命令；服务将其持久化为不可变计划，只有在用户明确批准后才可执行一次，并以真实执行及验收结果结束任务。

## 范围与边界

- 本阶段允许把工作区源码发送给用户已配置的 OpenAI、Anthropic 与 DeepSeek API。
- 源码读取仍只在 Runner 已配置的工作区根目录内进行；敏感路径、二进制文件、单文件大小、文件数量和总字节数均受既有或新增限制保护。
- 模型不得直接写文件或运行命令。只有携带审批哈希的 Runner gRPC 请求可以修改工作区。
- 本阶段不实现自动重规划。验收失败会以 `FAILED` 结束，用户须发起新任务并再次批准。
- 本阶段不扩展多工作区配对、后台队列、持续 SSE、mTLS 或部署编排；这些不属于 P0。

## 架构

Runner 新增只读工作区上下文 RPC。它枚举允许的文本文件，跳过敏感或超限项，返回根目录、Git 基本信息、以及带相对路径的内容快照。Council Server 在创建任务时已经知道 workspace ID；启动任务前通过该 RPC 取得快照，并将快照、需求及验收条件作为 `TaskBrief` 发送给 Council Engine。

Council 的提案、互审、Judge、Red-team 保持现有角色分工。Judge 额外返回严格 JSON 的候选执行计划。服务端仅接受能够解析为 `ExecutionPlan`、路径和命令均通过安全校验、且至少有一个补丁或命令的计划。Red-team 报告含阻断项或计划无效时，任务失败且不会出现可批准计划。

Council REST API 作为唯一运行时编排器：每个真实阶段完成后持久化状态和事件；任务内存缓存只作为加载后的缓存，而不是状态真相。执行前必须原子消费当前审批。执行使用由 task ID 和 plan version 派生的稳定 request ID，使重试得到同一 Runner 结果；任何非 `AWAITING_APPROVAL` 状态或已消费批准都会返回冲突。

## 数据流

1. 用户创建任务并提供需求、验收条件与已注册工作区。
2. `start` 调用 Runner 读取受限上下文，持久化 `ANALYZING`。
3. Council 依次完成提案、互审、裁决和红队审查；每阶段成功后才持久化下一个状态和事件。
4. 服务验证并持久化实际 `ExecutionPlan` 与 SHA-256 审批哈希，进入 `AWAITING_APPROVAL`。
5. 用户检查计划、补丁、命令、风险与验收标准后，提交匹配的版本和哈希。
6. `execute` 在数据库事务中确认并消费批准，将任务转换到 `EXECUTING`，再调用 Runner。
7. Runner 应用批准补丁，运行已批准命令及验收命令，返回每个步骤结果。
8. 服务持久化 `VERIFYING`；成功转 `SUCCEEDED`，失败转 `FAILED`。执行或验证均不可隐式重新批准或重新执行。

## 协议与验证

`WorkspaceContext` 至少包含 canonical root、Git 状态、文本文件清单及截取后的 UTF-8 内容。文件路径必须是相对根目录路径，且不得匹配敏感或忽略策略。返回内容有明确的最大文件数、最大单文件字节数和最大总字节数。

`ExecutionPlan` 至少包含非空的 `patches` 或 `commands`，并带有来自任务的验收条件。每个补丁路径使用 Runner path guard 验证；每个命令仍经 Runner argv、工作目录和超时策略验证。服务端将模型 JSON 解析后的结构再次校验，不信任模型声称的文件或路径安全性。

Runner 的响应应明确区分补丁、计划命令和验收命令的结果。任一命令失败、超时或验收项不通过时，响应为失败；若已写入补丁，按现有事务恢复策略回滚。

## 状态与失败处理

允许的运行路径为：

`DRAFT → ANALYZING → PROPOSING → REVIEWING → JUDGING → REDTEAM → AWAITING_APPROVAL → EXECUTING → VERIFYING → SUCCEEDED | FAILED`

上下文读取、模型调用、计划解析、红队阻断、Runner RPC 和验证任一失败均记录错误事件并转为 `FAILED`。`cancel` 仅对未执行状态有效；`reject` 将当前计划设为不可执行并转为 `CANCELLED`。数据库写失败必须返回错误，不能只在内存中继续。

## 测试与验收

- Runner 测试证明上下文读取不会泄露工作区外、敏感或二进制内容，并遵守全部大小上限。
- Council 测试证明 Judge 的结构化计划可被解析，空计划、非法路径和 Red-team 阻断无法进入审批。
- REST 集成测试证明状态按真实阶段写入、计划与审批哈希落盘、执行请求使用稳定 request ID。
- REST 集成测试证明已成功、失败、取消或已消费批准的任务不能再次执行。
- 端到端测试使用受控 Provider/Runner 夹具：生成补丁、批准、修改测试工作区、运行验收命令并得到 `SUCCEEDED`；验收命令失败时得到 `FAILED` 且补丁被恢复。

## 非目标

- 自动重规划和无需再次批准的重试。
- 多 Runner/远程工作区调度与完整配对管理。
- 长时间后台任务、持续事件推送和任务队列。
- Provider 账单、指标、原生 TLS/mTLS 和发布流水线改造。
