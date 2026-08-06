// ChatSyncService —— 聊天跨设备**下行**同步合并引擎。
//
// 目的：让本设备能看到其他设备产生的聊天（手机看 PC）。只做下行：
// 发送路径（BiuSessionConnection）不动，本服务只把 brain 的 chat.threads /
// chat.messages 灌进本地 Drift —— threadsProvider / messagesProvider 都
// watch Drift，UI 自动更新，无需改 UI。
//
// 对接的服务端 API（现成，不依赖新参数）：
//   GET /v1/threads                 cursor 分页（before + next_cursor）
//   GET /v1/threads/{id}            单条（realtime 增量 / 新 thread 用）
//   GET /v1/threads/{id}/messages   position 游标分页（after，升序）
//
// 冲突不变量：
//   - 服务端是权威，但只覆盖「已在服务端存在」的数据；
//   - 本地有、服务端没有的 thread / message 一律保留（可能是本地新建
//     未发消息的会话）；
//   - 本地 pending / failed 消息绝不被服务端数据覆盖（未上行完成）。
//
// 去重（同一条逻辑消息本地已发、服务端也有，id 却不同 —— 本地 id 是
// 客户端 uuid，服务端 id 是 brain 落库时新生成的 uuid）：
//   1. id 相同 → 已 hydrated，按服务端内容更新（pending/failed 除外）；
//   2. client_id 会话关联 —— 服务端 client_id = "<session_id>:user" /
//      "<session_id>:assistant"（agentplane router.go / transcript.go），
//      本地 assistant 消息带 sessionId，user 消息是它的前一条（seq-1）
//      或 multi-turn 时自己也带 sessionId；
//   3. 内容兜底 —— 同 role + 同文本的第一条未匹配本地消息（按 seq 序，
//      重复发同文也能逐条对上）。
//
// 幂等：重复执行结果一致 —— 已 hydrated 的行内容没变就不写（repo 层
// 比较后跳过），已匹配的行永远跳过。
//
// 关于「拉全量 vs after=本地最大 position」：本服务对每个**有变化**的
// thread 拉全量消息（after=0 分页翻完）。原因：本地 seq 会被 pending/
// failed 但未上行的消息消耗，导致本地 max seq 跑在服务端 position 前面，
// 用它当 after 会漏拉。全量 + 去重保证正确，chat 规模的 thread 成本可忽略。
// 是否拉取的判定：新 thread / 本地 remoteUpdatedAtUs ≠ 服务端 updated_at
// 的微秒整数（精确比较 —— Drift 把 DateTime 截断到秒，updatedAt 列比较
// 会漏掉同一秒内的 user→assistant 双 bump；trigger AFTER INSERT OR
// UPDATE 让 threads.updated_at 始终跟随消息活动）/ 本地 0 条消息但服务
// 端 last_msg_preview 非空（从没拉成功过）。sync_enabled=false 的
// 「仅本机」隐私会话整只跳过。
//
// 错误处理：threads 列表拉取失败整体抛（下个触发点重试）；单 thread
// 失败记录进 ChatSyncResult.errors 后继续，不阻塞其他 thread。

import 'package:logging/logging.dart';

import '../../../data/api/_http_helpers.dart' show apiRequest, ApiError;
import '../../../data/api/chat_client.dart' show ChatThread, ChatMessage;
import '../domain/chat_models.dart';
import 'chat_repo.dart';

final _log = Logger('biumind.chat.sync');

/// 一次 syncThreads 的简要统计 —— 日志 + 单测断言用。
class ChatSyncResult {
  int threadsFetched = 0; // 服务端看到的 thread 数
  int threadsUpserted = 0; // 本地新建或元数据被服务端覆盖的 thread 数
  int threadsSkipped = 0; // 已是最新、未拉消息的 thread 数
  int messagesFetched = 0; // 服务端看到的 message 数
  int messagesWritten = 0; // 实际写入本地的 message 数（去重/无变化不计）
  final List<String> errors = []; // 单 thread 级失败（不阻塞整体）

  bool get hasErrors => errors.isNotEmpty;

  @override
  String toString() =>
      'ChatSyncResult('
      'threads=$threadsFetched fetched/$threadsUpserted upserted/'
      '$threadsSkipped skipped, '
      'messages=$messagesFetched fetched/$messagesWritten written, '
      'errors=${errors.length})';
}

