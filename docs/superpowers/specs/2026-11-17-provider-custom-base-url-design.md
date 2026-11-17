# Provider 自定义 API Base URL 设计

## 目标

允许每个用户 Provider 密钥资料指定 API Base URL，以便使用 OpenRouter 等 OpenAI 兼容服务，同时保持现有密钥加密、用户隔离和默认 OpenAI 行为。

## 方案

- Provider 密钥资料增加可选 `base_url` 字段，并与 Provider、模型及密钥一起持久化。
- 对现有资料，空 `base_url` 保持原 Provider 默认地址；OpenAI 默认为 `https://api.openai.com/v1`。
- 兼容服务使用现有 OpenAI Provider 请求适配器。OpenRouter 配置示例为 `https://openrouter.ai/api/v1`，模型由用户按 OpenRouter 当前可用模型填写。
- URL 必须是绝对 HTTPS URL，主机名有效，不允许用户名/密码、片段或明文 HTTP。规范化去掉尾部斜杠；发送请求时不记录完整 URL 或密钥。
- 自定义 URL 仅对 `openai` 兼容 Provider 开放。Anthropic、DeepSeek 继续使用各自固定 Provider 端点，避免错误路由。
- API 请求和网页 Provider 管理页面支持新增/展示 Base URL。列表返回地址但绝不返回密钥明文；数据库中的 URL 是非秘密配置，密钥仍用现有 AES-GCM 加密。
- 部署环境变量 Provider 配置保持原样；如添加服务器级 OpenAI Base URL，仅作为部署默认值，并允许用户资料显式覆盖。第一版不增加服务器默认配置，避免把目标扩展成全局路由管理。

## 安全与失败处理

- Base URL 输入在 API 边界和服务层均校验；无效地址拒绝保存。
- URL 不允许 HTTP、userinfo 或 fragment，降低 API Key 泄漏和意外路由风险。
- 仅接受 OpenAI 兼容 Provider 的自定义 URL；不允许用户通过其他 Provider 类型更改请求端点。
- URL 路径可以包含服务前缀；配置自定义 Base URL 时，OpenAI 兼容适配器追加 `chat/completions` 路径，未配置时保留 OpenAI 默认 `responses` 路径。
- 迁移需兼容已有 SQLite Provider 资料；空字段代表使用当前默认端点。

## 测试与验收

- 服务测试：允许合法 HTTPS URL；拒绝 HTTP、相对 URL、userinfo、fragment、无主机及超长地址；验证密钥仍按用户隔离和加密。
- Provider 测试：自定义 Base URL 收到请求且 API Key 只发送到该地址；未配置时仍访问默认端点。
- REST 测试：创建、列出、删除资料覆盖 Base URL；列表响应不含密钥明文；无效 URL 返回参数错误。
- Web 测试：Provider 表单可输入 URL；空值保持默认；保存后列表展示地址并保持 Key 掩码。
- E2E 冒烟：在隔离 Linux Compose 环境中配置 OpenRouter URL 和新的低额度测试 Key，选择当前有效免费模型，完成一条 Council 协商并核对请求、错误、重试和成本指标。不得将 Key 写入仓库、镜像或日志。

## 不在本次范围

- 自定义认证头、代理配置、任意 HTTP URL、跨 Provider 协议适配。
- 自动发现或长期维护 OpenRouter 模型列表。
- Windows 桌面安装生命周期测试和生产服务器正式切换。

