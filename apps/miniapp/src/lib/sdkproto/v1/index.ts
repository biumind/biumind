// BiuMind SDK Protocol v1 — TypeScript bindings (placeholder).
//
// 实际类型定义留给后续 schema vendor（apps/miniapp/src/lib/biu-agent-schemas/）。
// BiuMind 扩展。当前小程序业务还没接入 SDK Protocol，等 S10 小程序 BiuClient 实现
// 时一并补全。
//
// 设计意图：
//   import { StdinMessage, SDKMessage, SDKControlRequest } from '@/lib/sdkproto/v1';
// 然后 zod parse / 自定义 dispatcher 处理。
//
// 优先用现成 Zod schema vendoring（不重新写 wire types）；
// BiuMind 扩展（mode / environment_id / lifecycle）才是 miniapp 端要补的少量代码。

export const SDK_PROTOCOL_VERSION = 'v1';

// 占位 type alias —— S10 时具体实现。
export type StdinMessage = unknown;
export type StdoutMessage = unknown;
export type SDKMessage = unknown;
export type SDKControlRequest = unknown;
export type SDKControlResponse = unknown;
export type Lifecycle = unknown;

// BiuMind Mode enum —— S10 之前已可使用的常量。
export const Mode = {
  chat: 'chat',
  agent: 'agent',
  task: 'task',
} as const;

export type Mode = (typeof Mode)[keyof typeof Mode];
