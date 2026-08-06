// WikiOutboxFlusher — drains the WikiOutbox table against the live API.
//
// Strategy
//   * `flushOnce()` walks all entries whose `nextAttemptAt <= now` in id
//     order, applies them sequentially, and either deletes them (success) or
//     bumps `attempts` + reschedules with exponential backoff (failure).
//   * `start()` arms a periodic timer that calls `flushOnce` every
//     [interval] (default 5 s) and also exposes `kick()` for the
//     repository to trigger an immediate attempt right after enqueueing.
//   * Local placeholder ids (the `local-…` uuids assigned at enqueue time)
//     are rewritten to the server-issued ids on success — both in the data
//     tables and in any pending outbox entries that referenced the
//     placeholder.
//   * On HTTP 409 (If-Match version conflict) we drop the conflicting op
//     and let the repository surface a "stale, refresh required" event via
//     [conflicts] — the user can re-edit instead of silently overwriting
//     fresh server state.

import 'dart:async';
import 'dart:convert';

import 'package:logging/logging.dart';

import '../api/wiki_client.dart' as api;
import '../local/db.dart';
import '../local/wiki_dao.dart';

class WikiOutboxConflict {
  final String op;
  final String entityId;
  final int? baseVersion;
  final String body;
  const WikiOutboxConflict({
    required this.op,
    required this.entityId,
    required this.baseVersion,
    required this.body,
  });

  @override
  String toString() =>
      'WikiOutboxConflict(op=$op, entity=$entityId, base=$baseVersion)';
}

class WikiOutboxFlusher {
  WikiOutboxFlusher({
    required this.dao,
    required this.client,
    this.interval = const Duration(seconds: 5),
    DateTime Function()? clock,
    Logger? log,
  })  : _now = clock ?? DateTime.now,
        _log = log ?? Logger('WikiOutboxFlusher');

  final WikiDao dao;
  final api.WikiClient client;
  final Duration interval;
  final DateTime Function() _now;
  final Logger _log;

  Timer? _timer;
  bool _flushing = false;
  final _conflicts = StreamController<WikiOutboxConflict>.broadcast();

  /// Conflict stream — UI can subscribe to surface "this edit was based on a
  /// stale version" snackbars without coupling to the flusher's internals.
  Stream<WikiOutboxConflict> get conflicts => _conflicts.stream;

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
    try {
      final due = await dao.dueOutbox(now: _now().toUtc());
      for (final entry in due) {
        try {
          // apply 前重读：同批前面的 create_* 成功后会 rekey，due 快照里
          // 的 entityId/projectId/pageId 可能已是旧值（local- 占位直接
          // 上送会 4xx 丢 op）。
          final fresh = await dao.outboxById(entry.id);
          if (fresh == null) continue;
          await _applyOne(fresh);
          await dao.deleteOutbox(entry.id);
        } on api.WikiApiError catch (e) {
          if (e.isVersionConflict) {
            _conflicts.add(WikiOutboxConflict(
              op: entry.op,
              entityId: entry.entityId,
              baseVersion: entry.baseVersion,
              body: e.body,
            ));
            // Drop the op — the user's pending edit was based on stale state.
            // Server is source of truth; the next refresh will pull the
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
  }

  Future<void> _applyOne(OutboxEntry entry) async {
    final payload = jsonDecode(entry.payloadJson) as Map<String, dynamic>;
    switch (entry.op) {
      case 'create_project':
        final created = await client.createProject(
          payload['name'] as String,
          templateId: payload['template_id'] as String?,
        );
        await dao.renameProjectId(entry.entityId, created.id);
        await dao.rekeyOutbox(oldEntityId: entry.entityId, newEntityId: created.id);
      case 'create_page':
        final projectId = entry.projectId!;
        final created = await client.createPage(
          projectId,
          title: payload['title'] as String,
        );
        await dao.renamePageId(entry.entityId, created.id);
        await dao.rekeyOutbox(oldEntityId: entry.entityId, newEntityId: created.id);
      case 'create_block':
        final projectId = entry.projectId!;
        final pageId = entry.pageId!;
        final created = await client.createBlock(
          projectId,
          pageId,
          type: payload['type'] as String,
          position: (payload['position'] as num).toDouble(),
          content: (payload['content'] as Map).cast<String, dynamic>(),
        );
        await dao.renameBlockId(entry.entityId, created.id);
        await dao.rekeyOutbox(oldEntityId: entry.entityId, newEntityId: created.id);
      case 'update_block':
        final projectId = entry.projectId!;
        final updated = await client.updateBlock(
          projectId,
          entry.entityId,
          content: (payload['content'] as Map).cast<String, dynamic>(),
          ifMatchVersion: entry.baseVersion ?? 1,
          position: payload['position'] != null
              ? (payload['position'] as num).toDouble()
              : null,
        );
        // Reflect the server-bumped version locally so the next If-Match works.
        final existing = await dao.blockById(entry.entityId);
        if (existing != null) {
          await dao.upsertBlock(LocalWikiBlock(
            id: existing.id,
            pageId: existing.pageId,
            position: updated.position,
            type: updated.type,
            contentJson: jsonEncode(updated.content),
            version: updated.version,
            deleted: existing.deleted,
            updatedAt: DateTime.now().toUtc(),
          ));
        }
      case 'delete_block':
        final projectId = entry.projectId!;
        await client.deleteBlock(projectId, entry.entityId);
        await dao.hardDeleteBlock(entry.entityId);
      default:
        throw StateError('unknown outbox op: ${entry.op}');
    }
  }

  Future<void> _backoff(OutboxEntry entry, String error) async {
    final attempts = entry.attempts + 1;
    // 1s, 2s, 4s, 8s … capped at 5 min.
    final secs = 1 << (attempts.clamp(0, 8));
    final next = _now().toUtc().add(Duration(seconds: secs.clamp(1, 300)));
    await dao.bumpOutboxFailure(entry.id, error, next);
    _log.fine('outbox ${entry.id} retry in ${secs}s: $error');
  }
}
