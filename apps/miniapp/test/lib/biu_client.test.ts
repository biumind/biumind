// S10-3 BiuClient idle reconnect + heartbeat 单测。
//
// 用 fake BiuTransport 注入到 BiuClient，避免依赖 uni global / 真 WS。
// 时间相关用 vitest fake timers 加速。
//
// 覆盖：
//   - happy path: connect + frames + sendUserText 序列化
//   - 25s 心跳：每 25s 发一帧 keep_alive
//   - 远端 onClose（模拟 6min idle 断）→ 自动重连 + 后续帧仍能收
//   - 主动 close 不重连
//   - reconnect 指数退避 + 上限放弃
//   - bad frame 容错
//   - send 前 connect 抛错

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { BiuClient, type BiuTransport, type SDKFrame } from '../../src/lib/biu_client';

// FakeTransport —— minimum BiuTransport 实现；caller 用 simulate* 推帧 / 关
class FakeTransport implements BiuTransport {
  private msgCb: ((data: string | ArrayBuffer) => void) | null = null;
  private closeCb: ((reason: string) => void) | null = null;
  private errorCb: ((err: unknown) => void) | null = null;
  public sent: string[] = [];
  public closed = false;

  onMessage(cb: (data: string | ArrayBuffer) => void): void {
    this.msgCb = cb;
  }
  onClose(cb: (reason: string) => void): void {
    this.closeCb = cb;
  }
  onError(cb: (err: unknown) => void): void {
    this.errorCb = cb;
  }
  async send(data: string): Promise<void> {
    this.sent.push(data);
  }
  async close(): Promise<void> {
    this.closed = true;
  }

  // 测试控制 API
  push(frame: SDKFrame): void {
    this.msgCb?.(JSON.stringify(frame));
  }
  pushRaw(s: string): void {
    this.msgCb?.(s);
  }
  simulateClose(reason = ''): void {
    this.closeCb?.(reason);
  }
  simulateError(err: unknown): void {
    this.errorCb?.(err);
  }
}

