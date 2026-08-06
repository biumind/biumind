# SDK Protocol v1 — TypeScript (miniapp)

## 当前状态（S1-5）

`index.ts` 是占位骨架 —— 导出 `Mode` 常量 + 类型 alias。

实际类型定义留给 **S10 小程序 BiuClient 实现**：

1. 把 SDK Protocol 的 Zod schema（`coreSchemas.ts` + `controlSchemas.ts`）vendor 到 `apps/miniapp/src/lib/biu-agent-schemas/`（目前已建占位目录）
2. 在本目录补 BiuMind 扩展的 Zod schema：`mode` / `environment_id` / `thread_id` / lifecycle 6 帧
3. 写 `parse(json)` 入口函数，按 `type`/`subtype` peek 后用对应 Zod schema 解析

## 为什么不在 S1-5 完成 TS

- 小程序 BiuClient 还没设计（S10 才落地）
- TS 端**复用现成 Zod 类型**比手写更省事，但现在写也用不上
- 占位骨架 + 文档说明就够，避免提前写一堆死代码

## 未来 import 例子（S10 实现后）

```ts
import { StdinMessage, parseFrame } from '@/lib/sdkproto/v1';

const frame = parseFrame(rawJson);
if (frame.type === 'control_request' && frame.request.subtype === 'can_use_tool') {
  // 处理权限询问
}
```
