// core/realtime_hub.ts — 单条实时连接, 多 topic 复用 (C1 / I1 / I8).
//
// 单例; 业务代码通过 subscribe(topic, handler) 拿事件流, 不直接连后端.
// 平台分流由 RealtimeTransport 接口实现:
//   - WechatChunkedTransport / QQChunkedTransport — 走 uni.request enableChunked
//   - H5EventSourceTransport                       — 标准 EventSource
//   - PollingTransport                             — 短轮询兜底 (其他平台)
//
// 严禁 WebSocket / connectSocket. CI 静态扫描会 grep 失败.

import { detectPlatform, supportsChunkedSSE, type Platform } from './platform/detect';
import { getAccessToken } from './token_manager';

const BASE_URL = (import.meta.env.VITE_BIU_API_BASE as string) || '';

export interface RealtimeEvent {
  /** 事件 id, 用于 Last-Event-ID 续传 */
  id?: string;
  /** topic 名 — 例 "agui:run:abc" / "wiki:project:xyz" */
  topic: string;
  /** 事件类型 — 协议层 (TEXT_MESSAGE_CONTENT / CUSTOM 等) */
  event: string;
  /** payload — 已 JSON.parse */
  data: unknown;
}

export type EventHandler = (e: RealtimeEvent) => void;

interface RealtimeTransport {
  start(topics: string[], lastEventId: string | null): void;
  stop(): void;
  /** 动态加 / 减 topic — chunked transport 重连即可; 轮询直接更新参数 */
  updateTopics(topics: string[]): void;
}

class RealtimeHub {
  private handlers = new Map<string, Set<EventHandler>>();
  private transport: RealtimeTransport | null = null;
  private platform: Platform = detectPlatform();
  private lastEventId: string | null = null;
  private started = false;

  /** subscribe — 注册 topic 处理函数, 返回 unsubscribe. 同一 topic 多 handler 共存. */
  subscribe(topic: string, handler: EventHandler): () => void {
    let set = this.handlers.get(topic);
    if (!set) {
      set = new Set();
      this.handlers.set(topic, set);
    }
    set.add(handler);
    this.ensureStarted();
    this.transport?.updateTopics(this.activeTopics());
    return () => {
      set!.delete(handler);
      if (set!.size === 0) {
        this.handlers.delete(topic);
      }
      this.transport?.updateTopics(this.activeTopics());
    };
  }

  private activeTopics(): string[] {
    return Array.from(this.handlers.keys());
  }

  private ensureStarted(): void {
    if (this.started) return;
    this.transport = this.makeTransport();
    this.transport.start(this.activeTopics(), this.lastEventId);
    this.started = true;
  }

  /** 仅给 transport 内部调 — 派发到所有订阅 handler. */
  dispatch(e: RealtimeEvent): void {
    if (e.id) this.lastEventId = e.id;
    const set = this.handlers.get(e.topic);
    if (!set) return;
    for (const h of set) {
      try {
        h(e);
      } catch (err) {
        console.error('[RealtimeHub] handler threw:', err);
      }
    }
  }

  private makeTransport(): RealtimeTransport {
    if (this.platform === 'h5') {
      return new H5EventSourceTransport(this);
    }
    if (supportsChunkedSSE(this.platform)) {
      return new MPChunkedTransport(this);
    }
    return new PollingTransport(this);
  }

  /** 测试 / 强制重连入口. */
  reset(): void {
    this.transport?.stop();
    this.transport = null;
    this.started = false;
  }
}

let _instance: RealtimeHub | null = null;
export function realtimeHub(): RealtimeHub {
  if (!_instance) _instance = new RealtimeHub();
  return _instance;
}

// ── transport 实现 ─────────────────────────────────────────

/**
 * MPChunkedTransport — 微信 / QQ 走 uni.request enableChunked + onChunkReceived.
 *
 * 帧格式: 标准 SSE — `id: <id>\nevent: <name>\ndata: <json>\n\n`.
 * 浏览器 EventSource 自带解析, 这里手写: 把每个 chunk 拼到 buffer, 切 `\n\n` 切出 frame.
 */
class MPChunkedTransport implements RealtimeTransport {
  private requestTask: UniApp.RequestTask | null = null;
  private buffer = '';
  private currentTopics: string[] = [];
  private currentLastID: string | null = null;
  private closed = false;
  // stateful UTF-8 decoder — 每次 connect 重建. TextDecoder {stream: true}
  // 保留尾部不完整字节, 下次 decode 拼回, 防止中文跨 chunk 乱码.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  private decoder: any = null;

