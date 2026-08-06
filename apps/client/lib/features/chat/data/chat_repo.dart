// ChatRepo —— Chat 重构 R1 持久化层。
//
// 职责：drift v2 表的 CRUD + reactive watchers。BiuSessionConnection（R2）
// 通过 ChatRepo 落帧；ChatController（R3）通过 ChatRepo watch 数据驱动 UI。
//
// 不在这里：BiuClient 网络层（apps/client/lib/data/api/biu_client.dart）、
// AgentPlaneClient HTTP（apps/client/lib/data/agent_plane/）、SDK Protocol
// → Block 翻译（R2 BiuSessionConnection 内部）。
//
// 测试：chat_repo_test.dart 用 AppDb.memory()。

import 'dart:async';

import 'package:drift/drift.dart';
import 'package:uuid/uuid.dart';

import '../../../data/local/db.dart';
import '../domain/chat_models.dart';
import '../domain/thread_export_json.dart';

class ChatRepo {
  final AppDb db;
  ChatRepo(this.db);

  // ── Threads ───────────────────────────────────────────────

  /// 列所有 thread（pinned 优先 + updatedAt desc）。archived 过滤掉。
  /// [projectId] 给 wiki 项目内嵌面板按 project_id 过滤；null 默认列
  /// 全局对话（project_id IS NULL）；'__all' 不过滤（暂不暴露）。
  Stream<List<Thread>> watchThreads({String? projectId}) {
    final q = db.select(db.chatThreadsV2)..where((t) => t.archived.equals(false));
    if (projectId != null) {
      q.where((t) => t.projectId.equals(projectId));
    } else {
      q.where((t) => t.projectId.isNull());
    }
    q.orderBy([
      (t) => OrderingTerm(expression: t.pinned, mode: OrderingMode.desc),
      (t) => OrderingTerm(expression: t.updatedAt, mode: OrderingMode.desc),
    ]);
    return q.watch().map((rows) => rows.map(_threadFromRow).toList());
  }

  Stream<Thread?> watchThread(String id) {
    final q = db.select(db.chatThreadsV2)..where((t) => t.id.equals(id));
    return q.watchSingleOrNull().map((row) => row == null ? null : _threadFromRow(row));
  }

  Future<Thread?> getThread(String id) async {
    final q = db.select(db.chatThreadsV2)..where((t) => t.id.equals(id));
    final row = await q.getSingleOrNull();
    return row == null ? null : _threadFromRow(row);
  }

  Future<Thread> createThread({
    required String id,
    required ThreadMode mode,
    String? title,
    String? environmentId,
    String? poolTag,
    String? model,
    String? providerId,
    String? systemPrompt,
    String? projectId,
    String? workdir,
    AutoApproveMode autoApprove = AutoApproveMode.manual,
    String runtimeEnvMode = 'none',
    String backend = 'biumindkit',
  }) async {
    final now = DateTime.now();
    await db.into(db.chatThreadsV2).insert(ChatThreadsV2Companion.insert(
          id: id,
          mode: mode.name,
          title: Value(title ?? ''),
          environmentId: Value(environmentId),
          poolTag: Value(poolTag),
          model: Value(model),
          providerId: Value(providerId),
          systemPrompt: Value(systemPrompt),
          projectId: Value(projectId),
          workdir: Value(workdir),
          autoApprove: Value(autoApprove.name),
          runtimeEnvMode: Value(runtimeEnvMode),
          backend: Value(backend),
          createdAt: now,
          updatedAt: now,
        ));
    return Thread(
      id: id,
      title: title ?? '',
      mode: mode,
      environmentId: environmentId,
      poolTag: poolTag,
      model: model,
      providerId: providerId,
      systemPrompt: systemPrompt,
      projectId: projectId,
      workdir: workdir,
      autoApprove: autoApprove,
      runtimeEnvMode: runtimeEnvMode,
      backend: backend,
      createdAt: now,
      updatedAt: now,
    );
  }

