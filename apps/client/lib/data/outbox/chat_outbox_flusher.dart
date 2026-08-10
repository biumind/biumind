// ChatOutboxFlusher —— chat 上行写盒冲刷器（P1.3，设计
// docs/BiuMind-Local-Data-Isolation-Design.md §4）。
//
// 照搬 WikiOutboxFlusher 范式（wiki_outbox_flusher.dart），覆盖 chat 的三
// 种会话级上行 op：delete_thread / archive_thread / rename_thread。
//
// 策略：
//   * flushOnce() 按 id 序（FIFO）应用所有 nextAttemptAt <= now 的 op，
//     成功删行，失败 attempts+1 并按指数退避重排（1s, 2s, 4s … cap 5min）。
//   * start() 起 5s 周期定时器；kick() 给入队方（ChatThreadOps）立即触发
//     一次尝试，与在途 flush 合并。
//   * HTTP 404 → 丢 op：目标 thread 服务端已不存在（他端先删了），删除/
//     归档/重命名都算幂等收敛，重试无意义。
//   * 其他 ApiError / 网络错误 → 退避重试（临时故障不能让上行静默丢失 —
//     这正是 P1.3 要修的「best-effort 一发即丢」）。
//   * 无 token（登出过渡态）→ 整轮跳过。登出时 ChatSyncManager 会停掉
//     flusher；这里是双保险 —— 表不清（scope 隔离），切回账号续传。
//
// scope 隔离：repo 构造绑定 ownerKey scope，due/delete/bump 查询全部只落
// 当前 scope —— 多账号共存时只 flush 当前登录账号的 op。

import 'dart:async';
import 'dart:convert';

import 'package:logging/logging.dart';

import '../../features/chat/data/chat_repo.dart';
import '../api/_http_helpers.dart' show apiRequest, ApiError;
import '../local/db.dart' show ChatOutboxEntry;

class ChatOutboxFlusher {
  ChatOutboxFlusher({
    required this.repo,
    required this.baseUrl,
    required this.tokenProvider,
    this.interval = const Duration(seconds: 5),
    DateTime Function()? clock,
    Logger? log,
  })  : _now = clock ?? DateTime.now,
        _log = log ?? Logger('ChatOutboxFlusher');

  /// scope 绑定的 repo —— 全部 outbox 查询只命中当前登录 scope。
  final ChatRepo repo;

  /// brain HTTP base（单 origin）。末尾不带 `/`。
  final String baseUrl;

  /// 每次请求拿最新 access_token（token 轮换后不留陈 token）。
  final Future<String?> Function() tokenProvider;

  final Duration interval;
  final DateTime Function() _now;
  final Logger _log;

  Timer? _timer;
  bool _flushing = false;

  void start() {
    _timer ??= Timer.periodic(interval, (_) => flushOnce());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  void dispose() => stop();

  /// Trigger a flush right now (e.g. right after ChatThreadOps enqueues).
  /// Coalesces with any in-flight flush.
  Future<void> kick() => flushOnce();

  Future<void> flushOnce() async {
    if (_flushing) return;
    // 无 token = 登出过渡态 —— 整轮跳过（op 留在表里，切回账号续传）。
    final tok = await tokenProvider();
    if (tok == null || tok.isEmpty) return;
    _flushing = true;
    try {
      final due = await repo.dueChatOutbox(now: _now().toUtc());
      for (final entry in due) {
        try {
          await _applyOne(entry);
          await repo.deleteChatOutboxEntry(entry.id);
        } on ApiError catch (e) {
          if (e.status == 404) {
            // 目标已不存在 —— 幂等收敛，丢 op。
            _log.fine(
              'drop chat outbox ${entry.id} (${entry.op} ${entry.threadId}): 404',
            );
            await repo.deleteChatOutboxEntry(entry.id);
          } else {
            await _backoff(entry, '${e.status}: ${e.body}');
          }
        } catch (e) {
          await _backoff(entry, e.toString());
        }
      }
    } finally {
      _flushing = false;
    }
  }

  Future<void> _applyOne(ChatOutboxEntry entry) async {
    final tok = await tokenProvider();
    if (tok == null || tok.isEmpty) {
      throw StateError('no token'); // 走退避分支，下轮再试
    }
    Future<Map<String, dynamic>> req(
      String method,
      String path, {
      Object? body,
      bool expectNoBody = false,
    }) =>
        apiRequest(
          method: method,
          url: Uri.parse('$baseUrl$path'),
          bearerToken: tok,
          body: body,
          expectNoBody: expectNoBody,
        );

    final payload = entry.payloadJson.isEmpty
        ? const <String, dynamic>{}
        : jsonDecode(entry.payloadJson) as Map<String, dynamic>;
    switch (entry.op) {
      case ChatRepo.outboxOpDeleteThread:
        await req('DELETE', '/v1/threads/${entry.threadId}',
            expectNoBody: true);
      case ChatRepo.outboxOpArchiveThread:
        await req('PATCH', '/v1/threads/${entry.threadId}', body: {
          'archived': payload['archived'] ?? true,
        });
      case ChatRepo.outboxOpRenameThread:
        await req('PATCH', '/v1/threads/${entry.threadId}', body: {
          'title': payload['title'] ?? '',
        });
      default:
        // 未知 op 重试一万年也不会成功 —— 丢掉防毒丸卡死队列。
        _log.warning('drop chat outbox ${entry.id}: unknown op ${entry.op}');
        await repo.deleteChatOutboxEntry(entry.id);
    }
  }

  Future<void> _backoff(ChatOutboxEntry entry, String error) async {
    final attempts = entry.attempts + 1;
    // 1s, 2s, 4s, 8s … capped at 5 min.
    final secs = 1 << (attempts.clamp(0, 8));
    final next = _now().toUtc().add(Duration(seconds: secs.clamp(1, 300)));
    await repo.bumpChatOutboxFailure(entry.id, error, next);
    _log.fine('chat outbox ${entry.id} retry in ${secs}s: $error');
  }
}
