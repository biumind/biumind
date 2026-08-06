// NoteOutboxFlusher — drains the NoteOutbox table against the live API.
//
// 镜像 WikiOutboxFlusher 的策略（O2 先复制后收敛）：
//   * `flushOnce()` 按 id 顺序处理 `nextAttemptAt <= now` 的条目：成功删行，
//     失败 attempts+1 指数退避重排；
//   * `start()` 起一个周期 timer（默认 5s），`kick()` 供 repository 入队后
//     立即触发一次；
//   * create_note 的 payload 带客户端预生成的 uuid 时透传给服务端
//     （幂等重放），flush 前后 id 不变，renameNoteId 退化为 no-op；
//     notebook/tag 及历史 local- 占位笔记仍走「服务端分配 id + rekey」：
//     create_* op 成功后把本地 'local-<uuid>' 占位 id rekey 成服务端
//     uuid（数据表 + 待冲刷 op 的 entityId/notebookId 列 + payloadJson
//     里的引用一起改）；
//   * HTTP 409（If-Match version 冲突）→ 丢弃该 op 并发 NoteOutboxConflict
//     到 [conflicts] 广播流 —— 服务端是真相源，禁止 latest-wins 自动覆盖
//     （设计 §4 D4），UI 引导用户用 repository.saveAsCopy 做裁决。
//
// 与 wiki 版的差异：去掉 projectId/pageId 列（笔记无层级），换 nullable
// notebookId；op 集合换成笔记域；update_note 成功后把服务端响应（新
// version + notebook_id 可能被 restore 置根）回填本地行。
//
// [onFlushSuccess] —— 成功冲刷至少一条 op 后回调一次。providers 把它接到
// NotesSyncPoller.kick()，让本机变更 flush 完立刻 pull 一轮 changes，
// 把事件流里的回声/他端变更及时落进 Drift。

import 'dart:async';
import 'dart:convert';

import 'package:logging/logging.dart';

import '../api/notes_client.dart' as api;
import '../local/db.dart';
import '../local/notes_dao.dart';

class NoteOutboxConflict {
  final String op;
  final String entityId;
  final int? baseVersion;
  final String body;
  const NoteOutboxConflict({
    required this.op,
    required this.entityId,
    required this.baseVersion,
    required this.body,
  });

  @override
  String toString() =>
      'NoteOutboxConflict(op=$op, entity=$entityId, base=$baseVersion)';
}

class NoteOutboxFlusher {
  NoteOutboxFlusher({
    required this.dao,
    required this.client,
    this.interval = const Duration(seconds: 5),
    DateTime Function()? clock,
    Logger? log,
  })  : _now = clock ?? DateTime.now,
        _log = log ?? Logger('NoteOutboxFlusher');

  final NotesDao dao;
  final api.NotesClient client;
  final Duration interval;
  final DateTime Function() _now;
  final Logger _log;

  Timer? _timer;
  bool _flushing = false;
  final _conflicts = StreamController<NoteOutboxConflict>.broadcast();

  /// 成功冲刷 ≥1 条 op 后回调（providers 接到 sync poller 的 kick）。
  Future<void> Function()? onFlushSuccess;

  /// Conflict stream — UI can subscribe to surface "this edit was based on a
  /// stale version" prompts without coupling to the flusher's internals.
  Stream<NoteOutboxConflict> get conflicts => _conflicts.stream;

