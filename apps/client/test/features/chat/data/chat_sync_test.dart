// ChatSyncService 合并逻辑单测 —— fake brain (in-process HttpServer) +
// AppDb.memory()。覆盖:
//   1. 新 thread 灌入（消息 + text block 形状）
//   2. 已有 thread 元数据被服务端覆盖（本地专属字段保留）
//   3. 本地 pending 消息不被覆盖；本地独有 thread 保留
//   4. 幂等：重复执行结果一致
//   5. 本机已发消息不重复灌（session 关联去重）
//   6. 消息分页拉全
//   7. mid-stream 占位不下行，terminal 后补拉
//   8. syncThread 404 → noop

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
  HttpServer? _server;

  String get baseUrl => 'http://127.0.0.1:${_server!.port}';

  Future<void> start() async {
    _server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server!.listen(_handle);
  }

  Future<void> stop() async => _server?.close(force: true);

  void addThread(Map<String, dynamic> t) => threads.add(t);

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
      body = _listThreads(req.uri.queryParameters);
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
      body = {'messages': _listMessages(segs[2], req.uri.queryParameters)};
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
    final list = threads.where((t) {
      if (before == null) return true;
      return DateTime.parse(t['updated_at'] as String).isBefore(before);
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
    repo = ChatRepo(db, scope: 'test-scope');
    brain = _FakeBrain();
    await brain.start();
  });

  tearDown(() async {
    await brain.stop();
    await db.close();
  });

  ChatSyncService makeSvc({int messagePageSize = 100}) => ChatSyncService(
    repo: repo,
    baseUrl: brain.baseUrl,
    tokenProvider: () async => 'tok',
    messagePageSize: messagePageSize,
  );

  /// 把本地 thread 的 updatedAt 回拨 —— chat_repo_test 同款手法。
  Future<void> backdateThread(String id, DateTime at) async {
    await db.customStatement(
      'UPDATE chat_threads_v2 SET updated_at = ? WHERE id = ?',
      [at.millisecondsSinceEpoch ~/ 1000, id],
    );
  }

  test('新 thread 灌入:元数据 + 消息 + text block 形状', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(
        id: 't1',
        title: 'from mac',
        model: 'claude-x',
        lastMsgPreview: '你好！有什么可以帮你的？',
        updatedAt: t,
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm1',
        threadId: 't1',
        content: '你好',
        position: 1,
        createdAt: t,
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm2',
        threadId: 't1',
        role: 'assistant',
        content: '你好！有什么可以帮你的？',
        model: 'claude-x',
        promptTokens: 11,
        completionTokens: 22,
        position: 2,
        createdAt: t.add(const Duration(seconds: 3)),
      ),
    );

    final r = await makeSvc().syncThreads();

    expect(r.errors, isEmpty);
    expect(r.threadsFetched, 1);
    expect(r.threadsUpserted, 1);
    expect(r.messagesFetched, 2);
    expect(r.messagesWritten, 2);

    final thread = await repo.getThread('t1');
    expect(thread, isNotNull);
    expect(thread!.title, 'from mac');
    expect(thread.model, 'claude-x');
    expect(thread.mode, ThreadMode.chat); // 服务端无 mode 概念 → 默认 chat

    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(2));
    expect(msgs[0].id, 'sm1');
    expect(msgs[0].role, MessageRole.user);
    expect(msgs[0].status, MessageStatus.completed);
    expect(msgs[0].seq, 1);
    expect(msgs[0].assembledText, '你好');
    // block 形状与 BiuSessionConnection 写法一致: '${id}_b0' / index 0 / closed
    expect(msgs[0].blocks, hasLength(1));
    expect(msgs[0].blocks.first.id, 'sm1_b0');
    expect(msgs[0].blocks.first.state, BlockState.closed);
    expect(msgs[1].id, 'sm2');
    expect(msgs[1].role, MessageRole.assistant);
    expect(msgs[1].model, 'claude-x');
    expect(msgs[1].inputTokens, 11);
    expect(msgs[1].outputTokens, 22);
    expect(msgs[1].assembledText, '你好！有什么可以帮你的？');
  });

  test('已有 thread 元数据被服务端覆盖,本地专属字段保留', () async {
    await repo.createThread(
      id: 't1',
      mode: ThreadMode.agent,
      environmentId: 'env1',
      title: 'old',
    );
    await backdateThread('t1', DateTime.utc(2026, 1, 1));
    final serverTime = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(
        id: 't1',
        title: 'new title',
        model: 'm-x',
        pinned: true,
        updatedAt: serverTime,
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm1',
        threadId: 't1',
        content: 'hi',
        position: 1,
        createdAt: serverTime,
      ),
    );

    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);
    expect(r.threadsUpserted, 1);

    final t = await repo.getThread('t1');
    expect(t!.title, 'new title'); // 服务端权威
    expect(t.pinned, isTrue);
    expect(t.model, 'm-x');
    expect(t.updatedAt.isAtSameMomentAs(serverTime), isTrue);
    // 本地专属字段保留(服务端根本不知道这些概念)
    expect(t.mode, ThreadMode.agent);
    expect(t.environmentId, 'env1');
  });

  test('本地 pending 消息不被覆盖;本地独有 thread 保留', () async {
    await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 't1');
    await repo.appendMessage(
      id: 'lp1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.pending,
    );
    await repo.upsertBlock(
      const TextBlock(
        id: 'lp1_b0',
        index: 0,
        state: BlockState.closed,
        text: 'draft',
      ),
      messageId: 'lp1',
    );
    await repo.createThread(
      id: 'local-only',
      mode: ThreadMode.chat,
      title: '未发消息',
    );
    await backdateThread('t1', DateTime.utc(2026, 1, 1));

    brain.addThread(
      _threadJson(
        id: 't1',
        title: 't1',
        lastMsgPreview: 'real',
        updatedAt: DateTime.utc(2026, 7, 1, 12),
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm1',
        threadId: 't1',
        content: 'real',
        position: 1,
        createdAt: DateTime.utc(2026, 7, 1, 12),
      ),
    );

    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);

    final lp = await repo.getMessage('lp1');
    expect(lp, isNotNull);
    expect(lp!.status, MessageStatus.pending); // 未被服务端覆盖
    expect(lp.assembledText, 'draft');

    final sm = await repo.getMessage('sm1');
    expect(sm, isNotNull); // 服务端新消息照常灌入

    final localOnly = await repo.getThread('local-only');
    expect(localOnly, isNotNull); // 服务端没有的本地 thread 保留
    expect(localOnly!.title, '未发消息');
  });

  test('幂等:重复执行结果一致', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'x', updatedAt: t),
    );
    for (var i = 1; i <= 3; i++) {
      brain.addMessage(
        't1',
        _msgJson(
          id: 'sm$i',
          threadId: 't1',
          role: i.isOdd ? 'user' : 'assistant',
          content: 'msg $i',
          position: i,
          createdAt: t.add(Duration(seconds: i)),
        ),
      );
    }

    final r1 = await makeSvc().syncThreads();
    expect(r1.messagesWritten, 3);

    final r2 = await makeSvc().syncThreads();
    expect(r2.errors, isEmpty);
    expect(r2.threadsFetched, 1);
    expect(r2.threadsUpserted, 0); // 无变化不写
    expect(r2.threadsSkipped, 1); // updatedAt 一致 → 不拉消息
    expect(r2.messagesWritten, 0);

    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(3)); // 没有重复
  });

  test('本机已发消息不重复灌(session 关联去重)', () async {
    // 模拟 BiuSessionConnection.open 的本地写法:
    // user 消息无 sessionId,assistant 消息带 sessionId。
    await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 't1');
    await repo.appendMessage(
      id: 'lu1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.pending,
    );
    await repo.upsertBlock(
      const TextBlock(
        id: 'lu1_b0',
        index: 0,
        state: BlockState.closed,
        text: 'hello',
      ),
      messageId: 'lu1',
    );
    await repo.finalizeMessage('lu1', status: MessageStatus.completed);
    await repo.appendMessage(
      id: 'la1',
      threadId: 't1',
      role: MessageRole.assistant,
      status: MessageStatus.streaming,
      sessionId: 'sess1',
    );
    await repo.upsertBlock(
      const TextBlock(
        id: 'la1_t0',
        index: 0,
        state: BlockState.closed,
        text: 'world',
      ),
      messageId: 'la1',
    );
    await repo.finalizeMessage('la1', status: MessageStatus.completed);
    await backdateThread('t1', DateTime.utc(2026, 1, 1));

    // 服务端同一份对话,但 message id 是 brain 新生成的。
    brain.addThread(
      _threadJson(
        id: 't1',
        title: 't1',
        lastMsgPreview: 'world',
        updatedAt: DateTime.utc(2026, 7, 1, 12),
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'su1',
        threadId: 't1',
        content: 'hello',
        clientId: 'sess1:user',
        position: 1,
        createdAt: DateTime.utc(2026, 7, 1, 12),
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sa1',
        threadId: 't1',
        role: 'assistant',
        content: 'world',
        clientId: 'sess1:assistant',
        position: 2,
        createdAt: DateTime.utc(2026, 7, 1, 12, 0, 3),
      ),
    );

    final r = await makeSvc().syncThreads();
    expect(r.errors, isEmpty);
    expect(r.messagesFetched, 2);
    expect(r.messagesWritten, 0); // 全部去重跳过

    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(2)); // 没有 su1/sa1 重复行
    expect(msgs.map((m) => m.id), containsAll(['lu1', 'la1']));
  });

  test('消息分页拉全(messagePageSize=2 翻页)', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'x', updatedAt: t),
    );
    for (var i = 1; i <= 3; i++) {
      brain.addMessage(
        't1',
        _msgJson(
          id: 'sm$i',
          threadId: 't1',
          content: 'msg $i',
          position: i,
          createdAt: t.add(Duration(seconds: i)),
        ),
      );
    }

    final r = await makeSvc(messagePageSize: 2).syncThreads();
    expect(r.errors, isEmpty);
    expect(r.messagesFetched, 3);
    expect(r.messagesWritten, 3);
    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs.map((m) => m.id), ['sm1', 'sm2', 'sm3']);
  });

  test('mid-stream 占位不下行,terminal 后补拉', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'q', updatedAt: t),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm1',
        threadId: 't1',
        content: 'q',
        position: 1,
        createdAt: t,
      ),
    );
    brain.addMessage(
      't1',
      _msgJson(
        id: 'sm2',
        threadId: 't1',
        role: 'assistant',
        content: 'partial...',
        status: 'streaming',
        position: 2,
        createdAt: t.add(const Duration(seconds: 1)),
      ),
    );

    final r1 = await makeSvc().syncThreads();
    expect(r1.messagesWritten, 1); // 只灌了 user 消息
    var msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(1));

    // 服务端消息 terminal(brain 更新同一行 + trigger bump thread.updated_at)。
    brain.messages['t1']![1] = _msgJson(
      id: 'sm2',
      threadId: 't1',
      role: 'assistant',
      content: 'partial... full answer',
      position: 2,
      createdAt: t.add(const Duration(seconds: 5)),
    );
    final ti = brain.threads.indexWhere((x) => x['id'] == 't1');
    brain.threads[ti] = {
      ...brain.threads[ti],
      'updated_at': t.add(const Duration(seconds: 5)).toIso8601String(),
    };

    final r2 = await makeSvc().syncThreads();
    expect(r2.messagesWritten, 1);
    msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(2));
    expect(msgs[1].status, MessageStatus.completed);
    expect(msgs[1].assembledText, 'partial... full answer');
  });

  test('syncThread:404 noop / 新 thread 单点灌入', () async {
    // 404 —— 服务端没有,直接 noop 不抛。
    await makeSvc().syncThread('ghost');
    expect(await repo.getThread('ghost'), isNull);

    // 新 thread —— realtime chat.message_created 到达但本地还没有该行。
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(id: 't9', title: 'rt', lastMsgPreview: 'hi', updatedAt: t),
    );
    brain.addMessage(
      't9',
      _msgJson(
        id: 'sm1',
        threadId: 't9',
        content: 'hi',
        position: 1,
        createdAt: t,
      ),
    );

    await makeSvc().syncThread('t9');
    final thread = await repo.getThread('t9');
    expect(thread, isNotNull);
    expect(thread!.title, 'rt');
    final msgs = await repo.listMessagesOnce('t9');
    expect(msgs, hasLength(1));
  });

  test('sync_enabled=false 的会话不下行(隐私开关)', () async {
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(
      _threadJson(
        id: 'priv',
        title: 'private',
        lastMsgPreview: 'secret',
        syncEnabled: false,
        updatedAt: t,
      ),
    );
    brain.addMessage(
      'priv',
      _msgJson(
        id: 'pm1',
        threadId: 'priv',
        content: 'secret',
        position: 1,
        createdAt: t,
      ),
    );

    final r = await makeSvc().syncThreads();
    expect(r.threadsFetched, 1); // 服务端列表能看到
    expect(r.messagesFetched, 0); // 但一条消息都没拉
    expect(r.threadsUpserted, 0);
    expect(await repo.getThread('priv'), isNull); // 本地不落任何痕迹

    // realtime 单点入口同样拒绝。
    await makeSvc().syncThread('priv');
    expect(await repo.getThread('priv'), isNull);
  });

  test('同一秒内的二次服务端更新不漏拉(remoteUpdatedAtUs 精确比较)', () async {
    // Drift 把 DateTime 存成秒级整数 —— user/assistant 同秒落库时
    // threads.updated_at 两次 bump 序列化到同一秒。用秒级比较会漏掉第二
    // 次更新,这里钉死微秒标记的行为。
    final t1 = DateTime.utc(2026, 7, 1, 12, 0, 0, 500); // .500s
    brain.addThread(
      _threadJson(id: 't1', title: 't1', lastMsgPreview: 'q', updatedAt: t1),
    );
    brain.addMessage(
      't1',
      _msgJson(id: 'm1', threadId: 't1', content: 'q', position: 1, createdAt: t1),
    );

    final r1 = await makeSvc().syncThreads();
    expect(r1.messagesWritten, 1);
    final markers = await repo.remoteUpdatedMarkers();
    expect(markers['t1'], t1.microsecondsSinceEpoch);

    // 同一秒内 assistant 终态落库,trigger 把 updated_at bump 到 .900s。
    final t2 = DateTime.utc(2026, 7, 1, 12, 0, 0, 900);
    brain.addMessage(
      't1',
      _msgJson(
        id: 'm2',
        threadId: 't1',
        role: 'assistant',
        content: 'a',
        position: 2,
        createdAt: t2,
      ),
    );

    final r2 = await makeSvc().syncThreads();
    expect(r2.messagesWritten, 1); // m2 必须被拉到
    final msgs = await repo.listMessagesOnce('t1');
    expect(msgs, hasLength(2));
    expect(msgs[1].assembledText, 'a');

    // 第三次:服务端无变化 → 跳过,不重拉。
    final r3 = await makeSvc().syncThreads();
    expect(r3.messagesFetched, 0);
    expect(r3.threadsSkipped, 1);
  });

  test('对账:本地有服务端无的 message 删掉,in-flight 保留', () async {
    // 服务端为准:服务端已无的本地 message 应被对账删除,但本机在途
    // (pending/streaming/failed)是刚发、服务端尚未 terminal 的,绝不能删。
    final t = DateTime.utc(2026, 7, 1, 12);
    brain.addThread(_threadJson(id: 't1', title: 't1', updatedAt: t));
    // 注意:brain 不给 t1 任何 message —— 本地这三条全是「服务端没有」的。

    await repo.createThread(id: 't1', mode: ThreadMode.chat, title: 't1');
    await repo.appendMessage(
      id: 'orphan',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.completed, // 非在途 → 孤儿 → 删
    );
    await repo.appendMessage(
      id: 'pending1',
      threadId: 't1',
      role: MessageRole.user,
      status: MessageStatus.pending, // 在途 → 保留
    );
    await repo.appendMessage(
      id: 'streaming1',
      threadId: 't1',
      role: MessageRole.assistant,
      status: MessageStatus.streaming, // 在途 → 保留
    );

    await makeSvc().syncThread('t1');

    expect(await repo.getMessage('orphan'), isNull, reason: '孤儿 message 应被对账删除');
    expect(await repo.getMessage('pending1'), isNotNull, reason: 'pending 在途 message 必须保留');
    expect(await repo.getMessage('streaming1'), isNotNull, reason: 'streaming 在途 message 必须保留');
  });
}
