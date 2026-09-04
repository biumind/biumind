// ChatController R3 单测 —— ProviderContainer override deps，FakeTransport
// 注入。验：sendMessage / cancel / regenerate 状态机 + reactive 数据流。

import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/agent_plane/agent_plane_client.dart';
import 'package:biumind/data/api/biu_client.dart';
import 'package:biumind/data/api/chat_client.dart';
import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/application/chat_controller.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';
import 'package:biumind/features/settings/application/settings_controller.dart';
import 'package:biumind/services/account_registry.dart';
import 'package:biumind/services/settings_repo.dart';

class FakeAgentPlane extends AgentPlaneClient {
  FakeAgentPlane()
      : super(baseUrl: 'http://test', tokenProvider: () async => 'tok');
  int sessionCount = 0;
  String? lastUserMessageId;
  String? lastPrompt;
  List<ChatImageInput>? lastImages;

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
    sessionCount++;
    lastUserMessageId = userMessageId;
    lastPrompt = prompt;
    lastImages = images;
    return CreateSessionResp(
      sessionId: 'sess-$sessionCount',
      sessionToken: 'tok-$sessionCount',
      expiresAt: DateTime.now().add(const Duration(minutes: 30)),
      mode: mode,
      environmentId: environmentId,
      jetstreamSubjectIn: '',
      jetstreamSubjectOut: '',
    );
  }

  @override
  Future<RefreshTokenResp> refreshSessionToken(String sessionId) async {
    return RefreshTokenResp(
      sessionToken: 'fresh',
      expiresAt: DateTime.now().add(const Duration(minutes: 30)),
    );
  }
}

class FakeTransport implements BiuTransport {
  final _ctrl = StreamController<dynamic>.broadcast();
  final List<String> sent = [];
  bool closed = false;

  void push(dynamic frame) => _ctrl.add(frame);

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

/// 造一个带 sub 的假 JWT (只解 payload, 不验签)。
String _fakeJwt(String sub) {
  String b64(Map<String, dynamic> o) =>
      base64Url.encode(utf8.encode(jsonEncode(o))).replaceAll('=', '');
  return '${b64({'alg': 'HS256', 'typ': 'JWT'})}.${b64({'sub': sub})}.sig';
}

void main() {
  late AppDb db;
  late ChatRepo repo;
  late FakeAgentPlane ap;
  late ProviderContainer container;
  /// 每条 connection 拿到自己的 FakeTransport（按调用顺序）。
  late List<FakeTransport> transports;
  /// helper closure: 拿最后一个 transport（最新创建的 session）。
  FakeTransport currentTransport() => transports.last;

  setUp(() {
    db = AppDb.memory();
    repo = ChatRepo(db, scope: 'test-scope');
    ap = FakeAgentPlane();
    transports = [];
    container = ProviderContainer(overrides: [
      chatControllerDepsProvider.overrideWithValue(ChatControllerDeps(
        repo: repo,
        agentPlane: ap,
        chatClient: ChatClient(Uri.parse('http://test'), 'tok'),
        brainBaseUrl: 'ws://test',
        transportConnector: (_) {
          final t = FakeTransport();
          transports.add(t);
          return t;
        },
      )),
    ]);
  });

  tearDown(() async {
    container.dispose();
    // Pump event loop so the connection's _disposeConnection (kicked off
    // by container.dispose() via Riverpod onDispose) completes before we
    // close the DB. Without this the cancel-timeout timer (3s, still
    // queued after a cancel() test) can race with db.close() and surface
    // "test failed after it had already completed" warnings.
    await Future<void>.delayed(Duration.zero);
    await Future<void>.delayed(Duration.zero);
    await db.close();
  });

  Future<Thread> createTestThread({
    String id = 't1',
    ThreadMode mode = ThreadMode.chat,
    String? envId,
  }) async {
    return repo.createThread(id: id, mode: mode, environmentId: envId);
  }

  test('build() returns idle state when no active session', () async {
    await createTestThread();
    final state = await container.read(chatControllerProvider('t1').future);
    expect(state.isStreaming, false);
    expect(state.activeAssistantMessageId, isNull);
    expect(state.lastError, isNull);
  });

  test('build() returns idle for non-existent thread', () async {
    final state = await container.read(chatControllerProvider('nope').future);
    expect(state.isStreaming, false);
  });

  test('sendMessage() opens new session, transitions isStreaming=true', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);

    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hello world');

    // 等帧 pump 跑一圈
    await Future.delayed(const Duration(milliseconds: 50));

    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isStreaming, true);
    expect(state.activeAssistantMessageId, isNotNull);
    expect(ap.sessionCount, 1);

