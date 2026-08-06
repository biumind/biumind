// data/api/chat.ts — chat 页面 API thin wrapper.
//
// 后端 brain: /v1/threads + /v1/threads/{id}/messages + /v1/threads/{id}/send.
//
// /send 是 SSE 流式 (text/event-stream), 事件: user_message /
// assistant_message / delta / stop / done / error. 客户端用 uni.request
// enableChunked 接收, parseSSEFrame 解 frame, 触发对应 handler.

import { get, post, patch, del } from './client';
import type { ChatMessage, SendMessageResp } from '@/core/ai_surface';
import { getAccessToken } from '@/core/token_manager';

export type { ChatMessage, SendMessageResp };

const BASE_URL =
  (import.meta.env.VITE_BIU_API_BASE as string) || '';

export interface Thread {
  id: string;
  title: string;
  updated_at: string;
  message_count: number;
}

interface ListThreadsResp {
  threads?: Thread[];
}
interface CreateThreadResp {
  id: string;
  title?: string;
}
interface ListMessagesResp {
  messages?: ChatMessage[];
}

export async function listThreads(): Promise<Thread[]> {
  const r = await get<ListThreadsResp>('/v1/threads?limit=50');
  return r.threads || [];
}

export async function listMessages(threadId: string): Promise<ChatMessage[]> {
  const r = await get<ListMessagesResp>(
    '/v1/threads/' + encodeURIComponent(threadId) + '/messages?limit=200',
  );
  return r.messages || [];
}

// 默认模型 — 与 BiuMind 总盘默认一致 (Anthropic Claude Sonnet 4.6).
// 后端 hub 必须配 BIUMIND_ANTHROPIC_KEY 才会真生效; 否则换成你环境里
// 真有的 key 对应的 model 名 (e.g. 'gpt-4o-mini' 走 OpenAI). 后续 W5
// me 页可加"模型选择"让用户改, 现在硬编码默认值就够把流跑通.
export const DEFAULT_MODEL = 'claude-sonnet-4-6';

// ── Thread CRUD ──────────────────────────────────────────────
//
// 与 Flutter 端 chat_client.dart 同 contract:
//   PATCH /v1/threads/:id  { title?, model?, ... } → 返回更新后的 thread
//   DELETE /v1/threads/:id → 软删 (后端实现细节)
//
// 小程序当前只用 title patch — 重命名场景. 后续模型切换 / pinned 同步
// 也可以走 patchThread.

export interface PatchThreadInput {
  title?: string;
  model?: string;
  pinned?: boolean;
}

export async function patchThread(
  id: string,
  input: PatchThreadInput,
): Promise<Thread> {
  return patch<PatchThreadInput, Thread>(
    '/v1/threads/' + encodeURIComponent(id),
    input,
  );
}

export async function deleteThread(id: string): Promise<void> {
  await del<{ ok?: boolean }>('/v1/threads/' + encodeURIComponent(id));
}

export async function ensureThread(
  threadId: string | undefined,
  firstContent: string,
  model: string = DEFAULT_MODEL,
): Promise<string> {
  if (threadId) return threadId;
  const t = await post<
    { title: string; model: string },
    CreateThreadResp
  >('/v1/threads', {
    title: firstContent.slice(0, 40),
    model,
  });
  return t.id;
}

// ── 流式发送 ────────────────────────────────────────────────────

export interface StreamHandlers {
  /** 服务端确认收到 user 消息 (含 server-side id) */
  onUserMessage?: (msg: ChatMessage) => void;
  /** 服务端创建 assistant 占位消息 */
  onAssistantStart?: (msg: ChatMessage) => void;
  /** AI 增量内容 (打字机) */
  onDelta?: (text: string) => void;
  /** 单段输出停止 (一次回复可能多段, 例如 tool-use 间隔) */
  onStop?: (reason: { reason?: string }) => void;
  /** 全部完成 */
  onDone?: () => void;
  /** 错误 — 网络 / 服务端业务错 / 解析失败 */
  onError?: (msg: string) => void;
}

export interface StreamHandle {
  /** 取消进行中的流 — 微信侧 abort requestTask */
  cancel(): void;
}

/**
 * streamMessage — 给已存在的 thread 发一条消息, SSE 拿 AI 流式回复.
 *
 * 调用前确保 threadId 已通过 ensureThread() 拿到.
 * 失败时 onError 触发, 不抛 promise rejection (接口设计为 fire-and-forget).
 *
 * model 可选 — 不传时由 thread 创建时定的模型 fallback; thread 也没定就
 * 报 no_model. ensureThread 会默认带 DEFAULT_MODEL, 所以正常路径不需传.
 */
