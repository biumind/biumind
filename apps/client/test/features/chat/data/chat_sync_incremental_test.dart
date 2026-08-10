// ChatSyncService 增量 hydrate + 墓碑收敛单测（P1.2，设计
// docs/BiuMind-Local-Data-Isolation-Design.md §4）—— fake brain
// (in-process HttpServer) + AppDb.memory()。覆盖:
//   1. 首跑全量建 cursor；二跑增量只拉 updated_after 之后
//   2. threads 列表网络失败不建 cursor（下次重试不丢数据）
//   3. 单 thread 失败（errors 非空）不推进 cursor
//   4. 全量 reconcile：曾同步 thread 服务端消失 → 级联删；本机新建保留
//   5. thread 墓碑 → 增量路径级联删；tombstoneSince 推进
//   6. message 墓碑删单条；跨 scope 不删（repo scope 过滤）

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/chat/data/chat_repo.dart';
import 'package:biumind/features/chat/data/chat_sync.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

// ─── Fake brain ──────────────────────────────────────────

class _FakeBrain {
  final List<Map<String, dynamic>> threads = [];
  final Map<String, List<Map<String, dynamic>>> messages = {};
  final List<Map<String, String>> tombstones = [];
  bool failThreads = false;
  final Set<String> failMessagesFor = {};
  /// true = 模拟 P1.1 未部署的老服务端：tombstones 路由 404。
  bool tombstones404 = false;

  /// 每次 GET /v1/threads 的 query 参数 —— 断言 updated_after 用。
  final List<Map<String, String>> threadRequests = [];

  HttpServer? _server;

  String get baseUrl => 'http://127.0.0.1:${_server!.port}';

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async => _server?.close(force: true);

  void addThread(Map<String, dynamic> t) => threads.add(t);

  void removeThread(String id) {
    threads.removeWhere((t) => t['id'] == id);
    messages.remove(id);
  }

  void addMessage(String threadId, Map<String, dynamic> m) {
    messages.putIfAbsent(threadId, () => []).add(m);
    // 模拟 messages_touch_thread trigger:消息活动 bump thread.updated_at。
    final ti = threads.indexWhere((t) => t['id'] == threadId);
    if (ti >= 0) {
      threads[ti] = {
        ...threads[ti],
        'updated_at':
            (m['created_at'] as String?) ??
            DateTime.now().toUtc().toIso8601String(),
        'last_msg_preview': (m['content'] as String? ?? '').length > 200
            ? (m['content'] as String).substring(0, 200)
            : (m['content'] as String? ?? ''),
      };
    }
  }