class ChatSyncService {
  ChatSyncService({
    required this.repo,
    required this.baseUrl,
    required this.tokenProvider,
    this.threadPageSize = 200, // 服务端上限 200（store.ListThreads clamp）
    this.messagePageSize = 100,
  });

  final ChatRepo repo;

  /// brain HTTP base（单 origin，site nginx 按 /v1/* 反代）。末尾不带 `/`。
  final String baseUrl;

  /// 每次请求拿最新 access_token（token 轮换后不留陈 token）。
  final Future<String?> Function() tokenProvider;

  final int threadPageSize;
  final int messagePageSize;

  // ── 全量 hydrate ──────────────────────────────────────────

  /// 拉全量 threads 合并进 Drift。未登录（无 token）退化为 noop。
  /// threads 列表本身的网络错误会 rethrow —— 调用方（manager）记录后
  /// 等下个触发点整体重试。
  Future<ChatSyncResult> syncThreads() async {
    final result = ChatSyncResult();
    final tok = await tokenProvider();
    if (tok == null || tok.isEmpty) {
      _log.fine('syncThreads: no token, skip');
      return result;
    }
    final remote = await _fetchAllThreads();
    result.threadsFetched = remote.length;

    final locals = {for (final t in await repo.listAllThreads()) t.id: t};
    final counts = await repo.messageCountsByThread();
    final markers = await repo.remoteUpdatedMarkers();

    for (final rt in remote) {
      // sync_enabled=false 是「仅本机」隐私开关：服务端不发事件（事件侧
      // 已 guard），下行拉取同样跳过 —— 否则关掉同步的会话会被全量
      // hydrate 灌进其他设备，隐私承诺被击穿。
      if (!rt.syncEnabled) {
        result.threadsSkipped++;
        continue;
      }
      try {
        await _syncOneThread(
          rt,
          locals[rt.id],
          counts[rt.id] ?? 0,
          markers[rt.id],
          result,
        );
      } catch (e) {
        // 单 thread 失败不阻塞其他 thread —— 记录后继续。
        result.errors.add('thread ${rt.id}: $e');
        _log.warning('sync thread ${rt.id} failed: $e');
      }
    }
    _log.info('syncThreads done: $result');
    return result;
  }

  /// 单 thread 增量入口 —— realtime 事件（chat.message_created /
  /// chat.thread_updated）触发。服务端已删（404）时按「本地保留」不动。
  Future<void> syncThread(String threadId) async {
    final rt = await _fetchThread(threadId);
    if (rt == null) return; // 404 —— 服务端没有，本地数据保留不动
    if (!rt.syncEnabled) return; // 「仅本机」隐私会话不下行（见 syncThreads）
    // 先拉消息再写 thread 元数据：消息拉取失败时 thread.updatedAt 保持
    // 旧值，下次 syncThreads 仍判定「服务端更新」自动重试。
    await _pullMessages(rt.id);
    await repo.upsertThreadFromSync(
      id: rt.id,
      title: rt.title,
      model: rt.model,
      systemPrompt: rt.systemPrompt,
      projectId: rt.projectId,
      pinned: rt.pinned,
      archived: rt.archived,
      createdAt: rt.createdAt,
      updatedAt: rt.updatedAt,
      remoteUpdatedAtUs: rt.updatedAt.toUtc().microsecondsSinceEpoch,
    );
  }

  // ── 单 thread 合并 ────────────────────────────────────────

  Future<void> _syncOneThread(
    ChatThread rt,
    Thread? local,
    int localMessageCount,
    int? remoteMarkerUs,
    ChatSyncResult result,
  ) async {
    final remoteUs = rt.updatedAt.toUtc().microsecondsSinceEpoch;
    final isNew = local == null;
    // 本地从未拉成功过的判定：服务端有预览但本地 0 条消息。
    final neverHydrated =
        localMessageCount == 0 && rt.lastMsgPreview.isNotEmpty;
    // 精确比较：marker 是上次同步时原样存下的服务端 updated_at 微秒整数
    // （Drift 把 DateTime 截断到秒，updatedAt 列比较会漏掉同一秒内的
    // user→assistant 双 bump）。不一致 = 服务端有新状态，拉。
    final needPull = isNew || neverHydrated || remoteMarkerUs != remoteUs;
    if (needPull) {
      final stats = await _pullMessages(rt.id);
      result.messagesFetched += stats.fetched;
      result.messagesWritten += stats.written;
    } else {
      result.threadsSkipped++;
    }
    // 先拉消息再 upsert 元数据（见 syncThread 注释 —— 失败自动重试语义）。
    final changed = await repo.upsertThreadFromSync(
      id: rt.id,
      title: rt.title,
      model: rt.model,
      systemPrompt: rt.systemPrompt,
      projectId: rt.projectId,
      pinned: rt.pinned,
      archived: rt.archived,
      createdAt: rt.createdAt,
      updatedAt: rt.updatedAt,
      remoteUpdatedAtUs: remoteUs,
    );
    if (changed) result.threadsUpserted++;
  }

