# 开发者

BiuMind 的全部集成面：一个账号、一份 JWT，六个入口按场景选。

## 选型速查

| 我想… | 用什么 | 文档 |
|---|---|---|
| 让任意 AI Agent（Claude Code、其他 MCP 客户端）读写我的知识库 | **MCP 工具服务**（首选，零 SDK 依赖） | [API 参考：MCP](api.md) |
| 从自己的服务调知识库 / 记忆 / 模型 / 生成接口 | **REST API** | [API 参考：REST](api.md) |
| 在 Go / Python / Node 里嵌 BiuMind 客户端 | **公共 SDK**（Apache-2.0） | [SDK（Go / Python / Node）](sdks.md) |
| 做一个自己的 AI 工作台 UI，复用 BiuMind 的 Agent 运行时 | **SDK Protocol**（WebSocket 双向流） | [SDK Protocol](sdk-protocol.md) |
| 给应用中心写一个可上架的应用 | **BiuApp** | [BiuApp 开发](biuapp.md) |
| 给 Agent 写可复用、可签名的技能 | **Skills** | [Skills 开发](skills.md) |

## 认证模型

所有接口共用一个 origin、一份凭证：在客户端「设置 → 虚拟 API Key」生成 PAT，以 Bearer JWT 调用。详见 [API 参考](api.md)。

## 许可

`sdks/` 与 `extensions/` 为 Apache-2.0；平台本体（`apps/` `services/` `workers/` `packages/`）为 BiuMind Community License（source-available，禁止作为公开 SaaS 提供给第三方）——你自己的 BiuApp / Skill 代码归你自己。
