# AI Council 生产安全与可观测性设计

日期：2026-09-01  
状态：设计已确认

## 目标与范围

本阶段将 AI Council 从“可运行的生产闭环”推进到可运营的生产服务，按以下顺序交付：

1. RBAC 端到端认证与授权
2. Council/Runner 原生 TLS 证书热加载
3. Prometheus 业务指标、Provider 成本计量和 Grafana Dashboard

本阶段保持本地优先架构、SQLite 持久化、Council HTTP 与 Runner gRPC 进程边界，以及现有静态 Bearer Token 的兼容能力。不引入外部身份提供商、分布式数据库或强制 Caddy 依赖。

## 一、RBAC 认证与授权

### 数据模型

- `users`：用户名、密码哈希、启用状态、时间戳。
- `roles`：唯一名称和描述。
- `permissions`：资源和动作组成的唯一权限，例如 `task:read`、`task:approve`、`task:execute`。
- `user_roles` 与 `role_permissions`：用户-角色、角色-权限多对多关系。
- `access_tokens`：只保存 Token 哈希、用户 ID、过期时间、撤销时间和创建时间，绝不保存明文 Token。

密码使用 Argon2id 哈希。数据库迁移必须幂等，重启后用户、角色、权限和撤销状态保持不变。

### 认证模式

通过 `AUTH_MODE` 选择：

- `static`：继续使用现有静态 Bearer Token。
- `rbac`：仅接受数据库用户签发的 Token。
- `hybrid`：同时接受静态 Token 和数据库 Token，支持平滑迁移。

首个管理员通过环境变量或一次性 bootstrap 命令创建；bootstrap 不回显密码，不把密钥或 Token 写入日志。

### API 与权限

公开认证接口：

- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/me`

管理员接口：

- `GET/POST/PATCH /api/v1/admin/users`
- `GET/POST/PATCH /api/v1/admin/roles`
- `GET /api/v1/admin/permissions`

业务路由使用最小权限：Task/Workspace 读写、Approval 审批、Execution 执行分别授权。中间件将用户、角色和权限注入请求上下文，并将主体写入审计事件。未认证返回 401，已认证但越权返回 403，响应不泄漏用户或 Token 是否存在的额外信息。

### 测试

覆盖登录与错误凭据、Token 撤销和过期、角色权限判断、越权拒绝、管理员 bootstrap、SQLite 重启恢复、静态/hybrid 兼容，以及审计主体记录。

## 二、原生 TLS 与证书热加载

### 统一 TLS 配置

扩展现有 `tlsconfig.Reloader`，使用原子证书指针和握手回调 `GetCertificate`。后台定时检查证书与私钥文件变化，成功后原子替换；加载失败保留旧证书并记录结构化错误。

Council HTTP 与 Runner gRPC 均支持服务端证书、可选 CA、可选 mTLS。Council 到 Runner 的 gRPC 客户端使用相同的 CA/客户端证书配置。CLI 参数包括：

- `-tls-cert`
- `-tls-key`
- `-tls-ca`
- `-tls-client-auth`

启动时校验证书、私钥和 CA。已有连接不因轮换中断，新连接使用新证书。Caddy 终止 TLS 的部署仍然受支持。

### 运维信号与测试

暴露当前证书过期时间、最近成功加载时间和加载失败计数；可通过健康检查或 Prometheus 查询。测试覆盖初始加载、有效替换、无效替换回滚、mTLS 客户端校验，以及 HTTP/gRPC 启停配置。

## 三、Prometheus、成本计量与 Grafana

### 指标

指标分为 Council 阶段、Runner 执行、HTTP/SSE、认证安全和资源五类。标签只使用低基数值：`provider`、`model`、`phase`、`status` 等；禁止 task ID、用户 ID 等高基数标签。

至少提供任务启动/成功/失败、阶段耗时、Provider 调用/重试/错误、Runner 执行/验证/幂等命中、HTTP 状态与延迟、SSE 连接、认证失败、权限拒绝和 TLS 加载失败指标。

### Provider 成本

统一记录 Provider、模型、输入 Token、输出 Token、请求耗时、错误类型和请求时间。价格表内置 OpenAI、Anthropic、DeepSeek 常用模型，并允许配置覆盖。每次调用计算输入/输出成本，usage 与成本记录持久化到 SQLite，可按 Task、Workspace、Provider 和时间范围查询。

价格未知时仍保存 usage，成本标记为 `unknown`，不阻断 Council 执行。REST 提供成本查询接口，前端显示任务成本摘要。

### Grafana 与告警

仓库提供可导入 Dashboard JSON 和 Prometheus 告警规则，包含 Council 成功率/阶段耗时、Provider 错误率与成本趋势、Runner 成功率、权限拒绝和 TLS 证书状态。告警覆盖高错误率、持续重试、验证失败和证书临近过期。

### 测试与 CI

验证包括 Prometheus exposition 单元测试、成本计算和价格覆盖测试、成本 SQLite 重启恢复、Dashboard/告警 JSON 结构校验，以及 CI 中的 Go test、go vet、前端测试和配置校验。

## 错误处理与兼容性

- RBAC 数据库不可用时拒绝需要认证的请求，不降级为匿名访问。
- TLS 新证书无效时继续使用上一张有效证书，并提供可观测错误信号。
- Provider 成本价格缺失不影响任务执行，只影响成本字段。
- 所有敏感值（密码、Token、API Key、私钥）不得写入日志、制品、SQLite 明文或浏览器 URL/localStorage。
- 现有 REST、gRPC、静态 Token 和 Caddy 部署保持向后兼容，新增能力通过配置显式启用。

## 非目标

- 不实现 OAuth/OIDC、LDAP 或云端 IAM 集成。
- 不把 SQLite 替换为分布式数据库。
- 不要求在 Windows 上运行完整 Chromium E2E；浏览器 E2E 继续由 CI/Linux 验证。
