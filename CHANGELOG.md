# Changelog

本项目的所有重要变更记录于此。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [0.1.0] - 2026-08-05

首次公开发布 / Initial public release.

一体化 AI 工作平台 SaaS：

- **知识层（Brain）**：Wiki 文档 + Graph 图谱 + 记忆 + 搜索（Postgres + pgvector）。
- **Agent 运行时（Runtime）**：WebSocket SDK Protocol 双向流，28 message variant，本地 / 云端 CLI backend（Claude Code / Codex）。
- **模型网关（model-relay）**：单一 egress，BYOK + 计费单一 SoT（`model_relay.pricing`）。
- **客户端**：Flutter 多端（macOS / Windows / Linux / iOS / Android / Web）。
- **App Center / AIGC / Channels**：应用中心、AIGC 产物、多渠道。
- **CLI（biu）**：与服务端共享内核（`biumindkit`）。
- **自托管 + 云端 SaaS 双发**，本地 `docker-compose` 全栈可起。

[0.1.0]: https://github.com/biumind/biumind/releases/tag/v0.1.0
