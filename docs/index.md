# BiuMind 文档

BiuMind 是一体化 AI 工作平台：写文档、跑 Agent、写代码、做笔记收进同一个工作台，数据互通，由 AI 连成知识图谱。

支持云端 SaaS（注册即用）与自托管（私有化部署）双轨。

## 从这里开始

- **第一次接触 BiuMind** —— [什么是 BiuMind](getting-started/index.md)
- **马上用起来** —— [快速开始](getting-started/quickstart.md)
- **装客户端 / CLI / 扩展** —— [下载与安装](getting-started/download.md)

## 按场景找

| 我想… | 看哪里 |
|---|---|
| 在终端里用 AI 干活 | [CLI (biu)](cli/getting-started.md) |
| 把 BiuMind 部署到自己服务器 | [自托管部署指南](self-hosting/index.md) |
| 本地起一套开发环境 | [本地开发栈](self-hosting/compose.md) |
| 查某个环境变量怎么配 | [环境变量参考](self-hosting/env-vars.md) |
| 让 AI Agent 读写我的知识库 | [开发者：API 参考](developers/api.md) |
| 写一个 BiuApp / Skill | [开发者](developers/index.md) |

## 六大模块

| 模块 | 一句话 | 指南 |
|---|---|---|
| 知识中枢 | Wiki 文档、知识图谱、全局搜索、外部来源接入、AI 调研与审阅 | [知识中枢与笔记](guide/knowledge.md) |
| 对话 | 模型选择、slash 命令、审批卡、跨会话搜索、存入 Wiki | [对话](guide/chat.md) |
| 编码工作台 | Git、终端、文件树、多任务并行、Skills / Hooks | [编码工作台](guide/code.md) |
| 创作 (AIGC) | 文生图、文生视频、爆款拆解、灵感与画廊 | [创作](guide/creation.md) |
| 云端工位 | 云端会话与记忆，多端接续 | 即将上线 |
| 消息接入 | 飞书 / Telegram / Slack / Discord / 邮件渠道接入 Agent | 即将上线 |
| 应用中心 | 安装内置应用与社区应用，或开发自己的 BiuApp | [应用中心](guide/apps.md) |

另有一层跨模块的扩展能力：**Skills**（可复用、可签名的 Agent 技能，见 [Skills 使用](guide/skills.md) 与 [Skills 开发](developers/skills.md)）。
