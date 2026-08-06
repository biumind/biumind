// core/ai_surface.ts — 统一 AI 调用面 (C9 不变量).
//
// 与 Flutter App 端 lib/services/ai_surface.dart 概念对齐: 业务代码不直接
// 知道走哪个 backend (本地 stub / Hub / Runtime AG-UI), 只 import 这里.
//
// W2 提供两条路径:
//   - sendMessage()        一次性请求 + 短轮询响应 (兜底)
//   - streamMessage()      AG-UI 流, 走 RealtimeHub (W3 接入)
//
// W3+ RealtimeHub 上线后, streamMessage 会走 chunked-SSE 或短轮询
// (按平台分流); sendMessage 仍保留作为最简兜底.

import { post } from '@/data/api/client';

export interface ChatMessage {
  id?: string;
  role: 'user' | 'assistant' | 'system';
  content: string;
  createdAt?: number;
  /** 实际用的 model id (assistant 消息). 客户端发送时已知, 服务端 SSE
   *  事件如果带 model 字段会覆盖. 用于 chat 内 "Powered by" 标签. */
  model?: string;
  /** 客户端计时的总响应时长 (ms), onDone 时填. 显示 "1.2s" 这类. */
  elapsedMs?: number;
  /** 失败消息保留原 user prompt 用于"重试"按钮. 仅 assistant 错误占位
   *  消息会带这个字段; 正常消息不带. */
  failedPrompt?: string;
}

export interface SendMessageReq {
  threadId?: string; // 空 → 后端新建 thread
  content: string;
}

export interface SendMessageResp {
  threadId: string;
  messageId: string;
  reply: ChatMessage;
}

// AiSurface — 业务代码引用的唯一 API.
// W2 默认实现走 BiuMind Hub /v1/chat/messages (短轮询).
// 测试 / dev 可 override 全局 instance.
export interface AiSurface {
  sendMessage(req: SendMessageReq): Promise<SendMessageResp>;
  // streamMessage(req): AsyncIterable<AGUIEvent>  ← W3 加, 接 RealtimeHub
}

class HubAiSurface implements AiSurface {
  async sendMessage(req: SendMessageReq): Promise<SendMessageResp> {
    return post<SendMessageReq, SendMessageResp>('/v1/chat/messages', req);
  }
}

// dev / 离线 / 单测时用. 不联网, 直接回 echo.
class EchoAiSurface implements AiSurface {
  async sendMessage(req: SendMessageReq): Promise<SendMessageResp> {
    await new Promise((r) => setTimeout(r, 300));
    return {
      threadId: req.threadId || 'local-thread',
      messageId: 'msg-' + Date.now(),
      reply: {
        role: 'assistant',
        content: '[本地回声] ' + req.content,
        createdAt: Date.now(),
      },
    };
  }
}

let _instance: AiSurface = new HubAiSurface();

export function aiSurface(): AiSurface {
  return _instance;
}

export function overrideAiSurface(impl: AiSurface): void {
  _instance = impl;
}

// 给测试用的便捷 getter
export const _testing = {
  EchoAiSurface,
  HubAiSurface,
};