  Future<void> renameThread(String id, String title) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        title: Value(title),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 设置 thread 的 model + 可选的 provider_id 路由。两者一起改是因为同
  /// model id 可能在多个 provider 下都存在(BiuMind Cloud 的 claude-sonnet-4-6
  /// vs 用户加的 Anthropic provider 的 claude-sonnet-4-6),分开 set 容易
  /// model 跟 providerId 错配。
  ///
  /// model 传 null = "BiuMind 默认"(brain 自己挑);providerId 传 null
  /// 同样表示不锁路由。
  Future<void> setThreadModel(
    String id,
    String? model, {
    String? providerId,
  }) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        model: Value(model),
        providerId: Value(providerId),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 切换 thread 的运行模式（chat / agent / task）。
  ///
  /// chat → agent / task 必须传 [environmentId]（agent）或 [poolTag]（task）；
  /// agent / task → chat 应该把 environmentId / poolTag 显式传 null 清空，
  /// 否则下次发送会带着旧的 env_id 给 brain 报"environment_offline"。
  ///
  /// 不校验 environment 是否在线 —— 调用方（UI 层）负责拦下"无 daemon 时
  /// 切到 agent"的场景。这里只做数据落库。
  Future<void> setThreadMode(
    String id,
    ThreadMode mode, {
    String? environmentId,
    String? poolTag,
  }) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        mode: Value(mode.name),
        environmentId: Value(environmentId),
        poolTag: Value(poolTag),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 设置 thread 的工具执行环境（Runtime v3 轴 B）：'none' | 'local' | 'cloud'。
  /// agent 模式可在 local / cloud 间切；chat 恒 none；task 恒 cloud。createSession
  /// 时透传给 brain → agent_sessions.runtime_env_mode。
  Future<void> setThreadRuntimeEnvMode(String id, String runtimeEnvMode) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        runtimeEnvMode: Value(runtimeEnvMode),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 设置 agent loop backend（Runtime v3 R3/Q3）：'biumindkit' | 'claude-cli'
  /// | 'codex-cli'。仅 agent 模式有意义。createSession 时透传给 brain。
  Future<void> setThreadBackend(String id, String backend) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        backend: Value(backend),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 设置 agent / task 模式下 daemon 跑工具的工作目录。空字符串 → 清空。
  /// chat 模式调用合法但无意义（daemon 不会被调度）。
  Future<void> setThreadWorkdir(String id, String? workdir) async {
    final normalized = (workdir == null || workdir.trim().isEmpty)
        ? null
        : workdir.trim();
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        workdir: Value(normalized),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 设置 Agent 工具调用自治程度。
  Future<void> setThreadAutoApprove(String id, AutoApproveMode mode) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        autoApprove: Value(mode.name),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 修改 thread 的 system prompt。空字符串会被存为空，让 brain 把它当无系统
  /// prompt 处理（chat 模式默认行为）。
  Future<void> setSystemPrompt(String id, String? prompt) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        systemPrompt: Value(prompt),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  Future<void> setPinned(String id, bool pinned) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        pinned: Value(pinned),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 列归档 thread（archived=true）—— 归档管理页用。
  /// 不按 projectId 过滤；当前归档管理是全局视图。
  Stream<List<Thread>> watchArchivedThreads() {
    final q = db.select(db.chatThreadsV2)
      ..where((t) => t.archived.equals(true))
      ..orderBy([
        (t) => OrderingTerm(expression: t.updatedAt, mode: OrderingMode.desc),
      ]);
    return q.watch().map((rows) => rows.map(_threadFromRow).toList());
  }

  /// 解归档 —— 把 thread 恢复到主列表。
  Future<void> unarchiveThread(String id) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        archived: const Value(false),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 简单计数：总 thread / 总 message（仅 completed）。Hero 副标题用。
  Future<({int threadCount, int messageCount})> threadStats() async {
    final tCount = await (db.selectOnly(db.chatThreadsV2)
          ..addColumns([db.chatThreadsV2.id.count()]))
        .map((row) => row.read(db.chatThreadsV2.id.count()) ?? 0)
        .getSingle();
    final mCount = await (db.selectOnly(db.chatMessagesV2)
          ..addColumns([db.chatMessagesV2.id.count()])
          ..where(db.chatMessagesV2.status.equals('completed')))
        .map((row) => row.read(db.chatMessagesV2.id.count()) ?? 0)
        .getSingle();
    return (threadCount: tCount, messageCount: mCount);
  }

  /// 连续活跃天数 streak —— 从今天向前数有 message 的连续天。
  /// 算法：取最近 60 天 message 的 distinct local date，walk back from today。
  /// 今天没活跃但昨天有 → 从昨天起算（保留昨天的 streak，避免今天没打开 app
  /// 就清零）。今天和昨天都没 → 0。
  Future<int> dailyStreak({int lookbackDays = 60}) async {
    final since = DateTime.now().subtract(Duration(days: lookbackDays));
    final rows = await db.customSelect(
      '''
      SELECT DISTINCT strftime('%Y-%m-%d', datetime(created_at, 'unixepoch', 'localtime')) AS d
      FROM chat_messages_v2
      WHERE status = 'completed' AND created_at > ?
      ''',
      variables: [
        Variable.withInt(since.toUtc().millisecondsSinceEpoch ~/ 1000),
      ],
      readsFrom: {db.chatMessagesV2},
    ).get();
    final dates = rows.map((r) => r.read<String>('d')).toSet();
    if (dates.isEmpty) return 0;
    final today = DateTime.now();
    String fmt(DateTime d) {
      final l = d.toLocal();
      String two(int n) => n.toString().padLeft(2, '0');
      return '${l.year}-${two(l.month)}-${two(l.day)}';
    }

    // 起点：今天有活跃 → 从今天数；今天没但昨天有 → 从昨天数；否则 0。
    var cursor = today;
    if (!dates.contains(fmt(cursor))) {
      cursor = cursor.subtract(const Duration(days: 1));
      if (!dates.contains(fmt(cursor))) return 0;
    }
    var streak = 0;
    while (dates.contains(fmt(cursor))) {
      streak++;
      cursor = cursor.subtract(const Duration(days: 1));
    }
    return streak;
  }

  /// 最近用过的模型（按最近一次使用时间 desc 去重）。Hero "最近用过" 行用。
  /// 默认拿前 5 个；fallback 到 thread.model 或 message.model 任意非空都算。
  Future<List<({String code, DateTime lastUsed})>> recentModels({
    int limit = 5,
  }) async {
    final rows = await db.customSelect(
      '''
      SELECT model, MAX(created_at) AS last_used
      FROM chat_messages_v2
      WHERE model IS NOT NULL AND model != ''
      GROUP BY model
      ORDER BY last_used DESC
      LIMIT ?
      ''',
      variables: [Variable.withInt(limit)],
      readsFrom: {db.chatMessagesV2},
    ).get();
    return rows.map((r) {
      return (
        code: r.read<String>('model'),
        lastUsed: DateTime.fromMillisecondsSinceEpoch(
          r.read<int>('last_used') * 1000,
          isUtc: true,
        ),
      );
    }).toList(growable: false);
  }

  /// 最近 [days] 天活跃统计（默认 7 天）—— Hero 周报 chip 用。
  /// 数 since 的 message 总数 + active thread 数。
  Future<({int messages, int activeThreads, int days})> recentStats({
    int days = 7,
  }) async {
    final since = DateTime.now().subtract(Duration(days: days));
    // completed 消息数
    final mCount = await (db.selectOnly(db.chatMessagesV2)
          ..addColumns([db.chatMessagesV2.id.count()])
          ..where(db.chatMessagesV2.status.equals('completed') &
              db.chatMessagesV2.createdAt.isBiggerThanValue(since)))
        .map((row) => row.read(db.chatMessagesV2.id.count()) ?? 0)
        .getSingle();
    // 活跃 thread 数：DISTINCT thread_id 在 since 之后有 message 的
    final rows = await db.customSelect(
      '''
      SELECT COUNT(DISTINCT thread_id) AS c
      FROM chat_messages_v2
      WHERE status = 'completed' AND created_at > ?
      ''',
      variables: [
        Variable.withInt(since.toUtc().millisecondsSinceEpoch ~/ 1000),
      ],
      readsFrom: {db.chatMessagesV2},
    ).get();
    final tCount = rows.isEmpty ? 0 : (rows.first.read<int?>('c') ?? 0);
    return (messages: mCount, activeThreads: tCount, days: days);
  }

  Future<void> archiveThread(String id) async {
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        archived: const Value(true),
        updatedAt: Value(DateTime.now()),
      ),
    );
  }

  Future<void> deleteThread(String id) async {
    await deleteThreads([id]);
  }

  /// 批量删除线程 —— 单事务级联清掉 blocks / messages / sessions / thread。
  /// 单条 deleteThread 走这里（[id] 列表长度 1），逻辑单一不漂移。空集合 noop。
  Future<void> deleteThreads(Iterable<String> ids) async {
    final list = ids.toList(growable: false);
    if (list.isEmpty) return;
    await db.transaction(() async {
      // 1. 找出这些 thread 下所有 message ids
      final msgIds = await (db.selectOnly(db.chatMessagesV2)
            ..addColumns([db.chatMessagesV2.id])
            ..where(db.chatMessagesV2.threadId.isIn(list)))
          .map((r) => r.read(db.chatMessagesV2.id)!)
          .get();
      // 2. 删 blocks（按 message_id 批量）
      if (msgIds.isNotEmpty) {
        await (db.delete(db.chatContentBlocks)
              ..where((b) => b.messageId.isIn(msgIds)))
            .go();
      }
      // 3. 删 messages
      await (db.delete(db.chatMessagesV2)
            ..where((m) => m.threadId.isIn(list)))
          .go();
      // 4. 删 sessions
      await (db.delete(db.chatSessions)..where((s) => s.threadId.isIn(list)))
          .go();
      // 5. 删 thread
      await (db.delete(db.chatThreadsV2)..where((t) => t.id.isIn(list))).go();
    });
  }

  // ── Messages + Blocks ────────────────────────────────────

  /// 看 thread 的所有消息（带 blocks），按 seq 升序。
  /// Drift v2 不直接支持 nested watch；这里用两条 query + 内存 join。
  /// blocks 表大改动时整 stream 会重发；chat 单会话规模不大可接受。
  //
  // (已删 listRecentTurns)Runtime v3 §8.2 翻案后,多轮历史不再由客户端组装
  // 带入 createSession —— brain 把对话轮落 chat.messages 并服务端组装(token
  // 预算 + 后续 compaction)。客户端本地 Drift 只负责渲染,不再切历史。

  Stream<List<Message>> watchMessages(String threadId) {
    final msgQ = db.select(db.chatMessagesV2)
      ..where((m) => m.threadId.equals(threadId))
      ..orderBy([(m) => OrderingTerm(expression: m.seq)]);
    final msgStream = msgQ.watch();

    final blockQ = db.select(db.chatContentBlocks).join([
      innerJoin(
        db.chatMessagesV2,
        db.chatMessagesV2.id.equalsExp(db.chatContentBlocks.messageId),
      ),
    ])
      ..where(db.chatMessagesV2.threadId.equals(threadId));
    final blockStream = blockQ.watch().map((rows) {
      final byMsg = <String, List<LocalChatContentBlock>>{};
      for (final r in rows) {
        final b = r.readTable(db.chatContentBlocks);
        byMsg.putIfAbsent(b.messageId, () => []).add(b);
      }
      // 按 blockIndex 排
      for (final list in byMsg.values) {
        list.sort((a, b) => a.blockIndex.compareTo(b.blockIndex));
      }
      return byMsg;
    });

    // 合并两流：message 列表 + blocks-by-msg 都到了再 emit
    return rxCombineLatest2(msgStream, blockStream, (msgs, blocksMap) {
      return msgs
          .map((m) => _messageFromRow(m,
              blocks: (blocksMap[m.id] ?? const [])
                  .map(_blockFromRow)
                  .toList()))
          .toList();
    });
  }

  Future<Message?> getMessage(String id) async {
    final mq = db.select(db.chatMessagesV2)..where((m) => m.id.equals(id));
    final mrow = await mq.getSingleOrNull();
    if (mrow == null) return null;
    final bq = db.select(db.chatContentBlocks)
      ..where((b) => b.messageId.equals(id))
      ..orderBy([(b) => OrderingTerm(expression: b.blockIndex)]);
    final brows = await bq.get();
    return _messageFromRow(mrow,
        blocks: brows.map(_blockFromRow).toList());
  }

  /// 在 thread 末尾追加一条 message（无 blocks）。返回 row。caller 用
  /// upsertBlock 增量加内容。seq 自动选 thread 当前 max+1。
  Future<Message> appendMessage({
    required String id,
    required String threadId,
    required MessageRole role,
    MessageStatus status = MessageStatus.streaming,
    String? sessionId,
    String? model,
  }) async {
    final now = DateTime.now();
    final maxSeq = await (db.selectOnly(db.chatMessagesV2)
          ..addColumns([db.chatMessagesV2.seq.max()])
          ..where(db.chatMessagesV2.threadId.equals(threadId)))
        .map((r) => r.read(db.chatMessagesV2.seq.max()) ?? 0)
        .getSingleOrNull();
    final seq = (maxSeq ?? 0) + 1;
    await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
          id: id,
          threadId: threadId,
          role: role.name,
          status: status.name,
          sessionId: Value(sessionId),
          model: Value(model),
          seq: seq,
          createdAt: now,
        ));
    // 顺路把 thread.updatedAt 推进
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(threadId)))
        .write(ChatThreadsV2Companion(updatedAt: Value(now)));
    return Message(
      id: id,
      threadId: threadId,
      role: role,
      status: status,
      sessionId: sessionId,
      model: model,
      seq: seq,
      createdAt: now,
    );
  }

  /// 增量追加 / 更新一条 block。R2 streaming 路径每条 SDK frame 调一次。
  /// 同一 (messageId, blockIndex) 已存在就 update，不存在就 insert。
  Future<void> upsertBlock(Block block, {required String messageId}) async {
    final companion = _blockToCompanion(block, messageId);
    await db.into(db.chatContentBlocks).insertOnConflictUpdate(companion);
  }

  /// 整批替换某 message 的所有 blocks（regenerate / 重建场景）。
  Future<void> replaceBlocks(String messageId, List<Block> blocks) async {
    await db.transaction(() async {
      await (db.delete(db.chatContentBlocks)
            ..where((b) => b.messageId.equals(messageId)))
          .go();
      for (final b in blocks) {
        await db.into(db.chatContentBlocks).insert(_blockToCompanion(b, messageId));
      }
    });
  }

  Future<void> finalizeMessage(
    String id, {
    required MessageStatus status,
    String? stopReason,
    int? inputTokens,
    int? outputTokens,
    String? errorMessage,
  }) async {
    await (db.update(db.chatMessagesV2)..where((m) => m.id.equals(id))).write(
      ChatMessagesV2Companion(
        status: Value(status.name),
        stopReason: Value(stopReason),
        inputTokens: Value(inputTokens),
        outputTokens: Value(outputTokens),
        errorMessage: Value(errorMessage),
        completedAt: Value(DateTime.now()),
      ),
    );
  }

  /// 跨会话搜索：在所有 thread 的 text block 里 LIKE 找 query。
  /// 返回 hit 列表，每条带 threadId / messageId / role / 关键 snippet（命中
  /// 句前后 30 字）。limit 默认 50，避免一次返一万条。
  ///
  /// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 跨会话搜索）。
  /// SQL LIKE 大小写不敏感由 SQLite 默认 NOCASE collation 提供（'lower'
  /// 转换在 chat_content_blocks 上没设；用 Dart 处理 lower-case 比较防
  /// CJK 异常）。
  Future<List<MessageSearchHit>> searchMessages({
    required String query,
    int limit = 50,
  }) async {
    final q = query.trim();
    if (q.isEmpty) return const [];
    final pattern = '%${_escapeLike(q)}%';
    // 取所有 text 块及其 message + thread 元数据，一次 query 解决。
    final rows = await db.customSelect(
      '''
      SELECT
        b.message_id  AS message_id,
        b.text_content AS text_content,
        m.thread_id   AS thread_id,
        m.role        AS role,
        m.seq         AS seq,
        m.created_at  AS created_at,
        t.title       AS thread_title
      FROM chat_content_blocks b
      JOIN chat_messages_v2 m ON m.id = b.message_id
      LEFT JOIN chat_threads_v2 t ON t.id = m.thread_id
      WHERE b.type = 'text' AND b.text_content LIKE ? ESCAPE '\\'
      ORDER BY m.created_at DESC
      LIMIT ?
      ''',
      variables: [Variable.withString(pattern), Variable.withInt(limit)],
      readsFrom: {db.chatContentBlocks, db.chatMessagesV2, db.chatThreadsV2},
    ).get();
    final lower = q.toLowerCase();
    return rows.map((r) {
      final text = r.read<String?>('text_content') ?? '';
      return MessageSearchHit(
        messageId: r.read<String>('message_id'),
        threadId: r.read<String>('thread_id'),
        threadTitle: r.read<String?>('thread_title') ?? '',
        role: MessageRole.fromName(r.read<String>('role')),
        seq: r.read<int>('seq'),
        createdAt: DateTime.fromMillisecondsSinceEpoch(
            (r.read<int>('created_at')) * 1000,
            isUtc: true),
        snippet: _snippet(text, lower),
      );
    }).toList(growable: false);
  }

  /// 转 LIKE 通配符。\ 作 ESCAPE，% 和 _ 都需要转义。
  static String _escapeLike(String s) {
    return s.replaceAll(r'\', r'\\').replaceAll('%', r'\%').replaceAll('_', r'\_');
  }

  /// 命中句前后 30 字 + 省略号；优先用 lower-case 找 offset。
  static String _snippet(String text, String lowerQuery) {
    final lower = text.toLowerCase();
    final i = lower.indexOf(lowerQuery);
    if (i < 0) {
      return text.length > 80 ? '${text.substring(0, 80)}…' : text;
    }
    final start = (i - 30).clamp(0, text.length);
    final end = (i + lowerQuery.length + 30).clamp(0, text.length);
    final pre = start > 0 ? '…' : '';
    final post = end < text.length ? '…' : '';
    return '$pre${text.substring(start, end)}$post'.replaceAll('\n', ' ');
  }

  // ── Thread export / import (JSON) ────────────────────────
  // 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 会话备份）。

  /// 把 thread + 所有 messages + blocks 序列化成 JSON 字符串。
  /// 业务过滤（completed only / 跳 toolResult）由 thread_export_json.dart 内做。
  Future<String> exportThreadJson(String threadId) async {
    final thread = await getThread(threadId);
    if (thread == null) {
      throw StateError('thread not found: $threadId');
    }
    final messages = await watchMessages(threadId).first;
    return exportThreadAsJson(thread: thread, messages: messages);
  }

  /// 批量导入 bulk JSON —— exportAllThreadsJson 的反向。
  /// 返回新建 thread 的 id 列表，按导入顺序排列。
  Future<List<String>> importAllThreadsJson(String jsonSource) async {
    final entries = parseBulkExportJson(jsonSource);
    final ids = <String>[];
    for (final e in entries) {
      // 单条复用 importThreadJson 的写入路径，避免逻辑重复。
      // 重新组单条 single export 让单条 import 路径吃。
      final singleJson = exportThreadAsJson(
        thread: e.thread,
        messages: e.messages,
      );
      final id = await importThreadJson(singleJson);
      ids.add(id);
    }
    return ids;
  }

  /// 批量导出全部（含 archived）—— 备份场景。
  Future<String> exportAllThreadsJson() async {
    // 一次性拿所有 thread；watchThreads 默认过滤 archived，这里直接 select。
    final rows = await db.select(db.chatThreadsV2).get();
    final entries = <({Thread thread, List<Message> messages})>[];
    for (final r in rows) {
      final t = _threadFromRow(r);
      final messages = await watchMessages(t.id).first;
      entries.add((thread: t, messages: messages));
    }
    return exportAllAsJson(entries: entries);
  }

  /// 解析 JSON 后写入新的 thread + messages + blocks。
  /// id 全部重新生成（防 import 同一文件多次时主键冲突 / 串本地数据）。
  /// 返回新生成的 thread id。
  Future<String> importThreadJson(String jsonSource) async {
    final parsed = parseThreadExportJson(jsonSource);
    const uuid = Uuid();
    final newThreadId = uuid.v4();
    final old = parsed.thread;
    final now = DateTime.now();
    return db.transaction(() async {
      // 新 thread —— 标题加 "（导入）" 后缀让用户区分。
      final title = old.title.isEmpty
          ? '（导入）'
          : '${old.title}（导入）';
      await db.into(db.chatThreadsV2).insert(
            ChatThreadsV2Companion.insert(
              id: newThreadId,
              mode: old.mode.name,
              title: Value(title),
              model: Value(old.model),
              systemPrompt: Value(old.systemPrompt),
              createdAt: now,
              updatedAt: now,
            ),
          );
      var seq = 0;
      for (final m in parsed.messages) {
        seq++;
        final newMsgId = uuid.v4();
        await db.into(db.chatMessagesV2).insert(
              ChatMessagesV2Companion.insert(
                id: newMsgId,
                threadId: newThreadId,
                role: m.role.name,
                status: 'completed',
                seq: seq,
                model: Value(m.model),
                inputTokens: Value(m.inputTokens),
                outputTokens: Value(m.outputTokens),
                createdAt: m.createdAt,
                completedAt: Value(m.completedAt ?? m.createdAt),
              ),
            );
        for (var i = 0; i < m.blocks.length; i++) {
          final b = m.blocks[i];
          await _insertBlockFromImported(b, newMsgId, i, now);
        }
      }
      return newThreadId;
    });
  }

  Future<void> _insertBlockFromImported(
      Block b, String messageId, int index, DateTime now) async {
    const uuid = Uuid();
    final id = uuid.v4();
    switch (b) {
      case TextBlock(:final text):
        await db.into(db.chatContentBlocks).insert(
              ChatContentBlocksCompanion.insert(
                id: id,
                messageId: messageId,
                blockIndex: index,
                type: 'text',
                textContent: Value(text),
                createdAt: now,
                updatedAt: now,
              ),
            );
      case ImageBlock(:final mimeType, :final data):
        await db.into(db.chatContentBlocks).insert(
              ChatContentBlocksCompanion.insert(
                id: id,
                messageId: messageId,
                blockIndex: index,
                type: 'image',
                imageMimeType: Value(mimeType),
                imageData: Value(data),
                createdAt: now,
                updatedAt: now,
              ),
            );
      case ToolUseBlock(:final toolUseId, :final toolName, :final input):
        // input map → JSON 串落库，跟 BiuSessionConnection 写法对齐。
        await db.into(db.chatContentBlocks).insert(
              ChatContentBlocksCompanion.insert(
                id: id,
                messageId: messageId,
                blockIndex: index,
                type: 'tool_use',
                toolUseId: Value(toolUseId),
                toolUseName: Value(toolName),
                toolUseInputJson:
                    Value(input == null ? null : encodeJsonMap(input)),
                createdAt: now,
                updatedAt: now,
              ),
            );
      case ToolResultBlock(
          :final toolResultId,
          :final isError,
          :final content
        ):
        await db.into(db.chatContentBlocks).insert(
              ChatContentBlocksCompanion.insert(
                id: id,
                messageId: messageId,
                blockIndex: index,
                type: 'tool_result',
                toolResultId: Value(toolResultId),
                toolResultIsError: Value(isError),
                toolResultContentJson: Value(content),
                createdAt: now,
                updatedAt: now,
              ),
            );
    }
  }

  /// 批量删除 message + 关联 blocks + reactions。多选删除路径用。
  /// 不删 sessions（session 跨多 message）；如果整 thread 都没了走 deleteThread。
  Future<void> deleteMessages(Iterable<String> messageIds) async {
    final ids = messageIds.toList();
    if (ids.isEmpty) return;
    await db.transaction(() async {
      await (db.delete(db.chatContentBlocks)
            ..where((b) => b.messageId.isIn(ids)))
          .go();
      await (db.delete(db.messageReactionsV2)
            ..where((r) => r.messageId.isIn(ids)))
          .go();
      await (db.delete(db.chatMessagesV2)..where((m) => m.id.isIn(ids))).go();
    });
  }

  // ── Reactions ────────────────────────────────────────────
  // 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P0-1。
  // kind 存任意字符串:emoji 反应存 emoji 字面量('👍' / '❤️' …),'star' 是收藏。
  // (messageId, kind) 唯一 → toggle = 有则删无则插,语义天然成立。
  // 历史 'like'/'dislike' 行残留:展示层过滤,不主动迁移(无同步、无消费者)。

  Stream<List<LocalMessageReactionV2>> watchReactionsForMessage(
      String messageId) {
    final q = db.select(db.messageReactionsV2)
      ..where((r) => r.messageId.equals(messageId));
    return q.watch();
  }

  Stream<List<LocalMessageReactionV2>> watchStarredMessages() {
    final q = db.select(db.messageReactionsV2)
      ..where((r) => r.kind.equals('star'))
      ..orderBy([
        (r) => OrderingTerm(expression: r.createdAt, mode: OrderingMode.desc),
      ]);
    return q.watch();
  }

  /// 收藏（star）消息列表，带 thread 标题 + 消息片段（首条 text block 前 80 字）。
  /// 跨所有 thread；按 starred_at desc 排。
  Stream<List<StarredMessageHit>> watchStarredMessageHits() {
    return db
        .customSelect(
      '''
      SELECT
        r.message_id  AS message_id,
        r.thread_id   AS thread_id,
        r.created_at  AS starred_at,
        m.role        AS role,
        t.title       AS thread_title,
        (SELECT b.text_content FROM chat_content_blocks b
          WHERE b.message_id = r.message_id AND b.type = 'text'
          ORDER BY b.block_index ASC LIMIT 1) AS first_text
      FROM message_reactions_v2 r
      LEFT JOIN chat_messages_v2 m ON m.id = r.message_id
      LEFT JOIN chat_threads_v2 t ON t.id = r.thread_id
      WHERE r.kind = 'star'
      ORDER BY r.created_at DESC
      ''',
      readsFrom: {
        db.messageReactionsV2,
        db.chatMessagesV2,
        db.chatThreadsV2,
        db.chatContentBlocks,
      },
    )
        .watch()
        .map((rows) {
      return rows.map((r) {
        final firstText = r.read<String?>('first_text') ?? '';
        final snippet =
            firstText.length > 80 ? '${firstText.substring(0, 80)}…' : firstText;
        return StarredMessageHit(
          messageId: r.read<String>('message_id'),
          threadId: r.read<String>('thread_id'),
          threadTitle: r.read<String?>('thread_title') ?? '',
          role: MessageRole.fromName(
              r.read<String?>('role') ?? 'assistant'),
          snippet: snippet.replaceAll('\n', ' '),
          starredAt: DateTime.fromMillisecondsSinceEpoch(
              (r.read<int>('starred_at')) * 1000,
              isUtc: true),
        );
      }).toList(growable: false);
    });
  }

  Future<void> toggleReaction({
    required String messageId,
    required String threadId,
    required String kind,
  }) async {
    final existing = await (db.select(db.messageReactionsV2)
          ..where((r) => r.messageId.equals(messageId) & r.kind.equals(kind)))
        .getSingleOrNull();
    if (existing != null) {
      await (db.delete(db.messageReactionsV2)
            ..where((r) => r.id.equals(existing.id)))
          .go();
      return;
    }
    await db.into(db.messageReactionsV2).insert(
          MessageReactionsV2Companion.insert(
            messageId: messageId,
            threadId: threadId,
            kind: kind,
            createdAt: DateTime.now(),
          ),
        );
  }

  Future<void> clearReactionsForMessage(String messageId) async {
    await (db.delete(db.messageReactionsV2)
          ..where((r) => r.messageId.equals(messageId)))
        .go();
  }

  // ── 下行同步（ChatSyncService）───────────────────────────
  //
  // 跨设备下行同步的写入原语。合并策略（哪些行能覆盖 / 哪些跳过）在
  // ChatSyncService 里；这里只做"知道表结构"的裸读写。
  //
  // 不变量（由调用方保证，这里不重复检查）：
  //   - 本地 pending / failed 消息绝不被服务端数据覆盖（service 先查再调）。
  //   - 本地有、服务端没有的 thread / message 不在这里删除。

  /// 列所有 thread（含 archived），一次性快照 —— 同步合并对照用。
  Future<List<Thread>> listAllThreads() async {
    final rows = await db.select(db.chatThreadsV2).get();
    return rows.map(_threadFromRow).toList();
  }

  /// watchMessages 的一次性快照版 —— 同步合并只读当前状态，不订阅流。
  Future<List<Message>> listMessagesOnce(String threadId) =>
      watchMessages(threadId).first;

  /// 每个 thread 的 message 数（一条 GROUP BY）—— 同步用来发现"本地从没
  /// 拉成功过"的 thread（本地 0 条但服务端 last_msg_preview 非空）。
  Future<Map<String, int>> messageCountsByThread() async {
    final rows = await db.customSelect(
      'SELECT thread_id, COUNT(*) AS c FROM chat_messages_v2 GROUP BY thread_id',
      readsFrom: {db.chatMessagesV2},
    ).get();
    return {
      for (final r in rows)
        r.read<String>('thread_id'): r.read<int>('c'),
    };
  }

  /// 每个已同步 thread 的「服务端 updated_at 微秒标记」—— 精确的增量比较
  /// 基准（updatedAt 列被 Drift 截断到秒，同秒多次服务端更新无法区分）。
  /// 只含 marker 非空的行；本机新建从未同步过的 thread 不在结果里。
  Future<Map<String, int>> remoteUpdatedMarkers() async {
    final rows = await db.customSelect(
      'SELECT id, remote_updated_at_us AS us FROM chat_threads_v2 '
      'WHERE remote_updated_at_us IS NOT NULL',
      readsFrom: {db.chatThreadsV2},
    ).get();
    return {
      for (final r in rows) r.read<String>('id'): r.read<int>('us'),
    };
  }

  /// 下行同步 upsert thread 元数据。返回是否真的写了（无变化时跳过，
  /// 避免 watchThreads 无效重发）。
  ///
  /// insert（新 thread）：本地专属字段给默认值（mode=chat 等）——服务端
  /// thread 没有 mode / environment / workdir 概念，真要用时用户在本机再切。
  ///
  /// update（已存在）：只覆盖服务端权威字段（title / pinned / archived /
  /// updatedAt + 非空的 model / systemPrompt / projectId），保留本地专属的
  /// mode / environmentId / poolTag / providerId / workdir / autoApprove /
  /// runtimeEnvMode / backend。服务端为 null 的字段不覆盖本地值
  /// （EnsureThread 只写 title/model，project_id / system_prompt 服务端
  /// 可能根本就不知道）。
  Future<bool> upsertThreadFromSync({
    required String id,
    required String title,
    String? model,
    String? systemPrompt,
    String? projectId,
    required bool pinned,
    required bool archived,
    required DateTime createdAt,
    required DateTime updatedAt,
    required int remoteUpdatedAtUs,
  }) async {
    final existing = await (db.select(db.chatThreadsV2)
          ..where((t) => t.id.equals(id)))
        .getSingleOrNull();
    if (existing == null) {
      await db.into(db.chatThreadsV2).insert(ChatThreadsV2Companion.insert(
            id: id,
            mode: 'chat',
            title: Value(title),
            model: Value(model),
            systemPrompt: Value(systemPrompt),
            projectId: Value(projectId),
            pinned: Value(pinned),
            archived: Value(archived),
            createdAt: createdAt,
            updatedAt: updatedAt,
            remoteUpdatedAtUs: Value(remoteUpdatedAtUs),
          ));
      return true;
    }
    // 服务端为空的字段以本地为准（不参与比较 = 不触发写入）。
    final nextTitle = title.isNotEmpty ? title : existing.title;
    final nextModel = model ?? existing.model;
    final nextPrompt = systemPrompt ?? existing.systemPrompt;
    final nextProject = projectId ?? existing.projectId;
    // 精确标记相等 = 服务端状态未变（updatedAt 列被 Drift 截断到秒，
    // 不能用于变更判定）。
    final unchanged = existing.title == nextTitle &&
        existing.pinned == pinned &&
        existing.archived == archived &&
        existing.model == nextModel &&
        existing.systemPrompt == nextPrompt &&
        existing.projectId == nextProject &&
        existing.remoteUpdatedAtUs == remoteUpdatedAtUs;
    if (unchanged) return false;
    await (db.update(db.chatThreadsV2)..where((t) => t.id.equals(id))).write(
      ChatThreadsV2Companion(
        title: Value(nextTitle),
        pinned: Value(pinned),
        archived: Value(archived),
        model: Value(nextModel),
        systemPrompt: Value(nextPrompt),
        projectId: Value(nextProject),
        updatedAt: Value(updatedAt),
        remoteUpdatedAtUs: Value(remoteUpdatedAtUs),
      ),
    );
    return true;
  }

  /// 下行同步 upsert 一条 message + 它的单个 text 内容块。返回是否真的
  /// 写了。block 形状照抄 BiuSessionConnection 的写法（'${messageId}_b0' /
  /// index 0 / closed），UI 渲染路径完全一致。
  ///
  /// 只用于"服务端 hydrated"的行：id = 服务端 message id，seq = 服务端
  /// position。调用方已排除本地 pending / failed 行，这里不做重复保护。
  Future<bool> upsertMessageFromSync({
    required String id,
    required String threadId,
    required MessageRole role,
    required MessageStatus status,
    required int seq,
    String? model,
    int? inputTokens,
    int? outputTokens,
    String? errorMessage,
    required DateTime createdAt,
    required String text,
  }) async {
    final isTerminal = status == MessageStatus.completed ||
        status == MessageStatus.failed ||
        status == MessageStatus.cancelled;
    final completedAt = isTerminal ? createdAt : null;
    final existing = await (db.select(db.chatMessagesV2)
          ..where((m) => m.id.equals(id)))
        .getSingleOrNull();
    var wrote = false;
    if (existing == null) {
      await db.into(db.chatMessagesV2).insert(ChatMessagesV2Companion.insert(
            id: id,
            threadId: threadId,
            role: role.name,
            status: status.name,
            model: Value(model),
            inputTokens: Value(inputTokens),
            outputTokens: Value(outputTokens),
            seq: seq,
            errorMessage: Value(errorMessage),
            createdAt: createdAt,
            completedAt: Value(completedAt),
          ));
      wrote = true;
    } else if (existing.status != status.name ||
        existing.model != model ||
        existing.inputTokens != inputTokens ||
        existing.outputTokens != outputTokens ||
        existing.errorMessage != errorMessage) {
      // 只更新服务端权威字段；seq / sessionId / createdAt 保持首拉时的值。
      await (db.update(db.chatMessagesV2)..where((m) => m.id.equals(id)))
          .write(ChatMessagesV2Companion(
        status: Value(status.name),
        model: Value(model),
        inputTokens: Value(inputTokens),
        outputTokens: Value(outputTokens),
        errorMessage: Value(errorMessage),
        completedAt: Value(completedAt),
      ));
      wrote = true;
    }
    // text block：内容没变就跳过（hydrated message 永远只有这一个块，
    // replaceBlocks 整批替换是安全且幂等的）。
    const blockIndex = 0;
    final blockId = '${id}_b$blockIndex';
    final existingBlock = await (db.select(db.chatContentBlocks)
          ..where((b) => b.id.equals(blockId)))
        .getSingleOrNull();
    if (existingBlock == null ||
        existingBlock.type != 'text' ||
        existingBlock.textContent != text) {
      await replaceBlocks(id, [
        TextBlock(
          id: blockId,
          index: blockIndex,
          state: BlockState.closed,
          text: text,
        ),
      ]);
      wrote = true;
    }
    return wrote;
  }

  // ── Sessions ─────────────────────────────────────────────

  /// 取 thread 当前活跃的 session（status=active）。
  Future<Session?> activeSession(String threadId) async {
    final q = db.select(db.chatSessions)
      ..where((s) =>
          s.threadId.equals(threadId) & s.status.equals('active'))
      ..orderBy([(s) => OrderingTerm(expression: s.createdAt, mode: OrderingMode.desc)])
      ..limit(1);
    final row = await q.getSingleOrNull();
    return row == null ? null : _sessionFromRow(row);
  }

  Future<void> persistSession(Session s) async {
    await db.into(db.chatSessions).insertOnConflictUpdate(
          ChatSessionsCompanion.insert(
            sessionId: s.sessionId,
            threadId: s.threadId,
            mode: s.mode.name,
            sessionToken: s.sessionToken,
            tokenExpiresAt: s.tokenExpiresAt,
            lastSeenSeq: Value(s.lastSeenSeq),
            status: s.status.name,
            createdAt: s.createdAt,
            closedAt: Value(s.closedAt),
          ),
        );
  }

  Future<void> updateLastSeenSeq(String sessionId, int seq) async {
    await (db.update(db.chatSessions)
          ..where((s) => s.sessionId.equals(sessionId)))
        .write(ChatSessionsCompanion(lastSeenSeq: Value(seq)));
  }

  Future<void> finalizeSession(String sessionId, {required SessionStatus status}) async {
    await (db.update(db.chatSessions)
          ..where((s) => s.sessionId.equals(sessionId)))
        .write(ChatSessionsCompanion(
      status: Value(status.name),
      closedAt: Value(DateTime.now()),
    ));
  }

  Future<void> updateSessionToken(
    String sessionId, {
    required String token,
    required DateTime expiresAt,
  }) async {
    await (db.update(db.chatSessions)
          ..where((s) => s.sessionId.equals(sessionId)))
        .write(ChatSessionsCompanion(
      sessionToken: Value(token),
      tokenExpiresAt: Value(expiresAt),
    ));
  }
}