    // user message + assistant placeholder 落 db
    final messages = await repo.watchMessages('t1').first;
    expect(messages.length, 2);
    expect(messages[0].role, MessageRole.user);
    expect(messages[0].assembledText, 'hello world');
    expect(messages[1].role, MessageRole.assistant);
    expect(messages[1].status, MessageStatus.streaming);
  });

  test('clearError() resets lastError without touching isStreaming', () async {
    await createTestThread();
    final notifier = container.read(chatControllerProvider('t1').notifier);
    await container.read(chatControllerProvider('t1').future);
    // 注入一个 error 进 state，模拟 send 失败。
    await notifier.sendMessage('boom');
    await Future.delayed(const Duration(milliseconds: 30));
    // 假设当前没真错，先手动塞 lastError 进去。
    final cur = container.read(chatControllerProvider('t1')).value!;
    notifier.state = AsyncValue.data(cur.copyWith(lastError: 'simulated'));
    expect(container.read(chatControllerProvider('t1')).value!.lastError,
        'simulated');

    notifier.clearError();
    final after = container.read(chatControllerProvider('t1')).value!;
    expect(after.lastError, isNull);
    // isStreaming 不被清掉
    expect(after.isStreaming, cur.isStreaming);
  });

  test('clearError() noop when no error', () async {
    await createTestThread();
    final notifier = container.read(chatControllerProvider('t1').notifier);
    await container.read(chatControllerProvider('t1').future);
    notifier.clearError();
    expect(container.read(chatControllerProvider('t1')).value!.lastError,
        isNull);
  });

  test('sendMessage() empty / whitespace ignored', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('   ');
    expect(ap.sessionCount, 0);
    final messages = await repo.watchMessages('t1').first;
    expect(messages, isEmpty);
  });

  test('result(success) frame transitions isStreaming=false', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 30));

    final sessionId = ap.sessionCount > 0 ? 'sess-${ap.sessionCount}' : '';
    currentTransport().push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'world',
      'uuid': 'u1',
      'session_id': sessionId,
    }));
    currentTransport().push(jsonEncode({
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
      'session_id': sessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isStreaming, false);
    expect(state.activeAssistantMessageId, isNull);
  });

  test('result(error) frame sets lastError + isStreaming=false', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 30));

    final sessionId = 'sess-${ap.sessionCount}';
    currentTransport().push(jsonEncode({
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
      'session_id': sessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isStreaming, false);
    expect(state.lastError, isNotNull);
    expect(state.lastError, contains('upstream blew up'));
  });

  test('cancel() enters cancelling intermediate state + sends cancel frame', () async {
    // #173: cancel() 不再立刻 flip isStreaming=false。改成进 cancelling
    // 中间态(isCancelling=true,isStreaming 保持 true), Composer 据此让 stop
    // 按钮 disable 防 spam。真正 idle 由 brain 的 Done{interrupted}
    // 触发(下一个 test 覆盖)或 3s timeout 兜底。
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 30));

    expect(container.read(chatControllerProvider('t1')).value!.isStreaming, true);

    await container.read(chatControllerProvider('t1').notifier).cancel();
    await Future.delayed(const Duration(milliseconds: 50));

    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isCancelling, true,
        reason: 'cancel() should flip isCancelling immediately for UI feedback');
    expect(state.isStreaming, true,
        reason: 'isStreaming stays true until brain Done lands; UI shows current msg');
    expect(state.activeAssistantMessageId, isNotNull,
        reason: 'active message preserved during cancelling — UI does not flash blank');

    // transport 收到了 cancel 帧
    final hasCancelFrame = currentTransport().sent.any((s) {
      try {
        final j = jsonDecode(s);
        return j is Map && j['type'] == 'control_cancel_request';
      } catch (_) {
        return false;
      }
    });
    expect(hasCancelFrame, true);
  });

  test('cancel() + Done{interrupted} → idle state, message marked cancelled', () async {
    // 完整 cancel UX 端到端:从 streaming 到 idle 经过 cancelling 中间态,
    // brain Done{stop_reason:"interrupted"} 落地后 isCancelling/isStreaming
    // 都归零, message 是 cancelled 不是 completed。
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 30));

    final amId = container
        .read(chatControllerProvider('t1'))
        .value!
        .activeAssistantMessageId!;
    final sessionId = (await container
            .read(chatControllerDepsProvider)
            .repo
            .activeSession('t1'))!
        .sessionId;

    await container.read(chatControllerProvider('t1').notifier).cancel();
    await Future.delayed(const Duration(milliseconds: 30));
    expect(container.read(chatControllerProvider('t1')).value!.isCancelling, true);

    // 模拟 brain 发回 Done{interrupted}
    currentTransport().push(jsonEncode({
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
      'stop_reason': 'interrupted',
      'uuid': 'u-interrupted',
      'session_id': sessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isStreaming, false);
    expect(state.isCancelling, false);
    expect(state.activeAssistantMessageId, isNull);
    expect(state.lastError, isNull, reason: 'cancelled is not an error');

    final m = await container.read(chatControllerDepsProvider).repo.getMessage(amId);
    expect(m!.status, MessageStatus.cancelled);
    expect(m.stopReason, 'interrupted');
  });

  test('cancel() noop when not streaming', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);
    // 不调 sendMessage，直接 cancel
    await container.read(chatControllerProvider('t1').notifier).cancel();
    final state = container.read(chatControllerProvider('t1')).value!;
    expect(state.isStreaming, false);
  });

  test('regenerate() 同 id 重发 user 消息,不产生重复 prompt', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);

    // 先发一条让有 user + assistant
    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('first prompt');
    await Future.delayed(const Duration(milliseconds: 30));
    final firstSessionId = 'sess-${ap.sessionCount}';
    final firstUserMessageId = ap.lastUserMessageId;

    // 推 result 让第一条 session 完成
    currentTransport().push(jsonEncode({
      'type': 'streamlined_text',
      'text': 'first answer',
      'uuid': 'u1',
      'session_id': firstSessionId,
    }));
    currentTransport().push(jsonEncode({
      'type': 'result',
      'subtype': 'success',
      'duration_ms': 1, 'duration_api_ms': 1, 'is_error': false,
      'num_turns': 1, 'result': '', 'total_cost_usd': 0,
      'usage': {}, 'modelUsage': {}, 'permission_denials': [],
      'stop_reason': 'end_turn',
      'uuid': 'u2', 'session_id': firstSessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    final beforeRegen = await repo.watchMessages('t1').first;
    expect(beforeRegen.length, 2);
    final assistantId = beforeRegen[1].id;

    final preCount = ap.sessionCount;
    await container
        .read(chatControllerProvider('t1').notifier)
        .regenerate(assistantId);
    await Future.delayed(const Duration(milliseconds: 100));

    expect(ap.sessionCount, preCount + 1,
        reason: 'regenerate should open new brain session');
    // 复用原 user message id 重发,而不是新建同文案消息
    expect(ap.lastUserMessageId, firstUserMessageId);
    expect(ap.lastPrompt, 'first prompt');

    // 形态：[user(同 id), new_assistant] —— 不再出现重复 user 气泡
    final afterRegen = await repo.watchMessages('t1').first;
    expect(afterRegen.length, 2);
    expect(afterRegen[0].role, MessageRole.user);
    expect(afterRegen[0].id, firstUserMessageId);
    expect(afterRegen[0].assembledText, 'first prompt');
    expect(afterRegen[1].role, MessageRole.assistant);
  });

  test('regenerateFromUserMessage() 带图 prompt 重新生成时转发原附件', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);

    final png = base64Decode(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==');
    await container.read(chatControllerProvider('t1').notifier).sendMessage(
        '看图说话',
        attachments: [AttachmentInput(mimeType: 'image/png', bytes: png)]);
    await Future.delayed(const Duration(milliseconds: 30));
    expect(ap.lastImages, isNotNull);
    expect(ap.lastImages!.length, 1);

    final firstSessionId = 'sess-${ap.sessionCount}';
    currentTransport().push(jsonEncode({
      'type': 'result',
      'subtype': 'success',
      'duration_ms': 1, 'duration_api_ms': 1, 'is_error': false,
      'num_turns': 1, 'result': 'ok', 'total_cost_usd': 0,
      'usage': {}, 'modelUsage': {}, 'permission_denials': [],
      'stop_reason': 'end_turn',
      'uuid': 'u1', 'session_id': firstSessionId,
    }));
    await Future.delayed(const Duration(milliseconds: 100));

    final msgs = await repo.watchMessages('t1').first;
    final userMsg = msgs.firstWhere((m) => m.role == MessageRole.user);
    expect(userMsg.blocks.whereType<ImageBlock>().length, 1);

    await container
        .read(chatControllerProvider('t1').notifier)
        .regenerateFromUserMessage(userMsg.id);
    await Future.delayed(const Duration(milliseconds: 100));

    // 重发仍带同一张图,且复用原 message id
    expect(ap.lastUserMessageId, userMsg.id);
    expect(ap.lastPrompt, '看图说话');
    expect(ap.lastImages, isNotNull);
    expect(ap.lastImages!.length, 1);
    expect(ap.lastImages!.first.mimeType, 'image/png');
    expect(base64Decode(ap.lastImages!.first.data), png);

    final after = await repo.watchMessages('t1').first;
    expect(after.where((m) => m.role == MessageRole.user).length, 1,
        reason: '重新生成不得产生重复 user 消息');
  });

  test('messagesProvider streams updates as repo changes', () async {
    await createTestThread();
    await container.read(chatControllerProvider('t1').future);

    final emissions = <int>[];
    final sub = container.listen<AsyncValue<List<Message>>>(
      messagesProvider('t1'),
      (_, next) {
        next.whenData((msgs) => emissions.add(msgs.length));
      },
      fireImmediately: true,
    );
    addTearDown(sub.close);

    await Future.delayed(const Duration(milliseconds: 20));
    final initialLen = emissions.length;

    await container
        .read(chatControllerProvider('t1').notifier)
        .sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 80));

    expect(emissions.length, greaterThan(initialLen),
        reason: 'messagesProvider should re-emit when sendMessage adds rows');
    expect(emissions.last, greaterThanOrEqualTo(2));
  });

  test('threadsProvider lists newly created threads', () async {
    final emitted = <List<Thread>>[];
    final sub = container.listen<AsyncValue<List<Thread>>>(
      threadsProvider,
      (_, next) {
        next.whenData(emitted.add);
      },
      fireImmediately: true,
    );
    addTearDown(sub.close);

    await createTestThread(id: 'a');
    await Future.delayed(const Duration(milliseconds: 20));
    await createTestThread(id: 'b', mode: ThreadMode.agent, envId: 'env-1');
    await Future.delayed(const Duration(milliseconds: 20));

    final last = emitted.last;
    expect(last.length, 2);
    expect(last.any((t) => t.id == 'a'), true);
    expect(last.any((t) => t.id == 'b'), true);
  });

  test('P2 多账号: owner scope 变化关闭活跃连接; 同人 token 轮换不关', () async {
    // 独立 container: chatOwnerScopeProvider 走真实 settings 链 (account A
    // 登录态), deps 复用共享 fake (repo/transport 同源, 便于断言)。
    // 切账号用 applyRefreshed 换不同 sub 的 JWT 模拟 —— 对 scope provider
    // 而言与 switchAccount 等效 (都是 settings identity slice 原子换)。
    final settingsRepo = InMemorySettingsRepo(AppSettings(
      identityUrl: 'http://x',
      accessToken: _fakeJwt('user-a'),
      refreshToken: 'rt-a',
    ));
    final c = ProviderContainer(overrides: [
      chatControllerDepsProvider
          .overrideWithValue(container.read(chatControllerDepsProvider)),
      settingsRepoProvider.overrideWithValue(settingsRepo),
      accountRegistryStoreProvider
          .overrideWithValue(InMemoryAccountRegistryStore()),
    ]);
    addTearDown(c.dispose);
    await c.read(settingsControllerProvider.future);

    await createTestThread();
    await c.read(chatControllerProvider('t1').future);
    await c.read(chatControllerProvider('t1').notifier).sendMessage('hi');
    await Future.delayed(const Duration(milliseconds: 50));
    expect(transports, hasLength(1));
    expect(c.read(chatControllerProvider('t1')).value!.isStreaming, isTrue);
    final connTransport = currentTransport();

    // 同人 token 轮换 (同 sub 新 JWT) → scope 不变 → 连接不动。
    await c.read(settingsControllerProvider.notifier).applyRefreshed(
          accessToken: _fakeJwt('user-a'),
          refreshToken: 'rt-a-rotated',
          tokenExpiresAt:
              DateTime.now().toUtc().add(const Duration(hours: 1)).toIso8601String(),
        );
    await Future.delayed(const Duration(milliseconds: 50));
    expect(connTransport.closed, isFalse, reason: 'token 轮换不该杀在途会话');
    expect(transports, hasLength(1), reason: 'scope 没变 → family 实例不重建');
    expect(c.read(chatControllerProvider('t1')).value!.isStreaming, isTrue);

    // 换账号 (不同 sub) → scope 变 → 重建 → onDispose 关旧连接。
    await c.read(settingsControllerProvider.notifier).applyRefreshed(
          accessToken: _fakeJwt('user-b'),
          refreshToken: 'rt-b',
          tokenExpiresAt:
              DateTime.now().toUtc().add(const Duration(hours: 1)).toIso8601String(),
        );
    await Future.delayed(const Duration(milliseconds: 100));
    expect(connTransport.closed, isTrue,
        reason: '切账号必须关掉旧账号的活跃 WS');
    // 注: 重建后新 build 是否 resume 出「新」连接存在 dispose/close 时序
    // race (测试里 deps 是静态 fake, scope 不变); 生产上 deps 随 creds 重建,
    // 新 scope 的 repo.getThread(旧 threadId) 必为 null, 不会误接旧会话。
    // 这里只断言核心语义: 旧连接被关。
  });
}
