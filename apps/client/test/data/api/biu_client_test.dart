// BiuClient 单测 —— 通过 connector 注入 fake BiuTransport，不启真 HTTP server。
// 覆盖：
//
//   - happy path: connect → 收 streamlined_text + result(success)
//   - sendUserText 序列化形状
//   - 主动 close 不触发重连
//   - 远端断（done）不会让 closed-by-user 状态泄漏
//   - broadcast 多 listener
//   - bad frame 容错
//   - send 前 connect 的 StateError

import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/biu_client.dart';
import 'package:biumind/data/api/sdkproto/v1/common.dart';
import 'package:biumind/data/api/sdkproto/v1/data/result.dart';
import 'package:biumind/data/api/sdkproto/v1/data/streamlined.dart';
import 'package:biumind/data/api/sdkproto/v1/data/user.dart';

class FakeTransport implements BiuTransport {
  final _ctrl = StreamController<dynamic>.broadcast();
  final List<String> sent = [];
  bool closed = false;

  /// server 端推一帧给 client（已经 String 化）。
  void push(dynamic frame) => _ctrl.add(frame);

  /// 模拟远端断 —— close stream 触发 client onDone。
  Future<void> closeFromServer() async {
    if (!_ctrl.isClosed) {
      await _ctrl.close();
    }
  }

  @override
  Stream<dynamic> get frames => _ctrl.stream;

  @override
  void send(String data) {
    sent.add(data);
  }

  @override
  Future<void> close() async {
    closed = true;
    if (!_ctrl.isClosed) {
      await _ctrl.close();
    }
  }
}