  constructor(private readonly hub: RealtimeHub) {}

  start(topics: string[], lastEventId: string | null): void {
    this.currentTopics = topics;
    this.currentLastID = lastEventId;
    this.connect();
  }

  stop(): void {
    this.closed = true;
    try {
      this.requestTask?.abort();
    } catch {
      /* noop */
    }
    this.requestTask = null;
  }

  updateTopics(topics: string[]): void {
    if (sameSet(topics, this.currentTopics)) return;
    this.currentTopics = topics;
    // chunked 没有"加 topic" 协议 — 重连一次, lastEventId 续传
    try {
      this.requestTask?.abort();
    } catch {
      /* noop */
    }
    if (this.currentTopics.length > 0 && !this.closed) {
      this.connect();
    }
  }

  private connect(): void {
    if (this.currentTopics.length === 0) return;
    this.buffer = '';
    // 每次连接重建 decoder, 旧的内部 state (上次断开时的尾字节) 不该带过来
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const g = globalThis as any;
    this.decoder =
      typeof g.TextDecoder === 'function' ? new g.TextDecoder('utf-8') : null;
    const token = getAccessToken();
    const header: Record<string, string> = { Accept: 'text/event-stream' };
    if (token) header['Authorization'] = 'Bearer ' + token;
    if (this.currentLastID) header['Last-Event-ID'] = this.currentLastID;
    const url =
      BASE_URL +
      '/v1/realtime/stream?topics=' +
      encodeURIComponent(this.currentTopics.join(','));
    const task = uni.request({
      url,
      method: 'GET',
      header,
      enableChunked: true,
      timeout: 60000,
      // ts: uni-app 类型对 chunked + onChunkReceived 描述不全, 这里宽松调用.
      success: () => {
        if (!this.closed) this.scheduleReconnect();
      },
      fail: () => {
        if (!this.closed) this.scheduleReconnect();
      },
    } as UniApp.RequestOptions);
    this.requestTask = task;
    // chunk 回调 — uni.app 微信侧 onChunkReceived 类型缺失, 用 any 临时
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const t = task as any;
    if (typeof t.onChunkReceived === 'function') {
      t.onChunkReceived((res: { data: ArrayBuffer }) => {
        this.handleChunk(res.data);
      });
    }
  }

  private handleChunk(data: ArrayBuffer): void {
    // 微信 chunked 是 ArrayBuffer; stateful decoder + {stream: true} 保留
    // 不完整 UTF-8 字节序列, 跨 chunk 拼接 — 否则中文 (3 字节/字) 切到
    // chunk 边界就乱码.
    let text: string;
    if (this.decoder) {
      text = this.decoder.decode(data, { stream: true });
    } else {
      // 老内核 fallback: ASCII-only, 中文乱; 2026 微信 / H5 走不到这里
      text = decodeUTF8(data);
    }
    this.buffer += text;
    let idx: number;
    while ((idx = this.buffer.indexOf('\n\n')) !== -1) {
      const frame = this.buffer.slice(0, idx);
      this.buffer = this.buffer.slice(idx + 2);
      const parsed = parseSSEFrame(frame);
      if (parsed) {
        // topic 由后端写在 event 内 data 字段或 event header 上 — 约定:
        // event: <topic>:<event-name>; 否则 fallback "default".
        let topic = 'default';
        let event = parsed.event;
        const colon = parsed.event.indexOf('|');
        if (colon > 0) {
          topic = parsed.event.slice(0, colon);
          event = parsed.event.slice(colon + 1);
        }
        let payload: unknown = parsed.data;
        try {
          payload = JSON.parse(parsed.data);
        } catch {
          /* keep raw string */
        }
        this.hub.dispatch({
          id: parsed.id,
          topic,
          event,
          data: payload,
        });
      }
    }
  }

  private scheduleReconnect(): void {
    setTimeout(() => {
      if (!this.closed) this.connect();
    }, 1500);
  }
}

/**
 * H5EventSourceTransport — 浏览器原生 EventSource.
 */
class H5EventSourceTransport implements RealtimeTransport {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  private es: any = null;
  private currentTopics: string[] = [];

  constructor(private readonly hub: RealtimeHub) {}