  void start() {
    _timer ??= Timer.periodic(interval, (_) => flushOnce());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  Future<void> dispose() async {
    stop();
    await _conflicts.close();
  }

  /// Trigger a flush right now (e.g. after the repository enqueues a write).
  /// Coalesces with any in-flight flush.
  Future<void> kick() => flushOnce();

  Future<void> flushOnce() async {
    if (_flushing) return;
    _flushing = true;
    var flushedAny = false;
    try {
      final due = await dao.dueOutbox(now: _now().toUtc());
      for (final entry in due) {
        try {
          // apply 前重读：同批前面的 create_* 成功后会 rekey，due 快照里
          // 的 entityId/payloadJson 可能已是旧值（local- 占位直接上送
          // 会 4xx 丢 op）。
          final fresh = await dao.outboxById(entry.id);
          if (fresh == null) continue;
          await _applyOne(fresh);
          await dao.deleteOutbox(entry.id);
          flushedAny = true;
        } on api.NotesApiError catch (e) {
          if (e.isVersionConflict) {
            _conflicts.add(NoteOutboxConflict(
              op: entry.op,
              entityId: entry.entityId,
              baseVersion: entry.baseVersion,
              body: e.body,
            ));
            // Drop the op — the user's pending edit was based on stale state.
            // Server is source of truth; the next pull will bring the
            // current version into Drift.
            await dao.deleteOutbox(entry.id);
          } else if (e.status >= 500 || e.status == 429) {
            await _backoff(entry, '${e.status}: ${e.body}');
          } else {
            // 4xx other than 409 → drop and log; retrying won't help.
            _log.warning('drop outbox ${entry.id} (${entry.op}): $e');
            await dao.deleteOutbox(entry.id);
          }
        } catch (e) {
          await _backoff(entry, e.toString());
        }
      }
    } finally {
      _flushing = false;
    }
    if (flushedAny) {
      try {
        await onFlushSuccess?.call();
      } catch (e) {
        _log.fine('onFlushSuccess callback failed: $e');
      }
    }
  }

  Future<void> _applyOne(NoteOutboxEntry entry) async {
    final payload = jsonDecode(entry.payloadJson) as Map<String, dynamic>;
    switch (entry.op) {
      case 'create_notebook':
        final created = await client.createNotebook(
          payload['name'] as String,
          position: (payload['position'] as num?)?.toDouble(),
        );
        await dao.renameNotebookId(entry.entityId, created.id);
        await dao.rekeyOutbox(
            oldEntityId: entry.entityId, newEntityId: created.id);
      case 'update_notebook':
        await client.updateNotebook(
          entry.entityId,
          name: payload['name'] as String?,
          position: (payload['position'] as num?)?.toDouble(),
        );
      case 'delete_notebook':
        await client.deleteNotebook(entry.entityId);
      case 'create_note':
        final created = await client.createNote(
          // 新创建路径 payload 带客户端 uuid（服务端幂等重放），created.id
          // 与 entityId 相同，下面的 renameNoteId 是 no-op；历史 local-
          // 占位行（恢复逻辑补的 op）不带 id，走服务端分配 + rekey。
          id: payload['id'] as String?,
          notebookId: payload['notebook_id'] as String?,
          title: payload['title'] as String? ?? '',
          contentMd: payload['content_md'] as String? ?? '',
          isTodo: payload['is_todo'] as bool? ?? false,
          position: (payload['position'] as num?)?.toDouble(),
        );
        await dao.renameNoteId(entry.entityId, created.id);
        await dao.rekeyOutbox(
            oldEntityId: entry.entityId, newEntityId: created.id);
      case 'update_note':
        // payload 的 presence 语义直接透传服务端 JSON 形状：
        // 'notebook_id' 存在且为 '' = 移回根；'todo_completed_at' 为
        // '' = 清除完成时间。
        final rawTodoCompletedAt = payload['todo_completed_at'] as String?;
        final updated = await client.updateNote(
          entry.entityId,
          ifMatchVersion: entry.baseVersion ?? 1,
          title: payload['title'] as String?,
          contentMd: payload['content_md'] as String?,
          notebookId: payload.containsKey('notebook_id')
              ? payload['notebook_id'] as String?
              : null,
          isTodo: payload['is_todo'] as bool?,
          todoCompletedAt: rawTodoCompletedAt != null &&
                  rawTodoCompletedAt.isNotEmpty
              ? DateTime.tryParse(rawTodoCompletedAt)
              : null,
          clearTodoCompleted: rawTodoCompletedAt != null &&
              rawTodoCompletedAt.isEmpty,
          position: (payload['position'] as num?)?.toDouble(),
        );
        // Reflect the server-bumped version locally so the next If-Match works.
        await _upsertFromDto(updated, keepTrashed: true);
      case 'trash_note':
        await client.trashNote(entry.entityId);
      case 'restore_note':
        final restored = await client.restoreNote(entry.entityId);
        // 还原时父笔记本若已删，服务端会置根 —— 以响应为准回填。
        await _upsertFromDto(restored, keepTrashed: false);
      case 'purge_note':
        await client.purgeNote(entry.entityId);
      case 'create_tag':
        final created = await client.createTag(payload['name'] as String);
        await dao.renameTagId(entry.entityId, created.id);
        await dao.rekeyOutbox(
            oldEntityId: entry.entityId, newEntityId: created.id);
      case 'set_note_tags':
        final tagIds = (payload['tag_ids'] as List? ?? const [])
            .map((t) => t.toString())
            .toList();
        await client.setNoteTags(entry.entityId, tagIds);
      default:
        throw StateError('unknown outbox op: ${entry.op}');
    }
  }

  /// 用服务端响应覆盖本地行。keepTrashed=true 时保留本地回收站标记
  /// （update_note 不改变回收站状态）。
  Future<void> _upsertFromDto(api.NoteNote n,
      {required bool keepTrashed}) async {
    final existing = await dao.noteById(n.id);
    if (existing == null) return;
    await dao.upsertNote(LocalNote(
      id: n.id,
      notebookId: n.notebookId,
      title: n.title,
      contentMd: n.contentMd,
      isTodo: n.isTodo,
      todoCompletedAt: n.todoCompletedAt,
      position: n.position,
      version: n.version,
      trashed: keepTrashed ? existing.trashed : (n.deletedAt != null),
      trashedAt: keepTrashed ? existing.trashedAt : n.deletedAt,
      updatedAt: n.updatedAt,
    ));
  }

  Future<void> _backoff(NoteOutboxEntry entry, String error) async {
    final attempts = entry.attempts + 1;
    // 1s, 2s, 4s, 8s … capped at 5 min.
    final secs = 1 << (attempts.clamp(0, 8));
    final next = _now().toUtc().add(Duration(seconds: secs.clamp(1, 300)));
    await dao.bumpOutboxFailure(entry.id, error, next);
    _log.fine('outbox ${entry.id} retry in ${secs}s: $error');
  }
}
