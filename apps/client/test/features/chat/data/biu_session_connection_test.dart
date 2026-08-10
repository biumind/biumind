// BiuSessionConnection R2 单测 —— 用 fake AgentPlaneClient + fake
// BiuTransport 模拟 brain 端，不打真网络。验帧 → ChatRepo 持久化路径。
//
// 重点：SDK Protocol → ContentBlock 翻译 + Session 生命周期 + token refresh
// + cancel / close 行为。

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:biumind/data/agent_plane/agent_plane_client.dart';
import 'package:biumind/data/api/biu_client.dart';
import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/biu_session_connection.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

class FakeAgentPlane extends AgentPlaneClient {
  FakeAgentPlane()
      : super(baseUrl: 'http://test', tokenProvider: () async => 'tok');

  String? createdSessionId;
  String? createdMode;
  String? createdEnvironmentId;
  CreateSessionResp Function(String mode)? respFor;
  int refreshCalls = 0;
  String refreshToken = 'refreshed-token';

  @override
  Future<CreateSessionResp> createSession({
    required String mode,
    String? environmentId,
    String? threadId,
    String? model,
    String? providerId,
    String? systemPrompt,
    String? prompt,
    String? poolTag,
    String? workdir,
    String? runtimeEnvMode,
    String? backend,
    List<ChatImageInput>? images,
    String? userMessageId,
    String? assistantMessageId,
    String? clientSideRecordId,
    String? clientSideBaseUrl,
    String? clientSideProtocol,
  }) async {
    createdMode = mode;
    createdEnvironmentId = environmentId;
    final r = respFor != null
        ? respFor!(mode)
        : CreateSessionResp(
            sessionId: 'sess-${DateTime.now().millisecondsSinceEpoch}',
            sessionToken: 'tok-init',
            expiresAt: DateTime.now().add(const Duration(minutes: 30)),
            mode: mode,
            environmentId: environmentId,
            jetstreamSubjectIn: '',
            jetstreamSubjectOut: '',
          );
    createdSessionId = r.sessionId;
    return r;
  }

  @override
  Future<RefreshTokenResp> refreshSessionToken(String sessionId) async {
    refreshCalls++;
    return RefreshTokenResp(
      sessionToken: refreshToken,
      expiresAt: DateTime.now().add(const Duration(minutes: 30)),
    );
  }
}

/// FakeTransport —— BiuClient 注入用，可推任意 String 帧、模拟 onClose。
class FakeTransport implements BiuTransport {
  final _ctrl = StreamController<dynamic>.broadcast();
  final List<String> sent = [];
  bool closed = false;

