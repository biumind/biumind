# SDK Schema Vendoring

承载 SDK 协议 `entrypoints/sdk/coreSchemas.ts` + `controlSchemas.ts` 的 Zod schema 复制 / 包装，供 miniapp 端复用而无需引入 `@anthropic-ai/claude-code-sdk` 全量依赖（小程序包大小敏感）。

## 当前状态

S1-1：目录占位。实际 schema 内容在 S1-2 / S1-3 / S1-4 / S1-5 落地，对照 `schema/sdk/v1/` 的 JSON Schema。

## 计划

- vendor `coreSchemas.ts` + `controlSchemas.ts`（git submodule 或手动同步）
- BiuMind 扩展字段（`mode` / `environment_id` / `thread_id` / lifecycle）放在同级 `biumind-ext.ts`
- miniapp 业务代码：`import { StdinMessageSchema } from '@/lib/biu-agent-schemas'`

## 同步策略

SDK 升级时手动同步并跑 `pnpm typecheck`，不做自动追踪 —— SDK 协议改动需要三端联动评审。
