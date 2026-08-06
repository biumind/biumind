// BiuClient (miniapp) —— uni.connectSocket 包装的 brain Agent Plane WS 客户端。
//
// **跟 realtime_hub.ts 对比**：
//   - realtime_hub 走老 AG-UI Realtime（chunked SSE + polling）
//   - BiuClient 走新 SDK Protocol（WebSocket）
//   不冲突 —— 两条独立路径。realtime_hub.ts 顶上"严禁 WebSocket"是
//   针对它自己那条路径（小程序 WS 老问题）；SDK Protocol Dev Plan 已
//   评估过：subprotocol biu-sdk.v1.json + 25s 心跳能克服小程序 6min idle
//   断线问题（详见 docs/BiuMind-Agent-Plane-WebSocket-Transport.md §14）。
//
// **跟 Flutter BiuClient 对应**：API 形态一致：
//   - connect(sessionId, sessionToken) → 立即返回，后台跑 socket 升级
//   - frames callback → 收解过的 SDK Protocol 帧
//   - sendUserText(text) → 简化封装；包成 type=user message
//   - close() → 主动断
//
// **为什么不引 vitest 单测**：miniapp 暂无测试框架；S10-2 一并加。
// 当前依赖 type-check + 实际小程序模拟器手测覆盖。

import { getAccessToken } from '../core/token_manager';

/// brain Agent Plane WS base URL；末尾不带 `/`，class 内部拼路径。
const BASE_URL =
  ((import.meta.env.VITE_BIU_AGENT_PLANE_URL as string) || '').replace(
    /\/$/,
    ''
  ) ||
  ((import.meta.env.VITE_BIU_API_BASE as string) || '');

/// SDK Protocol v1 帧最小类型集 —— 只包 BiuClient 自己用的字段。
/// 完整 schema 在 lib/biu-agent-schemas/（S1-2~S1-5 落地）；那时再切到完整
/// type，这里先用结构化 partial 避免循环依赖。
export interface SDKFrame {
  type: string;
  uuid?: string;
  session_id?: string;
  // 数据平面常见字段
  text?: string;
  subtype?: string;
  // 工具相关
  tool_use_id?: string;
  tool_name?: string;
  // 兜底 —— 让客户端拿原始 JSON 自己渲染
  [k: string]: unknown;
}

/// connect 时的可选参数。
export interface ConnectOpts {
  sessionId: string;
  sessionToken: string;
  /// 重连请求重放从 sinceSeq+1 开始的帧。无续传需求时省略（默认 0=
  /// 实时拉，不重放）。
  sinceSeq?: number;
}

export interface BiuClientOptions {
  /// brain WS base URL 覆盖；不传走环境变量默认。
  baseUrl?: string;
  /// 连续重连最多次数到达后放弃 frames 回调结束。默认 8 次。
  reconnectMaxAttempts?: number;
  /// 收到 1008 / 4xx 时调，期望返回新 session_token。null 时直接断。
  onTokenExpired?: () => Promise<string | null>;
  /// 测试注入 —— 替换 uni.connectSocket。生产留 undefined。
  connector?: (uri: string, protocols: string[]) => BiuTransport;
}

/// 下游帧回调。每条帧已经 JSON.parse 过。
export type FrameHandler = (frame: SDKFrame) => void;

/// uni.connectSocket 抽象 —— BiuClient 不直接 reach uni global，让测试
/// 注入 fake transport 不需要 stub 整个 uni 对象。
export interface BiuTransport {
  /// 注册帧回调（String / ArrayBuffer 都可能；BiuClient 内部 JSON.parse
  /// String 帧，二进制帧 drop）。
  onMessage(cb: (data: string | ArrayBuffer) => void): void;
  /// 远端断（包括小程序 6min idle 断线）。
  onClose(cb: (reason: string) => void): void;
  /// 升级 / 网络错。
  onError(cb: (err: unknown) => void): void;
  /// 发一帧 String（JSON.stringify 过）。返回 Promise resolve 即送出。
  send(data: string): Promise<void>;
  /// 主动关。
  close(): Promise<void>;
}