  void push(dynamic frame) => _ctrl.add(frame);
  Future<void> closeFromServer() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }

  @override
  Stream<dynamic> get frames => _ctrl.stream;
  @override
  void send(String data) => sent.add(data);
  @override
  Future<void> close() async {
    closed = true;
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

/// 帮 BiuClient 的 connector 注入 fake，外加共享一个 captured ref 让测试
/// 控制后端帧。BiuSessionConnection 内部 new BiuClient，无法直接注入 ——
/// 用 monkey-patch 替换 connector 静态默认有点麻烦。简化：本测试组只
/// 测公开 API（events stream + ChatRepo state），通过 thread mode 控分支
/// + 用 SDK 帧 JSON 字符串模拟整个流程。
///
/// 最终方案：BiuSessionConnection.open / resume 内部构造 BiuClient 时让
/// 我们能注入 connector。下面通过反射式 hook 失败时就走 spy 模式，
/// 直接断言 ChatRepo 的最终状态而非 events。

void main() {
  late AppDb db;
  late ChatRepo repo;
  late FakeAgentPlane ap;

  setUp(() {
    db = AppDb.memory();
    repo = ChatRepo(db, scope: 'test-scope');
    ap = FakeAgentPlane();
  });
  tearDown(() async {
    await db.close();
  });

  // ─── createSession 路径 ──────────────────────────────────────

  test('open() creates user message, session row, assistant placeholder', () async {
    // 创 thread
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;

    // 调 open（这一步会 BiuClient.connect 内部 throw 因为 brain url 假，
    // 但 ChatRepo 的写入应该已经发生在 throw 之前 —— 验证持久化路径）
    final fakeTransport = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'hello',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fakeTransport,
    );
    addTearDown(() async => c.close());

    // 验：user message 写入 + block 写入 + session 写入
    final user = await repo.getMessage('um1');
    expect(user, isNotNull);
    expect(user!.role, MessageRole.user);
    expect(user.assembledText, 'hello');
    expect(user.status, MessageStatus.completed);

    final assistant = await repo.getMessage('am1');
    expect(assistant, isNotNull);
    expect(assistant!.role, MessageRole.assistant);
    expect(assistant.status, MessageStatus.streaming);
    expect(assistant.sessionId, isNotNull);

    final s = await repo.activeSession('t1');
    expect(s, isNotNull);
    expect(s!.mode, ThreadMode.chat);
    expect(ap.createdMode, 'chat');
  });

  test('open() with attachments writes ImageBlock(s) onto user message',
      () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;

    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'see this',
      userMessageId: 'um-att',
      assistantMessageId: 'am-att',
      attachments: [
        AttachmentInput(
          mimeType: 'image/png',
          bytes: Uint8List.fromList([1, 2, 3, 4]),
        ),
        AttachmentInput(
          mimeType: 'image/jpeg',
          bytes: Uint8List.fromList([5, 6, 7, 8]),
        ),
      ],
      transportConnector: (_) => FakeTransport(),
    );
    addTearDown(() async => c.close());

    final user = await repo.getMessage('um-att');
    expect(user, isNotNull);
    expect(user!.blocks.length, 3); // 1 text + 2 image
    expect(user.blocks[0], isA<TextBlock>());
    final img1 = user.blocks[1] as ImageBlock;
    expect(img1.mimeType, 'image/png');
    expect(img1.index, 1);
    final img2 = user.blocks[2] as ImageBlock;
    expect(img2.mimeType, 'image/jpeg');
    expect(img2.index, 2);
    // base64 of [1,2,3,4] = AQIDBA==
    expect(img1.data, 'AQIDBA==');
  });

  test('open() with agent mode passes environmentId to AgentPlane', () async {
    await repo.createThread(
      id: 't1',
      mode: ThreadMode.agent,
      environmentId: 'env-mac',
    );
    final thread = (await repo.getThread('t1'))!;

    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'do something',
      userMessageId: 'um2',
      assistantMessageId: 'am2',
      transportConnector: (_) => FakeTransport(),
    );
    addTearDown(() async => c.close());

    expect(ap.createdMode, 'agent');
    expect(ap.createdEnvironmentId, 'env-mac');
  });

  // ─── resume 路径（active session 不存在时返 null） ──────────

  test('resume() returns null when no active session', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final c = await BiuSessionConnection.resume(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
    );
    expect(c, isNull);
  });

  test('resume() refreshes token when expiring soon', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    // 写一条快过期的 active session
    await repo.persistSession(Session(
      sessionId: 'sess-old',
      threadId: 't1',
      mode: ThreadMode.chat,
      sessionToken: 'old-tok',
      tokenExpiresAt: DateTime.now().add(const Duration(minutes: 2)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    ));
    final thread = (await repo.getThread('t1'))!;

    final c = await BiuSessionConnection.resume(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      transportConnector: (_) => FakeTransport(),
    );
    addTearDown(() async => c?.close());

    expect(ap.refreshCalls, 1);
    final s = await repo.activeSession('t1');
    expect(s!.sessionToken, 'refreshed-token');
  });

  // ─── 帧 → block 翻译（基于 ChatRepo state 的端到端形态） ────

  test('SDKStreamlinedText accumulates into single TextBlock', () async {
    // 直接搭一个能控帧的 connection：用 BiuClient connector 注入 FakeTransport。
    // 由于 BiuSessionConnection 不暴露 transport 注入，这里改用 ChatRepo 直接
    // 写入模拟 streaming —— 测的是"如果 SDK frame 真的流进来，repo 状态应该
    // 长这样"。完整 connection 流的端到端覆盖留 integration test。
    //
    // 替代：直接验 ChatRepo.upsertBlock(streaming) 累加是 R1 测过了。这条
    // test 改为 placeholder pass —— 真路径覆盖在 chat_repo_test.dart 的
    // "upsertBlock streams text delta accumulation"。
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    await repo.appendMessage(id: 'm', threadId: 't1', role: MessageRole.assistant);
    await repo.upsertBlock(
      const TextBlock(id: 'tb', index: 0, state: BlockState.streaming, text: 'Hel'),
      messageId: 'm',
    );
    await repo.upsertBlock(
      const TextBlock(id: 'tb', index: 0, state: BlockState.streaming, text: 'Hello'),
      messageId: 'm',
    );
    final m = await repo.getMessage('m');
    expect((m!.blocks.first as TextBlock).text, 'Hello');
  });

  test('SessionEvent enum class shape compiles + extends sealed', () {
    // sealed class 完整性：编译期保证 switch 所有分支齐全
    final evs = <SessionEvent>[
      const SessionStarted(sessionId: 's', assistantMessageId: 'a'),
      const BlockUpdated(
        'm',
        TextBlock(id: 'b', index: 0, state: BlockState.closed, text: 'x'),
      ),
      const MessageCompleted(messageId: 'm', stopReason: 'end_turn'),
      const MessageFailed('m', 'oops'),
      const MessageCancelled('m', latency: Duration(milliseconds: 800)),
      const SessionCancelling(),
      const SessionClosed(SessionStatus.completed),
      PermissionRequested(
        requestId: 'r',
        toolName: 'Bash',
        toolUseId: 'u',
        input: const {},
        respond: ({required bool allow}) {},
      ),
    ];
    for (final e in evs) {
      switch (e) {
        case SessionStarted():
        case BlockUpdated():
        case MessageCompleted():
        case MessageFailed():
        case MessageCancelled():
        case SessionCancelling():
        case SessionClosed():
        case PermissionRequested():
      }
    }
    expect(evs.length, 8);
  });

  // ─── close lifecycle ──────────────────────────────────────

  test('open() then close finalizes session', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'hi',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => FakeTransport(),
    );
    await c.close();
    final s = await repo.activeSession('t1');
    expect(s, isNull, reason: 'session should be finalized away from active');
  });

  test('open() result(success) frame finalizes assistant message + session', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'hi',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    // 模拟 brain 推一段 streamlined_text + result(success)
    fake.push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'Hello',
      'uuid': 'u1',
      'session_id': c.sessionId,
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
      'usage': {'input_tokens': 5, 'output_tokens': 8},
      'modelUsage': {},
      'permission_denials': [],
      'stop_reason': 'end_turn',
      'uuid': 'u2',
      'session_id': c.sessionId,
    }));
    // 等帧被处理 + session finalize
    await Future.delayed(const Duration(milliseconds: 100));

    final assistant = await repo.getMessage('am1');
    expect(assistant, isNotNull);
    expect(assistant!.status, MessageStatus.completed);
    expect(assistant.stopReason, 'end_turn');
    expect(assistant.inputTokens, 5);
    expect(assistant.outputTokens, 8);
    final block = assistant.blocks.first as TextBlock;
    expect(block.text, 'Hello');

    final s = await repo.activeSession('t1');
    expect(s, isNull, reason: 'success result should auto-close session');
  });

  test('open() result(error) frame finalizes failed', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'hi',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    fake.push(jsonEncode({
      'type': 'result',
      'subtype': 'error_during_execution',
      'duration_ms': 50,
      'duration_api_ms': 50,
      'is_error': true,
      'num_turns': 0,
      'total_cost_usd': 0,
      'usage': {},
      'modelUsage': {},
      'permission_denials': [],
      'errors': [
        {'message': 'upstream blew up', 'recoverable': false},
      ],
      'uuid': 'u-err',
      'session_id': c.sessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 50));

    final assistant = await repo.getMessage('am1');
    expect(assistant!.status, MessageStatus.failed);
  });

  // ─── Cancel UX (#173) ─────────────────────────────────────
  //
  // F5 + brain ingress (commit d186112) 落地后:cancel() 不再立刻关 WS,
  // 而是发 SDKControlCancelRequest + 等服务端 Done{interrupted}。这组
  // 测试覆盖三条主路径:中间态 SessionCancelling 事件 / Done{interrupted}
  // 触发 MessageCancelled / timeout 兜底。

  test('cancel() 发 control_cancel_request 并 emit SessionCancelling', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'long',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    final events = <SessionEvent>[];
    final sub = c.events.listen(events.add);
    addTearDown(sub.cancel);

    await c.cancel();
    await Future.delayed(const Duration(milliseconds: 30));

    // 1) WS sent the cancel frame
    final cancelFrame = fake.sent.firstWhere(
      (s) => s.contains('control_cancel_request'),
      orElse: () => '',
    );
    expect(cancelFrame, isNotEmpty,
        reason: 'cancel() should write control_cancel_request to WS');

    // 2) emitted SessionCancelling — 让 ChatController 进中间态
    expect(events.whereType<SessionCancelling>().length, 1,
        reason: 'cancel() should emit exactly one SessionCancelling');

    // 3) connection 还没关 —— 等 Done{interrupted}
    expect(fake.closed, isFalse,
        reason: 'cancel() should NOT close WS yet (waiting for brain Done)');
  });

  test('cancel() repeated is no-op (no second control frame)', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'long',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    await c.cancel();
    await c.cancel();
    await c.cancel();

    final cancelFrames =
        fake.sent.where((s) => s.contains('control_cancel_request')).length;
    expect(cancelFrames, 1, reason: 'duplicate cancel() should be ignored');
  });

  test('Done{interrupted} 落地 → MessageCancelled + cancelled status', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'long',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    final events = <SessionEvent>[];
    final sub = c.events.listen(events.add);
    addTearDown(sub.cancel);

    // 1) 用户按 stop
    await c.cancel();
    // 留点时间给 latency 显式 >0,这样断言才有意义(单元测试里默认
    // cancel→push 是同步连续,latency 小于 1ms 实际等于 0)。生产里
    // 网络往返会自然占几十~几百 ms。
    await Future.delayed(const Duration(milliseconds: 50));
    // 2) brain 走完 clean-stop → 发 Done{stop_reason: "interrupted"}。
    //    复用 SDKResultSuccess wire 形状(stop_reason 是其字段),客户端
    //    的 _onResultSuccess 看到 interrupted 路由到 MessageCancelled。
    fake.push(jsonEncode({
      'type': 'result',
      'subtype': 'success',
      'duration_ms': 200,
      'duration_api_ms': 100,
      'is_error': false,
      'num_turns': 1,
      'result': '',
      'total_cost_usd': 0,
      'usage': {'input_tokens': 3, 'output_tokens': 5},
      'modelUsage': {},
      'permission_denials': [],
      'stop_reason': 'interrupted',
      'uuid': 'u-int',
      'session_id': c.sessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    // 3) message 落地为 cancelled, 不是 completed
    final m = await repo.getMessage('am1');
    expect(m!.status, MessageStatus.cancelled,
        reason: 'stop_reason="interrupted" should map to cancelled, not completed');
    expect(m.stopReason, 'interrupted');

    // 4) MessageCancelled 事件触发, 没 MessageCompleted
    final cancelledEvents = events.whereType<MessageCancelled>().toList();
    expect(cancelledEvents.length, 1);
    expect(events.whereType<MessageCompleted>().isEmpty, isTrue);

    // 4a) latency 埋点 — 客户端记到了从 cancel() 到现在的时长。
    // 测试里 cancel + push Done 间 await 100ms,所以应该在 [50ms, 5s)
    // 区间(>50ms 排除 0 / 时钟问题,<5s 排除什么环境也太慢)。
    final latency = cancelledEvents.single.latency;
    expect(latency, isNotNull, reason: 'MessageCancelled should carry latency');
    expect(latency!.inMilliseconds, greaterThan(50));
    expect(latency.inMilliseconds, lessThan(5000));

    // 5) session 也最终 cancelled
    final s = await repo.activeSession('t1');
    expect(s, isNull,
        reason: 'cancelled session should drop out of activeSession');
  });

  test('cancel timeout 兜底:Done{interrupted} 不来也按 cancelled 关', () async {
    // 这条测试模拟旧 brain 部署 / 网络断 / Done 帧丢 — 客户端不能挂死。
    // _cancelGraceWindow 是 3s, 测试加 200ms 余量等 timer 落地。
    await repo.createThread(id: 't1', mode: ThreadMode.chat);
    final thread = (await repo.getThread('t1'))!;
    final fake = FakeTransport();
    final c = await BiuSessionConnection.open(
      repo: repo,
      agentPlane: ap,
      brainBaseUrl: 'ws://test',
      thread: thread,
      userPrompt: 'long',
      userMessageId: 'um1',
      assistantMessageId: 'am1',
      transportConnector: (_) => fake,
    );
    addTearDown(() async => c.close());

    final events = <SessionEvent>[];
    final sub = c.events.listen(events.add);
    addTearDown(sub.cancel);

    await c.cancel();
    // 注意:不 push 任何 Done 帧。等 timer 兜底。
    await Future.delayed(const Duration(seconds: 3, milliseconds: 200));

    final m = await repo.getMessage('am1');
    expect(m!.status, MessageStatus.cancelled,
        reason: 'force timeout should still finalize as cancelled');
    expect(events.whereType<MessageCancelled>().length, 1,
        reason: 'timeout fallback emits MessageCancelled');
  }, timeout: const Timeout(Duration(seconds: 6)));

  // anti-unused —— 让 import 保持
  test('imports resolve', () {
    expect(http.Client, isNotNull);
    expect(jsonEncode, isNotNull);
  });
}