describe('BiuClient', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('parses incoming JSON frames and dispatches to handlers', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    const frames: SDKFrame[] = [];
    c.onFrame((f) => frames.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    t.push({
      type: 'streamlined_text',
      text: 'hello',
      uuid: 'u1',
      session_id: 'sess-1',
    });
    t.push({
      type: 'result',
      subtype: 'success',
      uuid: 'u2',
      session_id: 'sess-1',
    });

    expect(frames.length).toBe(2);
    expect(frames[0].text).toBe('hello');
    expect(frames[1].subtype).toBe('success');
  });

  it('serializes user message via sendUserText (Anthropic content block shape)', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    await c.sendUserText('hi from miniapp', 'um-1');
    expect(t.sent.length).toBe(1);
    const parsed = JSON.parse(t.sent[0]);
    expect(parsed.type).toBe('user');
    expect(parsed.uuid).toBe('um-1');
    expect(parsed.session_id).toBe('sess-1');
    expect(parsed.message.role).toBe('user');
    expect(parsed.message.content[0]).toEqual({ type: 'text', text: 'hi from miniapp' });
  });

  it('emits keep_alive frame every 25s while connected', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    expect(t.sent.length).toBe(0);
    // 24s —— 还没到心跳周期
    await vi.advanceTimersByTimeAsync(24_000);
    expect(t.sent.length).toBe(0);
    // 26s —— 第一次心跳
    await vi.advanceTimersByTimeAsync(2_000);
    expect(t.sent.length).toBe(1);
    expect(JSON.parse(t.sent[0]).type).toBe('keep_alive');
    // 51s —— 第二次心跳
    await vi.advanceTimersByTimeAsync(25_000);
    expect(t.sent.length).toBe(2);
  });

  it('reconnects after remote onClose (6min idle disconnect simulation)', async () => {
    let connectCount = 0;
    const transports: FakeTransport[] = [];
    const c = new BiuClient({
      baseUrl: 'wss://test',
      connector: () => {
        connectCount++;
        const t = new FakeTransport();
        transports.push(t);
        return t;
      },
    });
    const frames: SDKFrame[] = [];
    c.onFrame((f) => frames.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });
    expect(connectCount).toBe(1);

    // 模拟 6min 后小程序 idle 断 —— 远端 onClose
    await vi.advanceTimersByTimeAsync(6 * 60 * 1000);
    transports[0].simulateClose('idle timeout');
    expect(c.isConnected).toBe(false);

    // 等指数退避（1s 第一次）+ doConnect 完成
    await vi.advanceTimersByTimeAsync(1_500);
    expect(connectCount).toBe(2);
    expect(c.isConnected).toBe(true);

    // 重连后下一帧仍能收到
    transports[1].push({
      type: 'streamlined_text',
      text: 'after-reconnect',
      uuid: 'u',
      session_id: 'sess-1',
    });
    expect(frames.length).toBe(1);
    expect(frames[0].text).toBe('after-reconnect');
  });

  it('explicit close() does not reconnect on transport close', async () => {
    let connectCount = 0;
    const c = new BiuClient({
      baseUrl: 'wss://test',
      connector: () => {
        connectCount++;
        return new FakeTransport();
      },
    });
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });
    expect(connectCount).toBe(1);

    await c.close();
    // 即使时钟跑过 1 分钟也不应该再连
    await vi.advanceTimersByTimeAsync(60_000);
    expect(connectCount).toBe(1);
  });

  it('gives up after reconnectMaxAttempts when connector keeps throwing', async () => {
    // connector 抛错 = 真"无法建立连接"。这种场景才会触发 reconnectAttempt
    // 累计 → max 后放弃。如果连接成功后远端断了，attempt 会 reset；那是
    // 另一种语义（临时断网，应该一直试，由 caller 显式 close）。
    let connectCount = 0;
    const c = new BiuClient({
      baseUrl: 'wss://test',
      reconnectMaxAttempts: 3,
      connector: () => {
        connectCount++;
        throw new Error('connect refused');
      },
    });
    // 第一次也会抛 —— BiuClient catch + scheduleReconnect
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });
    expect(connectCount).toBe(1);

    // 推 60s 让所有 reconnect timer 跑完
    await vi.advanceTimersByTimeAsync(60_000);
    // 1 (初次) + 3 (max attempts) = 4 总尝试，再 attempt=4 触发 give up
    expect(connectCount).toBe(4);
    expect(c.isConnected).toBe(false);

    // 之后不再尝试
    const before = connectCount;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(connectCount).toBe(before);
  });

  it('successful connect resets reconnect counter (reconnect indefinitely on remote close)', async () => {
    // 跟上一个测对照：connector 成功，但远端持续 onClose —— BiuClient
    // 应该一直重连。reconnectMaxAttempts 不在这条路径生效，因为每次
    // doConnect 成功都把 reconnectAttempt 清零。这是"连得上但被断"的
    // 临时网络抖动模型，由 caller 用 close() 显式停。
    let connectCount = 0;
    const c = new BiuClient({
      baseUrl: 'wss://test',
      reconnectMaxAttempts: 3,
      connector: () => {
        connectCount++;
        const t = new FakeTransport();
        // setTimeout(0) 协作 fake timer
        setTimeout(() => t.simulateClose('boom'), 0);
        return t;
      },
    });
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    await vi.advanceTimersByTimeAsync(10_000);
    // 不期望放弃 —— connector 持续被调用
    expect(connectCount).toBeGreaterThan(3);

    await c.close();
  });

  it('bad JSON does not kill the stream; subsequent frame still parses', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    const frames: SDKFrame[] = [];
    c.onFrame((f) => frames.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    t.pushRaw('not-json');
    t.push({ type: 'streamlined_text', text: 'after-bad', uuid: 'u', session_id: 'sess-1' });
    expect(frames.length).toBe(1);
    expect(frames[0].text).toBe('after-bad');
  });

  it('binary frames are dropped (protocol layer JSON-only)', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    const frames: SDKFrame[] = [];
    c.onFrame((f) => frames.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    // 直接通过 onMessage callback 喂 ArrayBuffer —— FakeTransport 转发
    (t as any)['msgCb']?.(new ArrayBuffer(8));
    expect(frames.length).toBe(0);
  });

  it('send() before connect throws', async () => {
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => new FakeTransport() });
    await expect(c.sendUserText('hi', 'u1')).rejects.toThrow(/not connected/);
  });

  it('multiple onFrame handlers all receive frames', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    const a: SDKFrame[] = [];
    const b: SDKFrame[] = [];
    c.onFrame((f) => a.push(f));
    c.onFrame((f) => b.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    t.push({ type: 'streamlined_text', text: 'broadcast', uuid: 'u', session_id: 'sess-1' });
    expect(a.length).toBe(1);
    expect(b.length).toBe(1);
    expect(a[0].text).toBe('broadcast');
  });

  it('unsubscribe via onFrame return stops the handler from getting more frames', async () => {
    const t = new FakeTransport();
    const c = new BiuClient({ baseUrl: 'wss://test', connector: () => t });
    const got: SDKFrame[] = [];
    const unsub = c.onFrame((f) => got.push(f));
    await c.connect({ sessionId: 'sess-1', sessionToken: 'tok' });

    t.push({ type: 'streamlined_text', text: 'first', uuid: 'u', session_id: 'sess-1' });
    expect(got.length).toBe(1);

    unsub();
    t.push({ type: 'streamlined_text', text: 'second', uuid: 'u', session_id: 'sess-1' });
    expect(got.length).toBe(1);
  });
});