// ── Row → domain mappers ───────────────────────────────────

Thread _threadFromRow(LocalChatThreadV2 r) {
  return Thread(
    id: r.id,
    title: r.title,
    mode: ThreadMode.fromName(r.mode),
    environmentId: r.environmentId,
    poolTag: r.poolTag,
    model: r.model,
    providerId: r.providerId,
    systemPrompt: r.systemPrompt,
    projectId: r.projectId,
    workdir: r.workdir,
    autoApprove: AutoApproveMode.fromName(r.autoApprove),
    runtimeEnvMode: r.runtimeEnvMode,
    backend: r.backend,
    pinned: r.pinned,
    archived: r.archived,
    createdAt: r.createdAt,
    updatedAt: r.updatedAt,
  );
}

Message _messageFromRow(LocalChatMessageV2 r, {List<Block> blocks = const []}) {
  return Message(
    id: r.id,
    threadId: r.threadId,
    role: MessageRole.fromName(r.role),
    status: MessageStatus.fromName(r.status),
    sessionId: r.sessionId,
    stopReason: r.stopReason,
    model: r.model,
    inputTokens: r.inputTokens,
    outputTokens: r.outputTokens,
    seq: r.seq,
    errorMessage: r.errorMessage,
    createdAt: r.createdAt,
    completedAt: r.completedAt,
    blocks: blocks,
  );
}