  start(topics: string[]): void {
    this.currentTopics = topics;
    this.connect();
  }

  stop(): void {
    this.es?.close();
    this.es = null;
  }

  updateTopics(topics: string[]): void {
    if (sameSet(topics, this.currentTopics)) return;
    this.currentTopics = topics;
    this.es?.close();
    this.connect();
  }

  private connect(): void {
    if (this.currentTopics.length === 0) return;
    const token = getAccessToken();
    const url =
      BASE_URL +
      '/v1/realtime/stream?topics=' +
      encodeURIComponent(this.currentTopics.join(',')) +
      (token ? '&access_token=' + encodeURIComponent(token) : '');
    // EventSource 不支持自定义 Header, 后端要支持 access_token query 参数
    // 作为 fallback (与 chunked 路径不同).
    // #ifdef H5
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const w = window as any;
    if (typeof w.EventSource === 'function') {
      this.es = new w.EventSource(url);
      this.es.onmessage = (m: { data: string; lastEventId?: string }) => {
        let payload: unknown = m.data;
        try {
          payload = JSON.parse(m.data);
        } catch {
          /* keep */
        }
        this.hub.dispatch({
          id: m.lastEventId,
          topic: 'default',
          event: 'message',
          data: payload,
        });
      };
    }
    // #endif
  }
}

/**
 * PollingTransport — 支付宝 / 抖音 / 快手 / 京东 / 飞书 等不支持 chunked
 * 的平台兜底. 5s 间隔 GET ?fallback=poll, 后端把累积事件一次返回再断开.
 */
class PollingTransport implements RealtimeTransport {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private currentTopics: string[] = [];
  private lastID: string | null = null;
  private closed = false;
  private static readonly INTERVAL_MS = 5000;

  constructor(private readonly hub: RealtimeHub) {}

  start(topics: string[], lastEventId: string | null): void {
    this.currentTopics = topics;
    this.lastID = lastEventId;
    this.tick();
  }

  stop(): void {
    this.closed = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }

  updateTopics(topics: string[]): void {
    this.currentTopics = topics;
  }

  private tick(): void {
    if (this.closed || this.currentTopics.length === 0) {
      this.scheduleNext();
      return;
    }
    const token = getAccessToken();
    const header: Record<string, string> = {};
    if (token) header['Authorization'] = 'Bearer ' + token;
    let url =
      BASE_URL +
      '/v1/realtime/stream?fallback=poll&topics=' +
      encodeURIComponent(this.currentTopics.join(','));
    if (this.lastID) url += '&since=' + encodeURIComponent(this.lastID);
    uni.request({
      url,
      method: 'GET',
      header,
      success: (res) => {
        const body = res.data as { events?: RealtimeEvent[] };
        if (body && Array.isArray(body.events)) {
          for (const e of body.events) {
            if (e.id) this.lastID = e.id;
            this.hub.dispatch(e);
          }
        }
      },
      fail: () => {
        /* 静默, 下个 tick 再试 */
      },
      complete: () => this.scheduleNext(),
    });
  }

  private scheduleNext(): void {
    if (this.closed) return;
    this.timer = setTimeout(() => this.tick(), PollingTransport.INTERVAL_MS);
  }
}

// ── helpers ────────────────────────────────────────────────

function sameSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const s = new Set(a);
  for (const x of b) if (!s.has(x)) return false;
  return true;
}

function decodeUTF8(buf: ArrayBuffer): string {
  // 小程序 / H5 都有 TextDecoder; 没有就 fallback 字符串拼接.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const g = globalThis as any;
  if (typeof g.TextDecoder === 'function') {
    return new g.TextDecoder('utf-8').decode(buf);
  }
  // fallback — 仅 ASCII 安全, 中文会乱; 兜底, 大概率不会走到.
  let s = '';
  const arr = new Uint8Array(buf);
  for (let i = 0; i < arr.length; i++) s += String.fromCharCode(arr[i]);
  return s;
}

interface ParsedFrame {
  id?: string;
  event: string;
  data: string;
}

function parseSSEFrame(frame: string): ParsedFrame | null {
  let id: string | undefined;
  let event = 'message';
  const dataLines: string[] = [];
  for (const line of frame.split('\n')) {
    if (line.startsWith('id:')) id = line.slice(3).trim();
    else if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
  }
  if (dataLines.length === 0) return null;
  return { id, event, data: dataLines.join('\n') };
}
