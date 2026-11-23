# AI Council 第一版收尾清单

> 来源：AWS Quick 智能代理对照 docs/superpowers/plans/ 全部计划与代码现状的核查（2026-11-23）。
> 与《AWSQuick 审查报告 — AI-Council》（同日）互为补充：审查报告的 P0/P1 修复不在本清单重复列出。

### A. 已确认：计划写了但代码没落地

| # | 计划 | 代码 | 判断依据 |
|---|---|---|---|
| 1 | single-host-production-v1 Task 6 Step 2：执行记录要有 `pending/running/succeeded/failed/cancelled` 状态，重启时把过期的 running 记录转为 `failed`，错误码 `runner_interrupted` | `internal/runner/idempotency/store.go` | 只有 `"running"` 和 `"complete"` 两种状态，也没有开始/结束时间字段。记录一旦停在 running，`Begin` 就会一直返回 `ErrInProgress`，没有任何恢复逻辑。整个代码库搜不到 `runner_interrupted`。 |
| 2 | 同计划 Task 6 Step 3：Runner 容器要有只读根文件系统、`no-new-privileges`、CPU/内存/pids 限制 | `deploy/docker-compose.yml` 中的 `runner` 服务 | 只配了 command、healthcheck、volumes 和 `app` 网络，没有 `read_only`、`security_opt`、`pids_limit`、`mem_limit`/`cpus`。 |
| 3 | 同计划 Task 6 Step 1：gRPC interceptor 要拒绝“非本地网络地址” | `internal/runner/grpc/auth.go` | 只校验 Bearer Token，没有读取 peer 地址。另外，`expected` 为空时会直接放行。 |
| 4 | 同计划 Task 1 Files：创建 `cmd/aicouncil-admin/main_test.go` | `cmd/aicouncil-admin/` 目录下只有 `main.go` | 文件不存在，计划里列的退出码测试没有对应代码。 |
| 5 | 同计划 Task 5 Step 2：用 token bucket 限流，key 同时包含 `ip:` 和 `user:` | `internal/security/ratelimit/limiter.go` | 实际是固定窗口计数（`bucket{start,count}`）。有用户 ID 时只用 `user:` 作 key，否则用 IP，两者不会同时生效。 |
| 6 | 同计划 Task 7 Step 3：Compose 里加 Grafana dashboard；single-host.md 写着“不要发布 Grafana 端口” | `deploy/docker-compose.yml`、`docs/operations/single-host.md` | Compose 里只有 Prometheus，没有 Grafana 服务。dashboard 只能按 compose-production.md 的说明手动导入。 |
| 7 | 同计划 Task 7 Step 2：部署 manifest 记录到“备份卷” | `deploy/deploy.ps1` | manifest 实际写到仓库目录下的 `.data/deployments/`，不在备份卷里。 |
| 8 | workspace-sharing Task 4：SSE 在每次发送事件前重新检查授权，撤销后断开连接 | `internal/transport/council/sse.go` | 只把现有事件回放一遍、发一次心跳就结束，不是长连接，所以“推送过程中撤销权限就断开”这件事其实不存在。逐事件检查是有的，但计划的意图只做到一半。 |

### B. 已确认：文档与实现不一致

| # | 文档 | 实际 | 判断依据 |
|---|---|---|---|
| 9 | single-host Task 4 写的是 `internal/security/ownership/service.go` | 授权逻辑在 `internal/security/workspaceaccess/service.go` 和 `internal/transport/council/ownership_test.go` | `ownership` 目录不存在。 |
| 10 | single-host Task 2 写的是 `internal/transport/council/registration.go` | 注册相关路由在 `internal/transport/council/rbac_routes.go` 第 54–57 行 | `registration.go` 不存在，只有 `registration_test.go`。 |
| 11 | single-host Task 5 写的是 `internal/observability/logging/http.go` | 日志代码在 `internal/transport/council/logging.go` | `internal/observability/` 下只有 `metrics`。 |
| 12 | single-host Task 7 写的是 `deploy/monitoring/grafana-dashboard.json`；09-01 观测计划写的是 `deploy/grafana/aicouncil-dashboard.json` 和 `deploy/prometheus/alerts.yml` | 实际文件是 `deploy/grafana/dashboard.json` | 路径对不上；`alerts.yml` 不存在。 |
| 13 | workspace-sharing Task 2 要求 `Service` 只解析已注册的工作区 | Compose 里 Council 硬编码了 `--runner runner:9091`，而迁移读取的是 `RUNNER_WORKSPACE_ROOT`（默认 `/workspace`），Runner 的 `--workspace-root /workspace` 也是硬编码 | 两边靠同一个默认值隐式保持一致。改了其中一处，迁移就会误判。 |
| 14 | `build/version.json` 写的是 `"version": "0.1.0"` 和 `AI-Council-Setup-0.1.0.exe` | 仓库已经发到 v0.2.0 之后 | 桌面安装包的版本号没有跟着升。 |
| 15 | README 末尾多了一个独立的 `# Deployment` 一级标题 | — | 与上面的“生产：HTTPS 同域 Compose 部署”一节重复，看起来是合并残留。 |
| 16 | `docs/operations/single-host.md` 的标题是 “Single-host backup and restore” | 实际内容涵盖部署、回滚、工作区和共享 | 标题太窄，和内容不符。 |

### C. 待人工核实（我没能确认）

- OAuth 是否真的实现了 PKCE，Google 是否校验 ID token 的 issuer 和 audience（single-host Task 3）。`oauth/service.go` 我没有逐行审完。
- 备份格式：计划写的是“制品只记 manifest”，代码则要求 `AICOUNCIL_BACKUP_KEY`，看起来是加密归档（`internal/backup/archive.go`）。是计划后来改了，还是实现偏离了计划，需要对照规格确认。
- “未验证邮箱的账户禁止登录”“密码重置后撤销所有会话”是否都覆盖到了（single-host Task 2 Step 4）。
- `release.yml` 构建的镜像没有用 `docker compose` 的 tag，而是直接推 `github.sha`。这和 `deploy.ps1` 在本地构建的标签体系能否配合使用，要看运维的实际流程。
- v0.1 第 4 阶段计划里的“样例仓库”和“运维文档”交付物，我没找到对应文件，需要确认是不是有意删掉的。
- 13 号计划 Task 7 的 `git diff --check` 和全量验证是否在 CI 里跑过。附件里没有 CI 运行记录。

---