Block _blockFromRow(LocalChatContentBlock r) {
  final state = BlockState.fromName(r.state);
  switch (r.type) {
    case 'tool_use':
      return ToolUseBlock(
        id: r.id,
        index: r.blockIndex,
        state: state,
        toolUseId: r.toolUseId ?? '',
        toolName: r.toolUseName ?? '',
        input: decodeJsonMap(r.toolUseInputJson),
      );
    case 'tool_result':
      return ToolResultBlock(
        id: r.id,
        index: r.blockIndex,
        state: state,
        toolResultId: r.toolResultId ?? '',
        isError: r.toolResultIsError ?? false,
        content: r.toolResultContentJson ?? '',
      );
    case 'image':
      return ImageBlock(
        id: r.id,
        index: r.blockIndex,
        state: state,
        mimeType: r.imageMimeType ?? '',
        data: r.imageData ?? '',
      );
    case 'text':
    default:
      return TextBlock(
        id: r.id,
        index: r.blockIndex,
        state: state,
        text: r.textContent ?? '',
      );
  }
}

ChatContentBlocksCompanion _blockToCompanion(Block b, String messageId) {
  final base = ChatContentBlocksCompanion.insert(
    id: b.id,
    messageId: messageId,
    blockIndex: b.index,
    type: '', // 下面 switch 覆盖
    state: Value(b.state.name),
    createdAt: DateTime.now(),
    updatedAt: DateTime.now(),
  );
  switch (b) {
    case TextBlock(:final text):
      return base.copyWith(
        type: const Value('text'),
        textContent: Value(text),
      );
    case ToolUseBlock(:final toolUseId, :final toolName, :final input):
      return base.copyWith(
        type: const Value('tool_use'),
        toolUseId: Value(toolUseId),
        toolUseName: Value(toolName),
        toolUseInputJson: Value(encodeJsonMap(input)),
      );
    case ToolResultBlock(:final toolResultId, :final isError, :final content):
      return base.copyWith(
        type: const Value('tool_result'),
        toolResultId: Value(toolResultId),
        toolResultIsError: Value(isError),
        toolResultContentJson: Value(content),
      );
    case ImageBlock(:final mimeType, :final data):
      return base.copyWith(
        type: const Value('image'),
        imageMimeType: Value(mimeType),
        imageData: Value(data),
      );
  }
}

