# Changelog

本文件记录 AI Council 的所有重要变更。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Fixed

- **存量数据库权限纠正脚本**：新增 `docs/operations/upgrade-member-role-permissions.sql`。修复代码（member 角色不再默认授予 `task:approve`/`task:execute`）只影响新注册，旧数据库里已存的授权不会自动撤销；对修复前创建的 Council 数据库运行该脚本一次即可移除 member 角色的这两项越权授权。脚本按权限名（而非 id）关联、幂等、不影响其他角色，并有回归测试直接执行该文件验证。

## [0.2.0] - 2026-11-16

### Added

- **Windows 桌面版**：基于 Wails 的桌面壳，并提供 Inno Setup 安装包。安装包内含 Council 服务和 Workspace Runner。用户数据保存在 `%LOCALAPPDATA%\AI-Council`，升级和卸载都不会删除。
- 桌面端可以直接在界面上启动和停止本地服务，还可以用系统原生对话框选择工作区目录。
- 桌面端可以导出脱敏的诊断支持包（`ExportDiagnostics`）。
- 桌面端运行时与加密密钥存储：Provider 密钥用 DPAPI 保护，子进程日志有大小上限。
- **Council 指挥中心界面**：重新设计了任务创建、协商过程、制品审阅和审批界面。任务必须绑定一个已注册的工作区。
- **Web 账户与权限管理（RBAC）**：
  - 基于 SQLite 的用户、角色和权限目录，支持密码登录和账户禁用。
  - 浏览器会话使用 `HttpOnly; Secure; SameSite=Strict` Cookie，有效期 8 小时。会话 Token 不会交给浏览器脚本。
  - 新增 `/login`、`/account`、`/admin/users` 页面，后端接口位于 `/api/v1/auth/*` 和 `/api/v1/admin/*`。
  - 可以用 `--rbac-bootstrap-subject` 加 `COUNCIL_BOOTSTRAP_PASSWORD` 初始化管理员。
- **可执行的 Council 流程**：Runner 向模型提供有边界、经过过滤的工作区上下文快照。系统根据 Council 决策生成执行计划，并实现完整的审批生命周期：批准、拒绝、执行、验证，且审批只能使用一次。
- **生产部署**：Docker Compose 栈，包含 Caddy、Web、Council、Runner 和 Prometheus。Caddy 是唯一的公开入口，Runner 只在内部网络中运行。
- 部署工具：`deploy/preflight.ps1`、`deploy.ps1`、`rollback.ps1`、`backup.ps1`、`restore-verify.ps1`、`smoke-test.ps1`，以及 `aicouncil-admin backup|restore` 命令。
- 账户能力：邮箱注册、邮箱验证、密码重置（通过 SMTP 发信），以及可选的 GitHub / Google OAuth 登录。
- **资源归属与工作区共享**：工作区、任务、制品和 Provider 配置都记录所有者。所有者可以给其他成员授予 `viewer` 或 `editor` 权限，也可以撤销。
- **Runner 工作区注册表**：新增 `RegisterWorkspace` gRPC。Runner 在自己的 SQLite 中持久化“工作区 ID → 规范根路径”的绑定，未知 ID 一律拒绝。
- Provider API Key 用 AES-GCM 加密后存储，查询接口只返回掩码。
- 可观测性：`X-Request-ID` 请求关联、结构化请求日志，以及 `/readyz` 就绪检查（检查 SQLite 和 Runner 连通性）。新增 Prometheus 指标（HTTP、认证、限流、执行、Provider、SQLite）和 `deploy/grafana/dashboard.json`。
- 限流：登录、任务创建、启动、批准和执行接口都有限流，超限返回 429。
- CI：Go 测试、vet、部分包的 race 测试、govulncheck、生产生命周期 E2E、Web 测试与构建、Linux Playwright E2E、部署脚本契约测试和镜像构建。还有需要人工审批的 `release-readiness` 步骤，以及按 `v*` 标签发布带 SBOM 和 provenance 的 GHCR 镜像（也可以手动触发）。

- 工作区与执行恢复状态持久化，进程重启后可恢复未完成的执行记录。

### Changed

- 任务只能在已注册的工作区上创建。工作区创建改为提交相对于 Runner 根目录的路径。
- 启用 RBAC 时，授权改为按单项权限判断，不再要求所有请求属于同一个角色。`--token` 静态 Bearer 模式在未启用 RBAC 时保持不变。
- Council 启动时，会把已有工作区重新注册到 Runner。路径不在 Runner 根目录内的旧记录会让服务拒绝启动，不会被静默映射。
- Compose 构建支持配置 Go 模块镜像（`GOPROXY`）和 npm 镜像。

### Fixed

- RBAC 修复：
  - 被禁用的用户会正确失去访问权限。
  - 更新用户时保留原有身份和权限。
  - 拒绝集合级别的 PATCH 请求。
  - 加强了路由级授权。
  - 修补了会话撤销和过期方面的安全问题。
  - 错误日志中不会出现凭据。
- 工作区上下文读取：加强了对敏感文件、符号链接和二进制文件的过滤，并补齐了默认大小限制。
- 部署：当 `docker compose` 不可用时，可以回退到独立的 Compose 命令。

## [0.1.0] - 2026-09-02

### Added

- **核心基础与状态机**：Go 单体仓库，包含 Council Server 和 Workspace Runner 两个进程。运行状态机覆盖 `Draft → Analyzing → Proposing → Reviewing → Judging → RedTeam → AwaitingApproval → Executing → Verifying`，以及重新规划、失败、取消等终态分支。基于 GORM + SQLite 的运行审计和版本化制品存储，制品写入后原子替换并校验 SHA-256。
- **Provider 与 Council 协议**：OpenAI Responses、Anthropic Messages、DeepSeek Chat Completions 三个适配器，统一错误（限流、鉴权）归一化。API Key 只保存在内存中。功能包括：
  - 记录模型调用用量。
  - 并发产生独立提案。
  - 确定性的匿名别名，匿名轮转互审，并过滤自评。
  - Judge 汇总、Red-team 检查。
  - 法定人数与预算控制，最后生成执行计划。
- **审批绑定的 Workspace Runner**：
  - 一次性配对码；路径越界、敏感文件和大小限制。
  - 审批哈希不可变，与 run、workspace、plan 绑定。
  - 用户文件保护与文件事务。
  - 不经过 shell 的 argv 命令执行器。
  - 幂等执行、确定性验证、gRPC/protobuf 契约。
- **Web 控制台与端到端交付**：Next.js 控制台，包含 Provider、工作区和任务页面。提供类型化的 REST 客户端和可断线续传的 SSE 客户端，以及审批界面、验证报告和端到端安全测试用例。

[Unreleased]: https://github.com/jianyunyi/AI-Council/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/jianyunyi/AI-Council/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jianyunyi/AI-Council/releases/tag/v0.1.0
