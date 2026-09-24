# 单机生产 V1 设计

## 目标

把 AI Council 发布为可供公网多用户安全使用的单台 Windows/Linux 服务器版本。系统必须在不绕过人工审批的前提下完成协商、计划、受控执行、验证与审计闭环。

## 部署边界

- 使用 Docker Compose 部署 Caddy、Web、Council API 与 Runner。
- 仅 Caddy 暴露 `80/443`；Council、Runner、SQLite 与制品卷只位于 Compose 内网或宿主机私有路径。
- Caddy 终止 TLS 并续签证书。应用服务不直接对公网监听。
- SQLite 数据库、制品目录和备份目录以命名宿主机卷持久化。SQLite 使用 WAL 模式，并在启动时执行完整性检查。
- 生产配置从 `deploy.env` 读取；禁止把 API Key、OAuth Secret、JWT/会话密钥、SMTP 密码或 Runner 服务令牌写入镜像、Git 或日志。

## 身份与授权

- 支持邮箱密码注册/登录，并以 SMTP 完成邮箱验证和密码重置。
- 支持 GitHub 与 Google OAuth；OAuth 身份与邮箱密码身份归一到本地用户记录。
- 管理员拥有成员、角色、站点策略和全局审计查看权限。
- 成员可管理自己拥有或被显式授予访问权的工作区、Provider API Key、任务和制品。
- 授权检查在 HTTP handler 进入业务服务前执行，并在制品下载、任务读取、审批、执行和 Provider Key 使用处再次执行。
- Provider API Key 加密后存储；所有读取响应只返回脱敏元数据，永不返回明文。

## 受控执行

执行状态机固定为：`council → plan → approval_pending → approved → running → verified|failed|cancelled`。

- 只有拥有任务审批权限的用户可批准计划；提交审批、执行和取消都记录审计事件。
- Runner 仅接受 Council 内网调用，并验证短期服务令牌。
- 每个执行请求使用稳定幂等键；Runner 将结果持久化，容器重启后可返回既有终态而不重复执行。
- 工作区 Git 状态、路径隔离、命令白名单/超时、资源限制和验证结果属于执行前后必经检查。
- 任何 API 调用都不能把 `approved` 之前的任务直接转为 `running`。

## 数据、制品与恢复

- SQLite 使用 WAL、busy timeout、单写入保护和可观测的锁等待指标。
- 制品位于按用户和工作区隔离的本地目录；写入与下载都检查归属、类型、单文件大小和工作区总量配额。
- 每日生成数据库一致性备份和制品清单；备份文件加密并保留可配置的有限代数。
- 部署脚本提供备份、恢复到隔离目录、校验和验证、以及恢复演练命令。

## 可观测性与滥用保护

- 提供 `/healthz`、`/readyz` 与受内部网络/管理员访问控制的 Prometheus `/metrics`。
- 指标覆盖 HTTP 请求、认证失败、限流、协商/审批/执行状态、Runner 延迟与错误、Provider 重试/熔断、成本估算、SQLite 锁等待、制品配额和磁盘空间。
- 所有服务输出 JSON 日志，并携带 request、user、workspace、task 与 run 关联 ID；日志和 Caddy 访问日志要可轮转。
- 公网认证、任务创建、审批和执行入口具备按 IP 与用户维度的限流；登录失败返回不泄露账户存在性的响应。

## CI、发布与验收

- CI 运行 Go 全量测试、前端测试和生产构建、Compose 配置校验、镜像构建、依赖/镜像漏洞扫描，以及真实 Council→Approval→Runner→Verification 最小闭环。
- 生产镜像必须使用不可变版本标签；部署前自动备份 SQLite 与制品清单。
- 更新失败可回滚到上一镜像；数据库迁移仅允许向前兼容并记录版本。
- V1 上线验收包括：公网 HTTPS；邮箱密码、GitHub OAuth、Google OAuth；跨用户越权阻断；API Key 不回显；审批执行闭环；容器重启后的幂等恢复；监控可抓取；备份恢复演练；回滚演练。

## 非目标

- 不引入 Kubernetes、PostgreSQL、S3/MinIO、组织计费或多机 Runner 调度。
- 不承诺服务自身热加载 TLS 证书；由 Caddy 管理证书生命周期。
- 不在 V1 引入无人值守执行或跳过审批的自动化规则。
