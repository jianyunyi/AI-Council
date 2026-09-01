# AI Council 桌面应用设计

日期：2026-09-01  
状态：设计已确认

## 目标

为 AI Council 增加可下载、可安装、可离线管理本地 Workspace 的桌面应用。桌面版与现有 REST/gRPC/Web 部署共存，不重写 Council Engine、Runner、SQLite 或审批状态机。

## 推荐架构

采用 Wails 桌面壳，Go 负责应用生命周期和本地服务管理，React 前端嵌入 WebView：

```text
AI-Council.exe
 ├─ Wails/Go desktop host
 ├─ 嵌入式 React 前端资源
 ├─ Council HTTP 服务（127.0.0.1 随机端口）
 ├─ Workspace Runner gRPC 服务（本地随机端口）
 └─ SQLite 数据库与应用配置目录
```

桌面壳启动时生成随机 Session Token，启动 Council 和 Runner 子进程，等待健康检查通过后把本地 API 地址注入前端。退出时先优雅停止任务，再停止子进程；异常退出后下次启动根据 SQLite 中的状态恢复可恢复任务。

## 前端适配

保留现有 React 组件和 TypeScript API 类型。桌面构建使用静态资源模式，将 `NEXT_PUBLIC_API_BASE` 替换为桌面壳提供的本地地址；SSE 使用同一个 Session Token。若 Next.js SSR 与 WebView 静态导出存在限制，则抽取 `web` 中的页面组件到 Vite/Wails 前端入口，业务 API 契约不变。

桌面 UI 增加：

- Workspace 目录选择器
- Council/Runner 服务状态和端口状态
- API Key 配置入口
- 数据目录和日志目录入口
- 任务恢复提示与安全退出提示

## 本地安全

- Council HTTP 只绑定 `127.0.0.1`，不暴露局域网。
- 每次启动生成不可预测的 Session Token，并通过内存或命令行参数传给子进程，退出后失效。
- API Key 使用 Windows Credential Manager/DPAPI 保存；不写 SQLite、日志、制品、URL 或 localStorage。
- Workspace 路径必须由用户选择并继续经过 Runner pathguard 和人工批准哈希校验。
- 默认关闭自动执行；任何 patch、command 仍需人工批准。
- 发布包使用代码签名，更新包校验签名和 SHA-256。

## 进程与配置

应用配置目录遵循操作系统规范，Windows 默认位于 `%LOCALAPPDATA%\\AI-Council`，包含 SQLite、日志、缓存和版本信息。用户数据目录与安装目录分离，升级不覆盖数据库和 Workspace。

桌面壳负责：

1. 分配本地随机 HTTP/gRPC 端口。
2. 注入 `AUTH_MODE=hybrid` 和随机本地 Session Token。
3. 监控子进程退出并提供可恢复错误提示。
4. 将标准输出和错误输出写入轮转日志，不记录密钥。
5. 在退出时发送 graceful shutdown，超时后强制终止子进程。

## 发布与更新

第一阶段发布 Windows：

- `AI-Council-Setup.exe`：安装向导和开始菜单快捷方式。
- `AI-Council.exe`：便携版，不写入系统目录。

GitHub Actions 使用 GoReleaser/Wails 构建、运行 Go/前端测试、生成 SHA-256 校验文件并上传 GitHub Release。后续增加 macOS `.dmg` 和 Linux AppImage，不改变核心服务协议。

## 观测与诊断

桌面版复用现有 Prometheus 指标，并额外记录应用启动耗时、子进程状态、崩溃恢复次数、更新检查结果。提供“导出诊断包”功能，只包含版本、配置摘要、脱敏日志和指标，不包含 API Key、Workspace 文件或制品内容。

## 测试

- Go 单元测试：端口分配、Session Token 注入、进程生命周期、优雅退出、异常恢复。
- 前端测试：桌面 API base 注入、Workspace 选择、服务状态和恢复提示。
- Windows 安装冒烟测试：安装、启动、创建 Workspace、启动 Council、审批、Runner 执行、退出重启恢复。
- Linux CI：构建 Wails 前端资源、Go test、Vitest；Playwright 浏览器 E2E 继续在 Linux 验证。

## 备选方案与取舍

- Electron：Next.js 兼容最好，但安装包和内存占用大，不符合 Go 本地优先目标。
- 浏览器模式：改造最少，但不是完整桌面体验，且容易被浏览器扩展和端口策略影响。
- Wails：需要静态资源适配，但包体小、Go 集成直接，适合当前项目，因此作为默认方案。

## 非目标

- 不把服务迁移到云端或引入账号云同步。
- 不在桌面版绕过人工审批、Runner 安全策略或 RBAC。
- 不把用户 Workspace 内容打包进安装程序。