export function streamMessage(
  threadId: string,
  content: string,
  handlers: StreamHandlers,
  model?: string,
): StreamHandle {
  const token = getAccessToken();
  const header: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  };
  if (token) header['Authorization'] = 'Bearer ' + token;

  let buffer = '';
  let closed = false;
  // stateful UTF-8 decoder — chunked 边界可能切断中文 (3 字节/字).
  // {stream: true} 让 decoder 内部保留尾部不完整字节, 拼到下次 chunk 一起 decode.
  const decoder = createStreamingDecoder();

  const task = uni.request({
    url: BASE_URL + '/v1/threads/' + encodeURIComponent(threadId) + '/send',
    method: 'POST',
    header,
    data: model ? { content, model } : { content },
    enableChunked: true,
    timeout: 600000, // LLM 长生成
    success: () => {
      if (!closed) {
        // 服务端 close 视为 done
        handlers.onDone?.();
        closed = true;
      }
    },
    fail: (e) => {
      if (!closed) {
        handlers.onError?.(e.errMsg || 'network failed');
        closed = true;
      }
    },
  } as UniApp.RequestOptions);

  // chunk 回调 — 类型定义里没有 onChunkReceived, 用 any 临时. 微信
  // 基础库 ≥ 2.20.1 上方法可用.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const t = task as any;
  if (typeof t.onChunkReceived === 'function') {
    t.onChunkReceived((res: { data: ArrayBuffer }) => {
      if (closed) return;
      const text = decoder.decode(res.data);
      buffer += text;
      let idx: number;
      while ((idx = buffer.indexOf('\n\n')) !== -1) {
        const frame = buffer.slice(0, idx);
        buffer = buffer.slice(idx + 2);
        const parsed = parseSSEFrame(frame);
        if (!parsed) continue;
        try {
          dispatchEvent(parsed.event, parsed.data, handlers);
        } catch (e) {
          console.error('[chat.stream] dispatch error:', e);
        }
      }
    });
  } else {
    // 不支持 chunked — 兜底报错让上层降级到非流式占位
    handlers.onError?.('chunked-SSE not supported on this platform');
    closed = true;
  }

  return {
    cancel: () => {
      closed = true;
      try {
        task.abort();
      } catch {
        /* noop */
      }
    },
  };
}

// ── 内部 helpers ───────────────────────────────────────────────

function dispatchEvent(
  event: string,
  data: string,
  h: StreamHandlers,
): void {
  let payload: unknown;
  try {
    payload = JSON.parse(data);
  } catch {
    payload = data;
  }
  switch (event) {
    case 'user_message': {
      const m = payload as ChatMessage;
      h.onUserMessage?.(m);
      return;
    }
    case 'assistant_message': {
      const m = payload as ChatMessage;
      h.onAssistantStart?.(m);
      return;
    }
    case 'delta': {
      const p = payload as { text?: string };
      if (p?.text) h.onDelta?.(p.text);
      return;
    }
    case 'stop': {
      h.onStop?.((payload as { reason?: string }) || {});
      return;
    }
    case 'done': {
      h.onDone?.();
      return;
    }
    case 'error': {
      const p = payload as { message?: string };
      h.onError?.(p?.message || 'stream error');
      return;
    }
  }
}

// createStreamingDecoder — 每个 streamMessage 调用建一个 stateful 解码器.
// TextDecoder 的 {stream: true} 会保留 chunk 尾的不完整 UTF-8 字节序列,
// 等下次 decode 拼起来一起处理. 不带这个标志, 跨 chunk 的多字节字符就乱码.
//
// 平台兜底: 如果 globalThis 没 TextDecoder (老内核), 退化为 ASCII-only
// 拼接 — 中文会全乱; 但 2026 年的微信小程序 / H5 都已支持, 走不到 fallback.
function createStreamingDecoder(): { decode(buf: ArrayBuffer): string } {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g = globalThis as any;
  if (typeof g.TextDecoder === 'function') {
    const td = new g.TextDecoder('utf-8');
    return {
      decode: (buf: ArrayBuffer) => td.decode(buf, { stream: true }) as string,
    };
  }
  return {
    decode: (buf: ArrayBuffer) => {
      let s = '';
      const arr = new Uint8Array(buf);
      for (let i = 0; i < arr.length; i++) s += String.fromCharCode(arr[i]);
      return s;
    },
  };
}

interface ParsedFrame {
  event: string;
  data: string;
}

function parseSSEFrame(frame: string): ParsedFrame | null {
  let event = 'message';
  const dataLines: string[] = [];
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
  }
  if (dataLines.length === 0) return null;
  return { event, data: dataLines.join('\n') };
}

// ── 兼容旧 sendMessage —— 走流式后等 done 一次性返 ──────────────

// 旧 AiSurface 接口仍然存在 (chat.ts 早期), 保留兼容. 推荐直接用
// streamMessage 拿打字机效果.
export async function sendMessage(
  threadId: string | undefined,
  content: string,
): Promise<SendMessageResp> {
  const tid = await ensureThread(threadId, content);
  return new Promise<SendMessageResp>((resolve, reject) => {
    let acc = '';
    let assistantId = '';
    streamMessage(tid, content, {
      onAssistantStart: (m) => {
        assistantId = m.id || '';
      },
      onDelta: (t) => {
        acc += t;
      },
      onDone: () => {
        resolve({
          threadId: tid,
          messageId: assistantId,
          reply: {
            id: assistantId,
            role: 'assistant',
            content: acc,
            createdAt: Date.now(),
          },
        });
      },
      onError: (msg) => reject(new Error(msg)),
    });
  });
}
