# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

独立开发者与小型研发团队，在本地开发环境中需要比较多个模型的建议、形成可审计的行动计划，并保留最终控制权。

## Product Purpose

AI Council 将 OpenAI、Anthropic、DeepSeek 等模型的独立方案、匿名互审、裁判汇总和红队检查组织为一个流程；用户确认计划后，才在受控工作区执行并验证结果。

## Positioning

这是一个本地优先的多模型协商与受控执行工作台：模型不是直接替用户改动工作区，而是先形成可审计、可批准、可验证的共识与执行计划。

## Operating Context

用户在浏览器或 Windows 桌面壳中配置模型、选择工作区、创建协商任务、审阅制品与计划、明确批准执行，再查看验证结果。工作流状态依次为 Draft、Analyzing、Proposing、Reviewing、Judging、RedTeam、AwaitingApproval、Executing 和 Verifying。

## Capabilities and Constraints

- Web 控制台使用 Next.js，桌面版使用 Wails；后端为 Go 服务、SQLite、Council Engine 和 Workspace Runner。
- 支持 Provider、工作区、任务、审批、制品、账户和 RBAC 用户管理。
- API Key 不写入浏览器存储、SQLite、制品或日志；执行必须由人工消费审批哈希。
- 页面需要同时兼顾品牌展示与高频任务操作；桌面和移动浏览器都应可用。

## Brand Commitments

产品名称为 AI Council。品牌应明确传达“多智能体协作、人工主权、受控执行”，避免把模型能力表现为无约束的自动化。

## Evidence on Hand

真实的产品流程与路由已实现于 `web/app`、`web/components` 和 README；不应虚构客户、指标、案例或模型结论。

## Product Principles

- 先呈现分歧与证据，再收敛为行动。
- 人工批准是流程的一部分，而非事后补救。
- 高风险执行必须可追溯、可验证、可停止。
- 展示层可以鲜明，但操作层必须清晰、快速、可访问。

## Accessibility & Inclusion

保持键盘可操作、足够的文字/背景对比度、可见焦点与非颜色状态提示；遵循用户的减少动态效果偏好，并为动态协商演示提供等价文字说明。
