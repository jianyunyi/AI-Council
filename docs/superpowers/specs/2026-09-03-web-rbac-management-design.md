# Web RBAC 用户管理设计

## 目标

将已有 SQLite RBAC 数据模型升级为完整的同域 Web 管理闭环：用户可以安全登录，管理员能管理用户、角色与权限，业务 API 根据最小权限矩阵授权。桌面端与服务间现有 Bearer Token 保持兼容。

## 认证模型

- Web 控制台与 Council API 同域部署。成功登录后，Council 将短期访问 Token 写入 `HttpOnly`、`Secure`、`SameSite=Strict` Cookie；浏览器 JavaScript 不读取或持久化 Token。
- `/api/v1/auth/login` 接受用户名、密码，创建现有 RBAC AccessToken 记录并设置 Cookie。响应只返回安全的当前身份摘要。
- `/api/v1/auth/logout` 撤销当前 Cookie 或 Bearer Token 对应的 AccessToken，并清除 Cookie。
- `/api/v1/auth/me` 返回主体、角色、权限和会话过期时间，不返回 Token、密码哈希或任何密钥。
- API 身份解析按顺序接受 Bearer Token（桌面端、CLI、自动化）或同域 Cookie（Web）。静态 Token 模式和当前桌面运行时不受影响。

## 授权模型

- 全局认证中间件解析身份并写入请求上下文；路由级中间件验证权限而非单一全站角色。
- 权限矩阵：
  - `workspace:read`、`workspace:write`
  - `task:read`、`task:write`
  - `task:approve`、`task:execute`
  - `admin:users`、`admin:roles`、`admin:permissions`
- `admin:*` 是管理员通配权限。健康检查与 Prometheus 指标不经过业务权限检查；所有任务、工作区和管理员变更均要求认证。
- 禁用用户、已撤销 Token 和已过期 Token 返回 401；已认证但无权限返回 403。

## REST 管理接口

- 认证：`POST /api/v1/auth/login`、`POST /api/v1/auth/logout`、`GET /api/v1/auth/me`。
- 用户：`GET /api/v1/admin/users`、`POST /api/v1/admin/users`、`PATCH /api/v1/admin/users/:id`，支持创建、启用/禁用、重设密码与分配角色。
- 角色：`GET /api/v1/admin/roles`、`POST /api/v1/admin/roles`、`PATCH /api/v1/admin/roles/:id`，支持创建角色与替换权限集合。
- 权限：`GET /api/v1/admin/permissions`。服务端维护标准权限目录；管理接口不返回密码哈希、Token 或其哈希。

## Web 管理台

- 新增“账户”页显示当前用户、角色、权限与退出操作；未认证时显示登录表单。
- 新增“用户管理”页，仅在 `admin:users`、`admin:roles` 或 `admin:*` 时导航可见并允许访问。
- 用户区可新建用户、启停用户、分配角色与触发密码重设；角色区可新建角色并替换权限集合。
- 前端请求使用同域 `credentials: 'same-origin'`。不将会话 Token 写入 URL、localStorage 或 sessionStorage。
- 复用现有深石墨/青绿色视觉系统，保留加载、空态、错误和 401/403 明确提示。

## 安全与测试

- 登录接口实施速率限制，错误响应不区分用户名和密码错误。
- Cookie 生产环境必须为 `Secure`；本地 HTTP 开发通过显式 `AUTH_COOKIE_SECURE=false` 配置降级，默认不降级。
- 覆盖 Cookie 登录/退出/me、Bearer 兼容、401/403、权限矩阵、管理员接口脱敏、禁用与撤销、SQLite 重启恢复。
- 覆盖 Web 登录、会话状态、管理员导航可见性和越权反馈；完整 E2E 继续在 Linux CI 执行。