  // ── 消息合并 ──────────────────────────────────────────────

  /// 拉 thread 全量消息并合并。返回 (fetched, written) 统计。
  Future<({int fetched, int written})> _pullMessages(String threadId) async {
    final remote = <ChatMessage>[];
    var after = 0;
    while (true) {
      final page = await _fetchMessages(threadId, after: after);
      if (page.isEmpty) break;
      remote.addAll(page);
      if (page.length < messagePageSize) break;
      after = page.last.position;
    }
    if (remote.isEmpty) return (fetched: 0, written: 0);

    final locals = await repo.listMessagesOnce(threadId);
    final byId = {for (final l in locals) l.id: l};
    final matched = <String>{}; // 已被去重消费掉的本地 message id
    var written = 0;

    for (final rm in remote) {
      // 服务端 mid-stream 占位（pending/processing/streaming）不下行 ——
      // terminal 后（同 tx 事件 + trigger bump updated_at）自然会拉到，
      // 避免本地出现永远没人喂帧的 streaming 消息。
      final status = _mapStatus(rm.status);
      if (status == MessageStatus.pending ||
          status == MessageStatus.streaming) {
        continue;
      }
      final text = _extractText(rm);

      // 1) id 相同 → 已 hydrated 的行，按服务端内容更新；
      //    本地 pending / failed 绝不被覆盖（防御：理论上本地那些行 id 是
      //    客户端 uuid，撞不上服务端 id，这里兜底护住不变量）。
      final localById = byId[rm.id];
      if (localById != null) {
        matched.add(rm.id);
        if (localById.status == MessageStatus.pending ||
            localById.status == MessageStatus.failed) {
          continue;
        }
        final changed = await repo.upsertMessageFromSync(
          id: rm.id,
          threadId: threadId,
          role: _mapRole(rm.role),
          status: status,
          seq: rm.position,
          model: rm.model,
          inputTokens: rm.promptTokens,
          outputTokens: rm.completionTokens,
          errorMessage: rm.errorMsg,
          createdAt: rm.createdAt,
          text: text,
        );
        if (changed) written++;
        continue;
      }

      // 2)+3) 本地已发过的同一逻辑消息（session 关联 / 内容兜底）→ 跳过。
      final dup = _findLocalCopy(rm, locals, matched, text);
      if (dup != null) {
        matched.add(dup.id);
        continue;
      }

      // 4) 真·新消息 → 灌入（id = 服务端 id，seq = 服务端 position）。
      final changed = await repo.upsertMessageFromSync(
        id: rm.id,
        threadId: threadId,
        role: _mapRole(rm.role),
        status: status,
        seq: rm.position,
        model: rm.model,
        inputTokens: rm.promptTokens,
        outputTokens: rm.completionTokens,
        errorMessage: rm.errorMsg,
        createdAt: rm.createdAt,
        text: text,
      );
      if (changed) written++;
    }
    return (fetched: remote.length, written: written);
  }

  /// 在本地消息里找服务端消息 rm 的「本机已发副本」。
  /// [matched] 记录已被消费的本地行，保证重复内容能逐条一一对应。
  Message? _findLocalCopy(
    ChatMessage rm,
    List<Message> locals,
    Set<String> matched,
    String text,
  ) {
    // client_id 会话关联（精确）：服务端 client_id = "<session_id>:user" /
    // "<session_id>:assistant"（router.go persistUserAndAssemble /
    // transcript.go finish 的幂等键）。
    final cid = rm.clientId;
    if (cid != null) {
      final sep = cid.lastIndexOf(':');
      if (sep > 0) {
        final sid = cid.substring(0, sep);
        final kind = cid.substring(sep + 1);
        bool isFree(Message l) => !matched.contains(l.id);
        if (kind == 'assistant') {
          for (final l in locals) {
            if (isFree(l) &&
                l.sessionId == sid &&
                l.role == MessageRole.assistant) {
              return l;
            }
          }
        } else if (kind == 'user') {
          // 首 turn：open() 写的 user 消息不带 sessionId —— 找同 session
          // assistant 的前一条（seq-1）user 消息。
          for (final a in locals) {
            if (a.sessionId == sid && a.role == MessageRole.assistant) {
              for (final l in locals) {
                if (isFree(l) &&
                    l.role == MessageRole.user &&
                    l.seq == a.seq - 1) {
                  return l;
                }
              }
            }
          }
          // multi-turn：sendUserMessage 写的 user 消息自己带 sessionId。
          for (final l in locals) {
            if (isFree(l) && l.sessionId == sid && l.role == MessageRole.user) {
              return l;
            }
          }
        }
      }
    }
    // 内容兜底：同 role + 同文本的第一条未匹配本地消息。
    final role = _mapRole(rm.role);
    for (final l in locals) {
      if (matched.contains(l.id)) continue;
      if (l.role != role) continue;
      if (l.assembledText != text) continue;
      return l;
    }
    return null;
  }