Session _sessionFromRow(LocalChatSession r) {
  return Session(
    sessionId: r.sessionId,
    threadId: r.threadId,
    mode: ThreadMode.fromName(r.mode),
    sessionToken: r.sessionToken,
    tokenExpiresAt: r.tokenExpiresAt,
    lastSeenSeq: r.lastSeenSeq,
    status: SessionStatus.fromName(r.status),
    createdAt: r.createdAt,
    closedAt: r.closedAt,
  );
}

// ── Reactive helper ─────────────────────────────────────────

/// rxCombineLatest2 —— 极简 combineLatest 实现；不引 rxdart 让 dep 干净。
/// 行为：只有当两个流都至少 emit 过一次后才发；之后任一更新都重新 emit。
Stream<R> rxCombineLatest2<A, B, R>(
  Stream<A> a,
  Stream<B> b,
  R Function(A, B) combine,
) async* {
  A? lastA;
  B? lastB;
  var hasA = false;
  var hasB = false;
  final controller = StreamController<R>();
  final sa = a.listen((v) {
    lastA = v;
    hasA = true;
    if (hasA && hasB) controller.add(combine(lastA as A, lastB as B));
  }, onError: controller.addError);
  final sb = b.listen((v) {
    lastB = v;
    hasB = true;
    if (hasA && hasB) controller.add(combine(lastA as A, lastB as B));
  }, onError: controller.addError);
  controller.onCancel = () async {
    await sa.cancel();
    await sb.cancel();
  };
  yield* controller.stream;
}