/// 默认 connector —— 包 uni.connectSocket。 subprotocol 用 'biu-sdk.v1.json'
/// 跟 brain ingress 协议谈判。
function defaultConnector(uri: string, protocols: string[]): BiuTransport {
  // uni 类型由 @dcloudio/uni-app 提供；avoid TS error 用 any 兜底。
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const u = (globalThis as any).uni;
  if (!u || typeof u.connectSocket !== 'function') {
    throw new Error('BiuClient: uni.connectSocket unavailable');
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const task: any = u.connectSocket({
    url: uri,
    protocols,
    // 小程序连接超时设短；25s 心跳保活由 BiuClient 自己处理
    timeout: 30000,
  });
  return {
    onMessage(cb) {
      task.onMessage((res: { data: string | ArrayBuffer }) => cb(res.data));
    },
    onClose(cb) {
      task.onClose((res: { reason: string }) => cb(res.reason || ''));
    },
    onError(cb) {
      task.onError((err: unknown) => cb(err));
    },
    send(data) {
      return new Promise((resolve, reject) => {
        task.send({
          data,
          success: () => resolve(),
          fail: (err: unknown) => reject(err),
        });
      });
    },
    close() {
      return new Promise((resolve) => {
        task.close({
          code: 1000,
          success: () => resolve(),
          fail: () => resolve(), // 忽略关闭失败 —— GC 即可
        });
      });
    },
  };
}

/// BiuClient —— 单条 brain Agent Plane WS session。
export class BiuClient {
  private readonly opts: Required<
    Omit<BiuClientOptions, 'baseUrl' | 'onTokenExpired' | 'connector'>
  > & {
    baseUrl: string;
    onTokenExpired?: () => Promise<string | null>;
    connector: (uri: string, protocols: string[]) => BiuTransport;
  };

  private readonly handlers = new Set<FrameHandler>();
  private transport: BiuTransport | null = null;
  private sessionId: string | null = null;
  private sessionToken: string | null = null;
  private closedByUser = false;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;

  constructor(opts: BiuClientOptions = {}) {
    this.opts = {
      baseUrl: opts.baseUrl?.replace(/\/$/, '') || BASE_URL,
      reconnectMaxAttempts: opts.reconnectMaxAttempts ?? 8,
      onTokenExpired: opts.onTokenExpired,
      connector: opts.connector ?? defaultConnector,
    };
  }

  /// 注册帧回调。返回 unsubscribe。多 handler 支持 broadcast 模式。
  onFrame(cb: FrameHandler): () => void {
    this.handlers.add(cb);
    return () => this.handlers.delete(cb);
  }

  /// 是否当前在线（已升级 + 没主动关）。
  get isConnected(): boolean {
    return this.transport !== null && !this.closedByUser;
  }

  /// 跟一条 session 建 WS 连接。返回前不等 ready —— 内部异步。
  async connect(opts: ConnectOpts): Promise<void> {
    this.sessionId = opts.sessionId;
    this.sessionToken = opts.sessionToken;
    this.closedByUser = false;
    this.reconnectAttempt = 0;
    await this.doConnect(opts.sinceSeq);
  }

  /// 续 token 后让 client 用新 token 重连（如果当前断了）。
  async refreshToken(newToken: string): Promise<void> {
    this.sessionToken = newToken;
    if (!this.transport && !this.closedByUser) {
      await this.doConnect();
    }
  }

  /// 发一帧给 brain。未连或已关 → 抛错。
  async send(frame: SDKFrame): Promise<void> {
    const t = this.transport;
    if (!t) {
      throw new Error('BiuClient.send: not connected');
    }
    await t.send(JSON.stringify(frame));
  }

  /// 简化封装：发一条 user message（type=user，content 走 Anthropic
  /// Messages API content block 数组）。uuid 由调用方生成。
  async sendUserText(text: string, userMessageUuid: string): Promise<void> {
    await this.send({
      type: 'user',
      uuid: userMessageUuid,
      session_id: this.sessionId ?? '',
      message: {
        role: 'user',
        content: [{ type: 'text', text }],
      },
    });
  }

  /// 主动关 —— frames 不再收到新帧；不重连。
  async close(): Promise<void> {
    this.closedByUser = true;
    this.clearTimers();
    const t = this.transport;
    this.transport = null;
    if (t) await t.close();
    this.handlers.clear();
  }

  // ── Internals ──────────────────────────────────────────────

  private async doConnect(sinceSeq?: number): Promise<void> {
    if (this.closedByUser) return;
    const sid = this.sessionId;
    const tok = this.sessionToken;
    if (!sid || !tok) {
      throw new Error('BiuClient: connect() must run before doConnect');
    }
    const baseHttp = this.opts.baseUrl;
    // http(s) → ws(s)
    const baseWs = baseHttp.replace(/^http(s?):/, 'ws$1:');
    let uri = `${baseWs}/v1/agent/sessions/${sid}/stream?session_token=${encodeURIComponent(tok)}`;
    if (sinceSeq && sinceSeq > 0) uri += `&since_seq=${sinceSeq}`;
    try {
      const t = this.opts.connector(uri, ['biu-sdk.v1.json']);
      this.transport = t;
      this.reconnectAttempt = 0;
      t.onMessage((data) => this.onWireFrame(data));
      t.onClose(() => this.onSocketDone());
      t.onError(() => this.onSocketDone());
      this.startHeartbeat();
    } catch (e) {
      console.warn('BiuClient connect failed', e);
      this.scheduleReconnect();
    }
  }

  private onWireFrame(data: string | ArrayBuffer): void {
    if (typeof data !== 'string') {
      // 二进制 drop —— 协议层只发 JSON 字符串
      return;
    }
    let parsed: SDKFrame;
    try {
      parsed = JSON.parse(data) as SDKFrame;
    } catch (e) {
      console.warn('BiuClient bad frame', e, data.slice(0, 80));
      return;
    }
    for (const cb of this.handlers) {
      try {
        cb(parsed);
      } catch (e) {
        console.warn('BiuClient handler threw', e);
      }
    }
  }

  private onSocketDone(): void {
    this.transport = null;
    this.clearHeartbeat();
    if (this.closedByUser) return;
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.closedByUser) return;
    this.reconnectAttempt += 1;
    if (this.reconnectAttempt > this.opts.reconnectMaxAttempts) {
      console.warn('BiuClient: reconnect exhausted, giving up');
      this.closedByUser = true;
      this.handlers.clear();
      return;
    }
    const ms = this.backoffMs(this.reconnectAttempt);
    this.reconnectTimer = setTimeout(async () => {
      // 第 3 次开始尝试 token 刷新（前两次假设网络抖动，token 还有效）
      if (this.opts.onTokenExpired && this.reconnectAttempt > 2) {
        try {
          const fresh = await this.opts.onTokenExpired();
          if (fresh) this.sessionToken = fresh;
        } catch (e) {
          console.warn('BiuClient onTokenExpired threw', e);
        }
      }
      await this.doConnect();
    }, ms);
  }

  private backoffMs(attempt: number): number {
    // 1s / 2s / 4s / 8s / 16s / 30s（cap）...
    const ms = 1000 * Math.pow(2, Math.min(attempt - 1, 5));
    return Math.min(Math.max(ms, 1000), 30000);
  }

  /// 心跳 25s —— 小程序 6min idle 断线兜底。SDK Protocol 协议层走
  /// keep_alive lifecycle 帧（可被 brain ingress 静默吞）。
  private startHeartbeat(): void {
    this.clearHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      this.send({ type: 'keep_alive', uuid: '', session_id: this.sessionId ?? '' }).catch(
        (e) => console.warn('BiuClient heartbeat failed', e)
      );
    }, 25_000);
  }

  private clearHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private clearTimers(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.clearHeartbeat();
  }
}

/// connectAuthenticated —— 简化入口；从 token_manager 拿当前 access
/// token（refresh 走 token_manager 内部），然后 connect。
///
/// 上层业务代码用这个，不直接构造 BiuClient —— 跟 realtime_hub 同款。
export async function connectBiuClient(opts: {
  sessionId: string;
  sessionToken: string;
  sinceSeq?: number;
  baseUrl?: string;
}): Promise<BiuClient> {
  // session_token 已经由 brain /v1/agent/sessions 颁发，不用再走 access_token；
  // 这里 import getAccessToken 只为 onTokenExpired 时拿新 access 触发 refresh。
  const client = new BiuClient({
    baseUrl: opts.baseUrl,
    onTokenExpired: async () => {
      // 兜底：期望上层 controller 先调 brain refresh-session-token 拿新
      // session_token，然后 client.refreshToken(newTok)。这里 access_token
      // 不直接当 session_token 用 —— 只是保证 token_manager 是热的。
      try {
        await getAccessToken();
      } catch {
        /* ignore */
      }
      return null;
    },
  });
  await client.connect({
    sessionId: opts.sessionId,
    sessionToken: opts.sessionToken,
    sinceSeq: opts.sinceSeq,
  });
  return client;
}