  Future<void> _handle(HttpRequest req) async {
    final segs = req.uri.pathSegments;
    var status = 200;
    Object? body;
    if (segs.length == 2 && segs[0] == 'v1' && segs[1] == 'threads') {
      if (failThreads) {
        status = 500;
        body = {
          'error': {'code': 'internal', 'message': 'boom'},
        };
      } else {
        threadRequests.add(Map.of(req.uri.queryParameters));
        body = _listThreads(req.uri.queryParameters);
      }
    } else if (segs.length == 3 &&
        segs[0] == 'v1' &&
        segs[1] == 'chat' &&
        segs[2] == 'tombstones') {
      if (tombstones404) {
        status = 404;
        body = {
          'error': {'code': 'not_found', 'message': ''},
        };
      } else {
        body = _listTombstones(req.uri.queryParameters);
      }
    } else if (segs.length == 3 && segs[0] == 'v1' && segs[1] == 'threads') {
      Map<String, dynamic>? found;
      for (final t in threads) {
        if (t['id'] == segs[2]) found = t;
      }
      if (found == null) {
        status = 404;
        body = {
          'error': {'code': 'not_found', 'message': ''},
        };
      } else {
        body = found;
      }
    } else if (segs.length == 4 &&
        segs[0] == 'v1' &&
        segs[1] == 'threads' &&
        segs[3] == 'messages') {
      if (failMessagesFor.contains(segs[2])) {
        status = 500;
        body = {
          'error': {'code': 'internal', 'message': 'boom'},
        };
      } else {
        body = {'messages': _listMessages(segs[2], req.uri.queryParameters)};
      }
    } else {
      status = 404;
      body = {
        'error': {'code': 'not_found', 'message': ''},
      };
    }
    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(body));
    await req.response.close();
  }

  Map<String, dynamic> _listThreads(Map<String, String> qp) {
    final limit = int.tryParse(qp['limit'] ?? '') ?? 50;
    final before = qp['before'] != null
        ? DateTime.tryParse(qp['before']!)
        : null;
    final updatedAfter = qp['updated_after'] != null
        ? DateTime.tryParse(qp['updated_after']!)
        : null;
    final list = threads.where((t) {
      final ua = DateTime.parse(t['updated_at'] as String);
      // 服务端契约：updated_at > updated_after（严格大于）。
      if (updatedAfter != null && !ua.isAfter(updatedAfter)) return false;
      if (before != null && !ua.isBefore(before)) return false;
      return true;
    }).toList();
    list.sort((a, b) {
      final pa = (a['pinned'] as bool? ?? false) ? 1 : 0;
      final pb = (b['pinned'] as bool? ?? false) ? 1 : 0;
      if (pa != pb) return pb - pa;
      return DateTime.parse(
        b['updated_at'] as String,
      ).compareTo(DateTime.parse(a['updated_at'] as String));
    });
    final page = list.take(limit).toList();
    return {
      'threads': page,
      'next_cursor': page.length == limit
          ? page.last['updated_at'] as String
          : '',
    };
  }

  Map<String, dynamic> _listTombstones(Map<String, String> qp) {
    final limit = int.tryParse(qp['limit'] ?? '') ?? 200;
    final since = qp['since'] ?? '1970-01-01T00:00:00Z';
    final sinceDt = DateTime.parse(since);
    final list = tombstones.where((t) {
      // 契约：deleted_at > since，升序。
      return DateTime.parse(t['deleted_at']!).isAfter(sinceDt);
    }).toList()
      ..sort(
        (a, b) => DateTime.parse(
          a['deleted_at']!,
        ).compareTo(DateTime.parse(b['deleted_at']!)),
      );
    final page = list.take(limit).toList();
    return {
      'tombstones': page,
      // 契约：空页 next_since 回显入参 since。
      'next_since': page.isEmpty ? since : page.last['deleted_at'],
    };
  }

  List<Map<String, dynamic>> _listMessages(
    String threadId,
    Map<String, String> qp,
  ) {
    final limit = int.tryParse(qp['limit'] ?? '') ?? 50;
    final after = int.tryParse(qp['after'] ?? '') ?? 0;
    final list =
        (messages[threadId] ?? const <Map<String, dynamic>>[])
            .where((m) => (m['position'] as int) > after)
            .toList()
          ..sort(
            (a, b) => (a['position'] as int).compareTo(b['position'] as int),
          );
    return list.take(limit).toList();
  }
}

// ─── JSON builders（镜像 brain threadOut / messageOut）────

Map<String, dynamic> _threadJson({
  required String id,
  String title = '',
  String lastMsgPreview = '',
  String? model,
  bool pinned = false,
  bool archived = false,
  bool syncEnabled = true,
  required DateTime updatedAt,
  DateTime? createdAt,
}) {
  return {
    'id': id,
    'user_id': 'u1',
    'title': title,
    'last_msg_preview': lastMsgPreview,
    'pinned': pinned,
    'archived': archived,
    'sync_enabled': syncEnabled,
    'model': ?model,
    'created_at': (createdAt ?? updatedAt).toUtc().toIso8601String(),
    'updated_at': updatedAt.toUtc().toIso8601String(),
  };
}

Map<String, dynamic> _msgJson({
  required String id,
  required String threadId,
  String role = 'user',
  String content = '',
  String status = 'success',
  String? clientId,
  String? model,
  int? promptTokens,
  int? completionTokens,
  required int position,
  DateTime? createdAt,
}) {
  final at = (createdAt ?? DateTime.now().toUtc()).toUtc();
  return {
    'id': id,
    'thread_id': threadId,
    'role': role,
    'content': content,
    'parts': const [],
    'status': status,
    'client_id': ?clientId,
    'model': ?model,
    'prompt_tokens': ?promptTokens,
    'completion_tokens': ?completionTokens,
    'position': position,
    'created_at': at.toIso8601String(),
    'updated_at': at.toIso8601String(),
  };
}