void main() {
  test('connect + receive streamlined_text + result(success)', () async {
    final fake = FakeTransport();
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => fake,
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    final frames = <Object>[];
    final sub = c.frames.listen(frames.add);

    fake.push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'hello',
      'uuid': 'u1',
      'session_id': 'sess-1',
    }));
    fake.push(jsonEncode({
      'type': 'result',
      'subtype': 'success',
      'duration_ms': 100,
      'duration_api_ms': 50,
      'is_error': false,
      'num_turns': 1,
      'result': '',
      'total_cost_usd': 0,
      'usage': {},
      'modelUsage': {},
      'permission_denials': [],
      'uuid': 'u2',
      'session_id': 'sess-1',
    }));

    await Future.delayed(const Duration(milliseconds: 30));
    await sub.cancel();
    await c.close();

    expect(frames.length, 2);
    expect(frames[0], isA<SDKStreamlinedText>());
    expect((frames[0] as SDKStreamlinedText).text, 'hello');
    expect(frames[1], isA<SDKResultSuccess>());
  });

  test('sendUserText serializes correct user message', () async {
    final fake = FakeTransport();
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => fake,
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    c.sendUserText('hi from flutter', userMessageUuid: 'um-1');

    expect(fake.sent.length, 1);
    final parsed = jsonDecode(fake.sent.first) as Map<String, dynamic>;
    expect(parsed['type'], 'user');
    expect(parsed['uuid'], 'um-1');
    expect(parsed['session_id'], 'sess-1');
    final msg = parsed['message'] as Map<String, dynamic>;
    expect(msg['role'], 'user');
    expect(msg['content'], isA<List>());
    expect((msg['content'] as List).first['text'], 'hi from flutter');

    await c.close();
  });

  test('explicit close does not reconnect', () async {
    var connectCalls = 0;
    FakeTransport? activeFake;
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) {
        connectCalls++;
        activeFake = FakeTransport();
        return activeFake!;
      },
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');
    expect(connectCalls, 1);

    await c.close();
    await activeFake!.closeFromServer();
    await Future.delayed(const Duration(milliseconds: 50));
    expect(connectCalls, 1, reason: 'closed-by-user must not reconnect');
  });

  test('frames stream is broadcast (multiple listeners)', () async {
    final fake = FakeTransport();
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => fake,
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    final a = <Object>[];
    final b = <Object>[];
    c.frames.listen(a.add);
    c.frames.listen(b.add);

    fake.push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'broadcast',
      'uuid': 'u',
      'session_id': 'sess-1',
    }));
    await Future.delayed(const Duration(milliseconds: 20));

    expect(a.length, 1);
    expect(b.length, 1);
    expect((a[0] as SDKStreamlinedText).text, 'broadcast');

    await c.close();
  });

  test("bad frame doesn't kill the stream", () async {
    final fake = FakeTransport();
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => fake,
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    final frames = <Object>[];
    c.frames.listen(frames.add);

    fake.push('not-json'); // 解析失败
    fake.push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'after-bad',
      'uuid': 'u',
      'session_id': 'sess-1',
    }));
    await Future.delayed(const Duration(milliseconds: 20));

    expect(frames.length, 1);
    expect((frames.first as SDKStreamlinedText).text, 'after-bad');

    await c.close();
  });

  test('send() before connect throws StateError', () {
    final c = BiuClient(
      brainBaseUrl: 'ws://x',
      connector: (_) => FakeTransport(),
    );
    expect(
      () => c.sendUserText('hi', userMessageUuid: 'u1'),
      throwsStateError,
    );
  });

  test('connect with sinceSeq adds since_seq query param (S9-1 resume)', () async {
    final captured = <Uri>[];
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (uri) {
        captured.add(uri);
        return FakeTransport();
      },
    );
    await c.connect(
      sessionId: 'sess-1',
      sessionToken: 'tok',
      sinceSeq: 42,
    );

    expect(captured.length, 1);
    expect(captured.first.path, '/v1/agent/sessions/sess-1/stream');
    expect(captured.first.queryParameters['session_token'], 'tok');
    expect(captured.first.queryParameters['since_seq'], '42');

    await c.close();
  });

  test('connect without sinceSeq omits since_seq param (default live tail)', () async {
    Uri? captured;
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (uri) {
        captured = uri;
        return FakeTransport();
      },
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    expect(captured, isNotNull);
    expect(captured!.queryParameters.containsKey('since_seq'), false);

    await c.close();
  });

  test('enqueue while connected sends immediately (S9-3)', () async {
    final fake = FakeTransport();
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => fake,
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');

    // online + outbox empty → enqueue 走 send 路径
    c.enqueue(SDKUserMessage(
      uuid: 'u1',
      sessionId: 'sess-1',
      message: AnthropicMessage(role: 'user', content: const []),
    ));
    expect(fake.sent.length, 1);
    expect(c.outboxPending, 0);

    await c.close();
  });

  test('enqueue while disconnected buffers + flushes on reconnect (S9-3)', () async {
    var connectAttempt = 0;
    final fakes = <FakeTransport>[];
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) {
        connectAttempt++;
        // 第一次连立即关 —— 模拟离线
        if (connectAttempt == 1) {
          final f = FakeTransport();
          fakes.add(f);
          // 立刻关让 _onSocketDone 触发 reconnect
          Future.microtask(() => f.closeFromServer());
          return f;
        }
        // 第二次成功（重连）
        final f = FakeTransport();
        fakes.add(f);
        return f;
      },
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');
    // 等第一个 transport done
    await Future.delayed(const Duration(milliseconds: 30));

    // 离线期间 enqueue 3 条 —— 不抛错，进队列
    for (var i = 0; i < 3; i++) {
      c.enqueue(SDKUserMessage(
        uuid: 'u-$i',
        sessionId: 'sess-1',
        message: AnthropicMessage(role: 'user', content: const []),
      ));
    }
    expect(c.outboxPending, 3, reason: 'all 3 buffered while offline');

    // 等 1s+ backoff + 第二次连成功 + flush
    await Future.delayed(const Duration(milliseconds: 1500));

    // 第二个 fake transport 应该收到 3 条按顺序
    expect(fakes.length >= 2, true,
        reason: 'expected at least 2 transports (initial + reconnect)');
    final second = fakes[1];
    expect(second.sent.length, 3);
    expect(c.outboxPending, 0);
    // 顺序：u-0 → u-1 → u-2
    final ids = second.sent
        .map((s) => (jsonDecode(s) as Map)['uuid'])
        .toList();
    expect(ids, ['u-0', 'u-1', 'u-2']);

    await c.close();
  });

  test('enqueue after close throws StateError (S9-3)', () async {
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      connector: (_) => FakeTransport(),
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');
    await c.close();

    expect(
      () => c.enqueue(SDKUserMessage(
        uuid: 'x',
        sessionId: 's',
        message: AnthropicMessage(role: 'user', content: const []),
      )),
      throwsStateError,
    );
  });

  test('enqueue beyond outboxMaxLen throws (S9-3 防爆内存)', () async {
    final c = BiuClient(
      brainBaseUrl: 'ws://localhost:7003',
      outboxMaxLen: 3,
      connector: (_) {
        // 一连就关 → 一直离线，enqueue 进队列
        final f = FakeTransport();
        Future.microtask(() => f.closeFromServer());
        return f;
      },
    );
    await c.connect(sessionId: 'sess-1', sessionToken: 'tok');
    await Future.delayed(const Duration(milliseconds: 50));

    SDKUserMessage mkUser(int i) => SDKUserMessage(
          uuid: 'u-$i',
          sessionId: 's',
          message: AnthropicMessage(role: 'user', content: const []),
        );

    // 入 3 条 —— OK
    for (var i = 0; i < 3; i++) {
      c.enqueue(mkUser(i));
    }
    expect(c.outboxPending, 3);

    // 第 4 条 → outbox 满 → 抛
    expect(() => c.enqueue(mkUser(4)), throwsStateError);

    await c.close();
  });
}
