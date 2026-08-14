// ChatOutboxFlusher 单测（P1.3，设计
// docs/BiuMind-Local-Data-Isolation-Design.md §4）—— fake brain
// (in-process HttpServer) + AppDb.memory() + 注入时钟。覆盖:
//   1. 入队 → flush 成功（DELETE / PATCH archived / PATCH title），行删
//   2. 404 → 丢 op（目标已不存在，幂等收敛）
//   3. 500 / 网络错 → attempts+1 + 指数退避，到期前不重试，到期后重试成功
//   4. scope 隔离：flusher 只 flush 当前 scope 的 op
//   5. 无 token（登出过渡态）→ 整轮跳过，op 留表

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/outbox/chat_outbox_flusher.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';

class _FakeBrain {
  /// 'METHOD /path {body}' 请求日志。
  final List<String> requests = [];

  /// 按 path 注入响应状态码（默认 200）。
  final Map<String, int> statusByPath = {};

  HttpServer? _server;

  String get baseUrl => 'http://127.0.0.1:${_server!.port}';

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async => _server?.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    requests.add('${req.method} ${req.uri.path} $body');
    final status = statusByPath[req.uri.path] ?? 200;
    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(
      status >= 400
          ? jsonEncode({
              'error': {'code': 'err', 'message': 'boom'},
            })
          : '{}',
    );
    await req.response.close();
  }
}

void main() {
  late AppDb db;
  late ChatRepo repo;
  late _FakeBrain brain;

  setUp(() async {
    db = AppDb.memory();
    repo = ChatRepo(db, scope: 'scope-a');
    brain = _FakeBrain();
    await brain.start();
  });

  tearDown(() async {
    await brain.stop();
    await db.close();
  });

  ChatOutboxFlusher makeFlusher({
    ChatRepo? forRepo,
    DateTime Function()? clock,
    String? token = 'tok',
  }) =>
      ChatOutboxFlusher(
        repo: forRepo ?? repo,
        baseUrl: brain.baseUrl,
        tokenProvider: () async => token,
        clock: clock,
      );

  Future<List<ChatOutboxEntry>> pending() =>
      repo.dueChatOutbox(now: DateTime.utc(2100));

  test('入队 → flush 成功：DELETE / PATCH archived / PATCH title', () async {
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't1',
    );
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpArchiveThread,
      threadId: 't2',
      payload: const {'archived': true},
    );
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpRenameThread,
      threadId: 't3',
      payload: const {'title': '新标题'},
    );

    await makeFlusher().flushOnce();

    expect(brain.requests, [
      'DELETE /v1/threads/t1 ',
      'PATCH /v1/threads/t2 {"archived":true}',
      'PATCH /v1/threads/t3 {"title":"新标题"}',
    ]);
    expect(await pending(), isEmpty, reason: '成功的 op 已删除');
  });

  test('404 → 丢 op（幂等收敛），其他 op 照常', () async {
    brain.statusByPath['/v1/threads/gone'] = 404;
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 'gone',
    );
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't1',
    );

    await makeFlusher().flushOnce();

    expect(brain.requests, hasLength(2));
    expect(await pending(), isEmpty, reason: '404 的 op 已丢弃、成功的已删除');
  });

  test('500 → 指数退避：到期前不重试，到期后重试成功', () async {
    // fake 时钟锚定真实当前时间(秒精度, Drift DateTime 存秒) +1min ——
    // enqueueOutbox 用真实时钟写 nextAttemptAt=now, 锚未来值保证入队的
    // op 对 fake 时钟到期可 flush。之前硬编码 2026-08-10 12:00, 真实时间
    // 一过该时刻 op 就「永不到期」, 首条 flush 发不出请求 (time bomb)。
    final n = DateTime.now().toUtc();
    var now = DateTime.utc(n.year, n.month, n.day, n.hour, n.minute, n.second)
        .add(const Duration(minutes: 1));
    brain.statusByPath['/v1/threads/t1'] = 500;
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't1',
    );

    final flusher = makeFlusher(clock: () => now);
    await flusher.flushOnce();
    expect(brain.requests, hasLength(1));

    var rows = await pending();
    expect(rows, hasLength(1));
    expect(rows.single.attempts, 1);
    expect(rows.single.lastError, contains('500'));
    // 首次失败退避 2s（attempts=1 → 1<<1）。Drift 读回 DateTime 是本地
    // 时区 —— 转 UTC 比瞬时值。
    expect(
      rows.single.nextAttemptAt.toUtc(),
      now.add(const Duration(seconds: 2)),
    );

    // 未到期 —— flushOnce 不再打服务端。
    now = now.add(const Duration(seconds: 1));
    await flusher.flushOnce();
    expect(brain.requests, hasLength(1), reason: 'nextAttemptAt 未到不重试');

    // 到期 + 服务端恢复 → 重试成功，行删。
    now = now.add(const Duration(seconds: 2));
    brain.statusByPath.clear();
    await flusher.flushOnce();
    expect(brain.requests, hasLength(2));
    rows = await pending();
    expect(rows, isEmpty);
  });

  test('scope 隔离：只 flush 当前 scope 的 op', () async {
    final repoB = ChatRepo(db, scope: 'scope-b');
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't-a',
    );
    await repoB.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't-b',
    );

    await makeFlusher(forRepo: repo).flushOnce();

    expect(brain.requests, ['DELETE /v1/threads/t-a '],
        reason: 'scope-b 的 op 不能被 flush');
    expect(await repo.dueChatOutbox(now: DateTime.utc(2100)), isEmpty);
    expect(
      await repoB.dueChatOutbox(now: DateTime.utc(2100)),
      hasLength(1),
      reason: 'scope-b 的 op 原样留表（切回账号续传）',
    );
  });

  test('无 token（登出过渡态）→ 整轮跳过，op 留表', () async {
    await repo.enqueueOutbox(
      op: ChatRepo.outboxOpDeleteThread,
      threadId: 't1',
    );

    await makeFlusher(token: null).flushOnce();

    expect(brain.requests, isEmpty);
    expect(await pending(), hasLength(1));
  });
}