  // ── 服务端 → 本地映射 ─────────────────────────────────────

  /// brain chat.messages status（store.go 六态）→ 本地 MessageStatus。
  MessageStatus _mapStatus(String s) {
    switch (s) {
      case 'success':
        return MessageStatus.completed;
      case 'error':
        return MessageStatus.failed;
      case 'pending':
        return MessageStatus.pending;
      case 'processing':
      case 'streaming':
        return MessageStatus.streaming;
      case 'paused': // 服务端「用户取消/暂停」→ 本地 cancelled 灰态
        return MessageStatus.cancelled;
      default:
        return MessageStatus.completed;
    }
  }

  MessageRole _mapRole(String r) {
    switch (r) {
      case 'assistant':
        return MessageRole.assistant;
      case 'system':
        return MessageRole.system;
      case 'tool':
      case 'tool_result':
        return MessageRole.toolResult;
      case 'user':
      default:
        return MessageRole.user;
    }
  }

  /// 服务端消息 → 单个 text 块的文本。content 优先；空则兜底 parts
  /// （Anthropic content blocks 形态 [{type:'text',text:...}]）。
  String _extractText(ChatMessage m) {
    if (m.content.isNotEmpty) return m.content;
    final sb = StringBuffer();
    for (final p in m.parts) {
      if (p is Map && p['type'] == 'text' && p['text'] is String) {
        if (sb.isNotEmpty) sb.write('\n');
        sb.write(p['text'] as String);
      }
    }
    return sb.toString();
  }

  // ── HTTP ──────────────────────────────────────────────────

  Future<Map<String, dynamic>> _get(
    String path,
    Map<String, String> queryParams,
  ) async {
    final tok = await tokenProvider();
    return apiRequest(
      method: 'GET',
      url: Uri.parse('$baseUrl$path').replace(queryParameters: queryParams),
      bearerToken: tok,
    );
  }

  Future<List<ChatThread>> _fetchAllThreads() async {
    final out = <ChatThread>[];
    String? before;
    while (true) {
      final qp = <String, String>{'limit': '$threadPageSize'};
      if (before != null) qp['before'] = before;
      final raw = await _get('/v1/threads', qp);
      final page = (raw['threads'] as List? ?? const [])
          .cast<Map<String, dynamic>>()
          .map(ChatThread.fromJson)
          .toList();
      out.addAll(page);
      // next_cursor 是现服务端的分页游标；老部署没有时退回用本页最老
      // updated_at（服务端 cursor 语义本来就是它）。
      var next = raw['next_cursor'] as String? ?? '';
      if (next.isEmpty && page.length >= threadPageSize) {
        next = page.last.updatedAt.toUtc().toIso8601String();
      }
      if (page.length < threadPageSize || next.isEmpty) break;
      before = next;
    }
    return out;
  }

  /// 单条 thread；404（服务端没有 / 非 owner）返 null。
  Future<ChatThread?> _fetchThread(String id) async {
    try {
      final raw = await _get('/v1/threads/$id', const {});
      return ChatThread.fromJson(raw);
    } on ApiError catch (e) {
      if (e.status == 404) return null;
      rethrow;
    }
  }

  Future<List<ChatMessage>> _fetchMessages(
    String threadId, {
    required int after,
  }) async {
    final raw = await _get('/v1/threads/$threadId/messages', {
      'limit': '$messagePageSize',
      'after': '$after',
    });
    return (raw['messages'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ChatMessage.fromJson)
        .toList();
  }
}