// ─── tests ───────────────────────────────────────────────

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

  ChatSyncService makeSvc() => ChatSyncService(
    repo: repo,
    baseUrl: brain.baseUrl,
    tokenProvider: () async => 'tok',
  );

  test('首跑全量建 cursor；二跑增量只拉 updated_after 之后', () async {
    final tA = DateTime.utc(2026, 7, 1, 12, 0, 0, 500);
    final tB = DateTime.utc(2026, 7, 2, 12, 0, 0, 900);
    brain.addThread(_threadJson(id: 't1', title: 't1', updatedAt: tA));
    brain.addThread(_threadJson(id: 't2', title: 't2', updatedAt: tB));

    final r1 = await makeSvc().syncThreads();
    expect(r1.errors, isEmpty);
    expect(r1.threadsFetched, 2);
    expect(brain.threadRequests.single.containsKey('updated_after'), isFalse,
        reason: '首跑无 cursor → 全量');

    // cursor = 本轮最大 updated_at（RFC3339Nano 原样透传语义）。
    var state = await repo.chatSyncState();
    expect(state, isNotNull);
    expect(state!.threadsCursor, tB.toIso8601String());

    // 二跑：新增 t3（updated_at > cursor）—— 增量只拉它。
    final tC = DateTime.utc(2026, 7, 3, 12);
    brain.addThread(
      _threadJson(id: 't3', title: 't3', lastMsgPreview: 'hi', updatedAt: tC),
    );
    brain.addMessage(
      't3',
      _msgJson(id: 'sm1', threadId: 't3', content: 'hi', position: 1, createdAt: tC),
    );

    brain.threadRequests.clear();
    final r2 = await makeSvc().syncThreads();
    expect(r2.errors, isEmpty);
    expect(r2.threadsFetched, 1, reason: '增量只应看到 t3');
    expect(brain.threadRequests.first['updated_after'], tB.toIso8601String());
    expect(await repo.getThread('t3'), isNotNull);
    expect(await repo.listMessagesOnce('t3'), hasLength(1));

    state = await repo.chatSyncState();
    expect(state!.threadsCursor, tC.toIso8601String());

    // 三跑：服务端无变化 → 增量空页，cursor 不丢。
    final r3 = await makeSvc().syncThreads();
    expect(r3.threadsFetched, 0);
    state = await repo.chatSyncState();
    expect(state!.threadsCursor, tC.toIso8601String());
  });

  test('threads 列表网络失败不建 cursor（重试不丢数据）', () async {
    brain.failThreads = true;
    brain.addThread(
      _threadJson(id: 't1', title: 't1', updatedAt: DateTime.utc(2026, 7, 1)),
    );

    await expectLater(makeSvc().syncThreads(), throwsA(anything));
    expect(await repo.chatSyncState(), isNull, reason: '失败不能落游标');
    expect(await repo.getThread('t1'), isNull);

    // 恢复后重试照常 hydrate。
    brain.failThreads = false;
    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);
    expect(await repo.getThread('t1'), isNotNull);
    expect((await repo.chatSyncState())!.threadsCursor, isNotNull);
  });

  test('单 thread 失败（errors 非空）不推进 cursor', () async {
    final tA = DateTime.utc(2026, 7, 1, 12);
    final tB = DateTime.utc(2026, 7, 2, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'a', updatedAt: tA),
    );
    brain.addThread(
      _threadJson(id: 't2', title: 't2', lastMsgPreview: 'b', updatedAt: tB),
    );
    brain.addMessage(
      't1',
      _msgJson(id: 'm1', threadId: 't1', content: 'a', position: 1, createdAt: tA),
    );
    brain.addMessage(
      't2',
      _msgJson(id: 'm2', threadId: 't2', content: 'b', position: 1, createdAt: tB),
    );
    brain.failMessagesFor.add('t2');

    final r1 = await makeSvc().syncThreads();
    expect(r1.errors, hasLength(1));
    expect(await repo.getThread('t1'), isNotNull); // 成功的照常落
    // cursor 不推进 —— 失败 thread 的 updated_at ≤ 推进后的 cursor 时
    // 增量再也拉不到它（见 chat_sync.dart 文件头失败语义）。
    expect((await repo.chatSyncState())!.threadsCursor, isNull);

    // 恢复后下轮仍是全量，t2 补齐，cursor 推进。
    brain.failMessagesFor.clear();
    final r2 = await makeSvc().syncThreads();
    expect(r2.errors, isEmpty);
    expect(await repo.listMessagesOnce('t2'), hasLength(1));
    expect(
      (await repo.chatSyncState())!.threadsCursor,
      tB.toIso8601String(),
    );
  });

  test('全量 reconcile：曾同步 thread 服务端消失 → 级联删；本机新建保留', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'x', updatedAt: t),
    );
    brain.addMessage(
      't1',
      _msgJson(id: 'sm1', threadId: 't1', content: 'x', position: 1, createdAt: t),
    );
    await makeSvc().syncThreads();
    await repo.toggleReaction(messageId: 'sm1', threadId: 't1', kind: 'star');

    // 本机新建、从未同步（remoteUpdatedAtUs == null）的会话。
    await repo.createThread(id: 'local1', mode: ThreadMode.chat, title: '本机');

    // 他端删了 t1（服务端连墓碑都可能已过期 —— reconcile 不依赖墓碑）。
    brain.removeThread('t1');

    // desync 清游标 → 下轮回全量（chat_events_realtime._handleDesync 同款）。
    await repo.clearChatSyncState();
    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);
    expect(r.threadsReconciled, 1);

    expect(await repo.getThread('t1'), isNull, reason: '曾同步 + 服务端无 → 删');
    expect(await repo.getMessage('sm1'), isNull, reason: '级联删 message');
    expect(
      await repo.watchReactionsForMessage('sm1').first,
      isEmpty,
      reason: '级联删 reaction',
    );
    expect(await repo.getThread('local1'), isNotNull, reason: '本机新建保留');
  });

  test('增量 + thread 墓碑：级联删且 tombstoneSince 推进', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'a', updatedAt: t),
    );
    brain.addThread(
      _threadJson(id: 't2', title: 't2', lastMsgPreview: 'b', updatedAt: t),
    );
    brain.addMessage(
      't2',
      _msgJson(id: 'sm1', threadId: 't2', content: 'b', position: 1, createdAt: t),
    );
    await makeSvc().syncThreads();
    expect(await repo.getThread('t2'), isNotNull);

    // 他端删 t2：列表里消失 + 写墓碑（30 天保留期内的正常删除路径）。
    final deletedAt = DateTime.utc(2026, 7, 5, 12);
    brain.removeThread('t2');
    brain.tombstones.add({
      'id': 't2',
      'kind': 'thread',
      'deleted_at': deletedAt.toIso8601String(),
    });

    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);
    expect(r.threadsFetched, 0, reason: '无 updated_after 后的更新');
    expect(r.tombstonesApplied, 1);
    expect(await repo.getThread('t2'), isNull);
    expect(await repo.getMessage('sm1'), isNull, reason: '墓碑级联删 message');
    expect(await repo.getThread('t1'), isNotNull);

    // tombstoneSince 推进到 next_since；再跑一次墓碑为空页（回显 since）。
    final state = await repo.chatSyncState();
    expect(state!.tombstoneSince, deletedAt.toIso8601String());
    final r2 = await makeSvc().syncThreads();
    expect(r2.tombstonesApplied, 0);
  });

  test('message 墓碑删单条；跨 scope 不删', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'z', updatedAt: t),
    );
    brain.addMessage(
      't1',
      _msgJson(id: 'm1', threadId: 't1', content: 'a', position: 1, createdAt: t),
    );
    brain.addMessage(
      't1',
      _msgJson(id: 'm2', threadId: 't1', content: 'z', position: 2, createdAt: t),
    );
    await makeSvc().syncThreads();
    expect(await repo.listMessagesOnce('t1'), hasLength(2));

    // 另一 scope 的本地数据 —— 墓碑命中它的 id 也不能删（repo 强制
    // scope 过滤）。模拟「同设备换账号」场景。
    final repoB = ChatRepo(db, scope: 'scope-b');
    await repoB.createThread(id: 'x1', mode: ThreadMode.chat, title: 'B');
    await repoB.appendMessage(
      id: 'm9',
      threadId: 'x1',
      role: MessageRole.user,
      status: MessageStatus.completed,
    );

    final deletedAt = DateTime.utc(2026, 7, 5, 12);
    brain.messages['t1']!.removeWhere((m) => m['id'] == 'm2');
    brain.tombstones.add({
      'id': 'm2',
      'kind': 'message',
      'deleted_at': deletedAt.toIso8601String(),
    });
    // 越界墓碑（假装服务端 bug / 越权数据）—— scope-b 的行必须存活。
    brain.tombstones.add({
      'id': 'm9',
      'kind': 'message',
      'deleted_at': deletedAt.add(const Duration(seconds: 1)).toIso8601String(),
    });
    brain.tombstones.add({
      'id': 'x1',
      'kind': 'thread',
      'deleted_at': deletedAt.add(const Duration(seconds: 2)).toIso8601String(),
    });

    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);

    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs.map((m) => m.id), ['m1'], reason: 'm2 墓碑命中 → 删');
    expect(await repoB.getMessage('m9'), isNotNull, reason: '跨 scope 不删 message');
    expect(await repoB.getThread('x1'), isNotNull, reason: '跨 scope 不删 thread');

    // tombstoneSince 推进到最后一页 next_since（含越界墓碑 —— 已消费）。
    final state = await repo.chatSyncState();
    expect(
      state!.tombstoneSince,
      deletedAt.add(const Duration(seconds: 2)).toIso8601String(),
    );
  });

  test('老服务端无墓碑端点（404）→ 跳过不推进 tombstoneSince', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(_threadJson(id: 't1', title: 't1', updatedAt: t));
    await makeSvc().syncThreads();

    // 把 fake 调成「老服务端」：tombstones 路径 404。
    brain.tombstones404 = true;
    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty, reason: '404 降级不算错误');
    expect(r.tombstonesApplied, 0);
    expect(
      (await repo.chatSyncState())!.tombstoneSince,
      isNull,
      reason: '未消费任何墓碑，游标不推进',
    );
  });
}
