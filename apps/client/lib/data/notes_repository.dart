// NotesRepository — local-first façade over Drift + NotesClient.
//
// 镜像 WikiRepository 的模式（O2 先复制后收敛）：读永远走 Drift（离线
// 可用）；写先乐观落 Drift、再 enqueue NoteOutbox，由
// `outbox/note_outbox_flusher.dart` 后台冲刷。笔记创建直接生成真 uuid
// 落库并随 create op 上送（brain POST /v1/notes 支持客户端 id，幂等
// 重放），flush 前后 id 不变 —— UI 持有的引用永不失效。notebook/tag
// 仍用 'local-<uuid>' 占位 id + flush 成功后 rekey（与 wiki 同一模式）。
//
// 与 wiki 的差异：
//   * 无块层 —— contentMd 整篇 markdown 一行存（设计 §4 D1）；
//   * 回收站是软删状态（trashed），purge 才删行；
//   * 冲突用户裁决：flusher 遇 409 丢 op 并发 NoteOutboxConflict，
//     UI 调 [saveAsCopy] 把本地草稿另存为新笔记再人工合并。
//
// ownerKey（v33 Phase 33 数据隔离）：本仓库不持有 scope —— 构造
// LocalNote / LocalNoteNotebook / LocalNoteTag 时 ownerKey 一律传 '' 占位，
// NotesDao 在每次写入时按当前登录 scope 盖章覆盖（见 notes_dao.dart 顶部
// 隔离说明）。读路径同样由 DAO 强制按 scope 过滤。'' 是非法值，即便漏盖
// 也只会在查询中永不匹配（safe-fail，不泄露）。

import 'dart:async';
import 'dart:convert';

import 'package:drift/drift.dart' show Value;
import 'package:meta/meta.dart';
import 'package:uuid/uuid.dart';

import 'api/notes_client.dart' as api;
import 'local/db.dart';
import 'local/notes_dao.dart';

/// saveAsCopy 的标题后缀（N1 数据层不接 l10n，先硬编码中文常量）。
const kNoteConflictCopySuffix = '(冲突副本)';

@immutable
class RepoNotebook {
  final String id;
  final String name;
  final double position;
  final bool pendingCreate;
  const RepoNotebook({
    required this.id,
    required this.name,
    required this.position,
    this.pendingCreate = false,
  });

  factory RepoNotebook.fromLocal(LocalNoteNotebook row,
          {bool pendingCreate = false}) =>
      RepoNotebook(
        id: row.id,
        name: row.name,
        position: row.position,
        pendingCreate: pendingCreate,
      );
}

@immutable
class RepoNote {
  final String id;
  final String? notebookId;
  final String title;
  final String contentMd;
  final bool isTodo;
  final DateTime? todoCompletedAt;
  final double position;
  final int version;
  final bool trashed;
  final DateTime? trashedAt;
  final DateTime? archivedAt;
  final String? promotedPageId;
  final DateTime updatedAt;
  final bool pendingCreate;
  final bool pendingUpdate;

  const RepoNote({
    required this.id,
    this.notebookId,
    required this.title,
    required this.contentMd,
    required this.isTodo,
    this.todoCompletedAt,
    required this.position,
    required this.version,
    this.trashed = false,
    this.trashedAt,
    this.archivedAt,
    this.promotedPageId,
    required this.updatedAt,
    this.pendingCreate = false,
    this.pendingUpdate = false,
  });

  factory RepoNote.fromLocal(
    LocalNote row, {
    bool pendingCreate = false,
    bool pendingUpdate = false,
  }) =>
      RepoNote(
        id: row.id,
        notebookId: row.notebookId,
        title: row.title,
        contentMd: row.contentMd,
        isTodo: row.isTodo,
        todoCompletedAt: row.todoCompletedAt,
        position: row.position,
        version: row.version,
        trashed: row.trashed,
        trashedAt: row.trashedAt,
        archivedAt: row.archivedAt,
        promotedPageId: row.promotedPageId,
        updatedAt: row.updatedAt,
        pendingCreate: pendingCreate,
        pendingUpdate: pendingUpdate,
      );
}

@immutable
class RepoTag {
  final String id;
  final String name;
  final bool pendingCreate;
  const RepoTag({
    required this.id,
    required this.name,
    this.pendingCreate = false,
  });

  factory RepoTag.fromLocal(LocalNoteTag row, {bool pendingCreate = false}) =>
      RepoTag(id: row.id, name: row.name, pendingCreate: pendingCreate);
}

/// Outbox op codes — kept as strings so they survive schema migrations.
class NoteOutboxOp {
  static const createNotebook = 'create_notebook';
  static const updateNotebook = 'update_notebook';
  static const deleteNotebook = 'delete_notebook';
  static const createNote = 'create_note';
  static const updateNote = 'update_note';
  static const trashNote = 'trash_note';
  static const restoreNote = 'restore_note';
  static const purgeNote = 'purge_note';
  static const createTag = 'create_tag';
  static const setNoteTags = 'set_note_tags';
}

class NotesRepository {
  NotesRepository({
    required this.dao,
    required this.client,
    Uuid? uuid,
  }) : _uuid = uuid ?? const Uuid();

  final NotesDao dao;
  final api.NotesClient client;
  final Uuid _uuid;

  // ─── Reads ───────────────────────────────────────────────
  //
  // pending 标志（pendingCreate/pendingUpdate）由 outbox 驱动：用
  // dao.watchOutbox() 与实体流 combine，订阅期内 enqueue/flush 都会推动
  // 重算 —— 否则标志在订阅那一刻定死，applyChanges 的回声抑制会被过期
  // 标志带偏（N2 修复）。outbox 很小，且实体流不变时不会重查实表。

  Stream<List<RepoNotebook>> watchNotebooks() {
    return _combineLatest2(
      dao.watchNotebooks(),
      _watchPendingCreateIds(NoteOutboxOp.createNotebook),
      (rows, pendingIds) => rows
          .map((r) =>
              RepoNotebook.fromLocal(r, pendingCreate: pendingIds.contains(r.id)))
          .toList(),
    );
  }

  Stream<List<RepoNote>> watchNotes({
    String? notebookId,
    bool rootOnly = false,
    bool todoOnly = false,
  }) {
    return _combineLatest2(
      dao.watchNotes(
          notebookId: notebookId, rootOnly: rootOnly, todoOnly: todoOnly),
      _watchPendingNoteFlags(),
      (rows, pending) => rows
          .map((r) => RepoNote.fromLocal(
                r,
                pendingCreate: pending.create.contains(r.id),
                pendingUpdate: pending.update.contains(r.id),
              ))
          .toList(),
    );
  }

  Stream<List<RepoNote>> watchTrash() async* {
    yield* dao
        .watchTrash()
        .map((rows) => rows.map(RepoNote.fromLocal).toList());
  }

  Stream<List<RepoNote>> watchNotesForTag(String tagId) {
    return _combineLatest2(
      dao.watchNotesForTag(tagId),
      _watchPendingNoteFlags(),
      (rows, pending) => rows
          .map((r) => RepoNote.fromLocal(
                r,
                pendingCreate: pending.create.contains(r.id),
                pendingUpdate: pending.update.contains(r.id),
              ))
          .toList(),
    );
  }

  Stream<List<RepoTag>> watchTags() {
    return _combineLatest2(
      dao.watchTags(),
      _watchPendingCreateIds(NoteOutboxOp.createTag),
      (rows, pendingIds) => rows
          .map((r) =>
              RepoTag.fromLocal(r, pendingCreate: pendingIds.contains(r.id)))
          .toList(),
    );
  }

  Future<RepoNote?> getNote(String id) async {
    final row = await dao.noteById(id);
    return row == null ? null : RepoNote.fromLocal(row);
  }

  Future<List<String>> listTagIdsForNote(String noteId) =>
      dao.listTagIdsForNote(noteId);

  Stream<int> watchPendingCount() => dao.watchOutboxCount();

  /// 全文搜索 —— 纯服务端调用，不进本地镜像；离线无本地降级（N2 范围外，
  /// UI 层显示错误态 + 重试）。
  Future<List<api.NoteSearchResult>> searchNotes(String q, {int limit = 20}) =>
      client.searchNotes(q, limit: limit);

  Stream<Set<String>> _watchPendingCreateIds(String op) =>
      dao.watchOutbox().map((outbox) => {
            for (final e in outbox)
              if (e.op == op) e.entityId,
          });

  Stream<({Set<String> create, Set<String> update})>
      _watchPendingNoteFlags() => dao.watchOutbox().map((outbox) => (
            create: {
              for (final e in outbox)
                if (e.op == NoteOutboxOp.createNote) e.entityId,
            },
            update: {
              for (final e in outbox)
                if (e.op == NoteOutboxOp.updateNote) e.entityId,
            },
          ));

  // ─── Refresh from server (全量) ──────────────────────────

  /// 全量拉取并覆盖本地镜像。网络失败故意不吞 —— 调用方（poller 启动时）
  /// 决定如何处理；watch 流不受影响，本地缓存仍是 UI 的真相源。
  Future<void> refreshAll() async {
    await Future.wait([
      refreshNotebooks(),
      refreshNotes(),
      refreshTrash(),
      refreshTags(),
    ]);
  }

  Future<void> refreshNotebooks() async {
    final notebooks = await client.listNotebooks();
    await dao.upsertNotebooks([
      for (final nb in notebooks)
        LocalNoteNotebook(
          id: nb.id,
          name: nb.name,
          position: nb.position,
          ownerKey: '',
          updatedAt: nb.updatedAt,
        ),
    ]);
  }

  Future<void> refreshNotes() async {
    final notes = await client.listNotes(limit: 500);
    await dao.upsertNotes([for (final n in notes) _localFromDto(n)]);
  }

  Future<void> refreshTrash() async {
    final notes = await client.listTrash(limit: 500);
    await dao.upsertNotes([for (final n in notes) _localFromDto(n)]);
  }

  Future<void> refreshTags() async {
    final tags = await client.listTags();
    await dao.upsertTags([
      for (final t in tags) LocalNoteTag(id: t.id, name: t.name, ownerKey: ''),
    ]);
  }

  LocalNote _localFromDto(api.NoteNote n) => LocalNote(
        id: n.id,
        notebookId: n.notebookId,
        title: n.title,
        contentMd: n.contentMd,
        isTodo: n.isTodo,
        todoCompletedAt: n.todoCompletedAt,
        position: n.position,
        version: n.version,
        // 服务端权威快照 —— base := 本次服务端内容与版本（3-way merge
        // 共同祖先）。本地编辑保留此基线不动，仅在下次服务端确认时刷新。
        baseContentMd: n.contentMd,
        baseVersion: n.version,
        trashed: n.deletedAt != null,
        trashedAt: n.deletedAt,
        archivedAt: n.archivedAt,
        promotedPageId: n.promotedPageId,
        ownerKey: '',
        updatedAt: n.updatedAt,
      );

  // ─── 历史 local- 占位笔记恢复 ─────────────────────────────

  /// 历史版本的 createNote 用 `local-<uuid>` 占位 id 落库，flush 成功后
  /// rekey；若 create_note op 在 flush 前丢失，这些笔记只剩本地行、永远
  /// 同步不上服务端。启动时调用一次（providers 里 fire-and-forget）：
  /// 扫描 id 以 'local-' 开头的 note 行，对没有待冲刷 create_note op 的
  /// 按当前行数据补入一条（payload 不带 id —— `local-<uuid>` 不是合法
  /// uuid，走服务端分配 + flusher 既有 rekey 路径）。幂等，可重复调用。
  Future<int> recoverOrphanedLocalNotes() async {
    final orphans = await dao.notesWithIdPrefix('local-');
    if (orphans.isEmpty) return 0;
    final pendingCreates = {
      for (final e in await dao.allOutbox())
        if (e.op == NoteOutboxOp.createNote) e.entityId,
    };
    var recovered = 0;
    for (final row in orphans) {
      if (pendingCreates.contains(row.id)) continue;
      final now = DateTime.now().toUtc();
      await dao.enqueueOutbox(NoteOutboxCompanion.insert(
        op: NoteOutboxOp.createNote,
        entityId: row.id,
        notebookId: Value(row.notebookId),
        payloadJson: jsonEncode({
          'title': row.title,
          'content_md': row.contentMd,
          'is_todo': row.isTodo,
          'position': row.position,
          'notebook_id': ?row.notebookId,
        }),
        createdAt: now,
        nextAttemptAt: now,
      ));
      recovered++;
    }
    return recovered;
  }

  // ─── Changes (增量应用) ──────────────────────────────────

  /// 把 GET /v1/notes/changes 拉到的增量事件应用到 Drift。
  ///
  /// tombstone：note.deleted → 本地置 trashed；note.purged → 删行；
  /// notebook.deleted → 删行（挂着的笔记由服务端还原逻辑置根，后续
  /// note.updated 事件会刷成 notebook_id=NULL）。
  ///
  /// 回声抑制（简化版）：本机 flush 出去的变更会经事件流回显。对仍有
  /// 未冲刷 update_note op 的笔记跳过 payload 覆盖，避免用旧的回声
  /// 冲掉用户正在编辑的乐观内容 —— 服务端版本以 flush 响应为准。
  Future<void> applyChanges(List<api.NoteChangeEvent> events) async {
    if (events.isEmpty) return;
    final outbox = await dao.allOutbox();
    final pendingUpdates = {
      for (final e in outbox)
        if (e.op == NoteOutboxOp.updateNote) e.entityId,
    };
    for (final e in events) {
      final p = e.payload;
      switch (e.eventType) {
        case 'note.created':
        case 'note.restored':
          await _applyNotePayload(p, e.createdAt);
        case 'note.updated':
          final id = p['note_id'] as String?;
          if (id == null) break;
          if (pendingUpdates.contains(id)) break; // 回声抑制
          await _applyNotePayload(p, e.createdAt);
        case 'note.deleted':
          final id = p['note_id'] as String?;
          if (id != null) await dao.markNoteTrashed(id, e.createdAt);
        case 'note.purged':
          final id = p['note_id'] as String?;
          if (id != null) await dao.hardDeleteNote(id);
        case 'notebook.created':
        case 'notebook.updated':
          final id = p['notebook_id'] as String?;
          if (id == null) break;
          await dao.upsertNotebook(LocalNoteNotebook(
            id: id,
            name: p['name'] as String? ?? '',
            position: (p['position'] as num?)?.toDouble() ?? 0.0,
            ownerKey: '',
            updatedAt: e.createdAt,
          ));
        case 'notebook.deleted':
          final id = p['notebook_id'] as String?;
          if (id != null) await dao.hardDeleteNotebook(id);
        case 'tag.created':
          final id = p['tag_id'] as String?;
          if (id != null) {
            await dao.upsertTag(LocalNoteTag(
              id: id,
              name: p['name'] as String? ?? '',
              ownerKey: '',
            ));
          }
        case 'note.tags_updated':
          final noteId = p['note_id'] as String?;
          if (noteId == null) break;
          final tagIds = (p['tag_ids'] as List? ?? const [])
              .map((t) => t.toString())
              .toList();
          await dao.setNoteTags(noteId, tagIds);
        default:
          // 未知事件类型（未来新增）—— 跳过，不影响游标推进。
          break;
      }
    }
  }

  /// note.created/updated/restored 的 payload 是整行快照
  /// （store.notePayload），直接 upsert。
  Future<void> _applyNotePayload(
      Map<String, dynamic> p, DateTime eventAt) async {
    final id = p['note_id'] as String?;
    if (id == null) return;
    await dao.upsertNote(LocalNote(
      id: id,
      notebookId: p['notebook_id'] as String?,
      title: p['title'] as String? ?? '',
      contentMd: p['content_md'] as String? ?? '',
      isTodo: p['is_todo'] as bool? ?? false,
      todoCompletedAt:
          DateTime.tryParse(p['todo_completed_at'] as String? ?? '')
              ?.toUtc(),
      position: (p['position'] as num?)?.toDouble() ?? 0.0,
      version: (p['version'] as num?)?.toInt() ?? 1,
      // 服务端事件快照 = 权威，base := 本次 content/version（3-way merge
      // 共同祖先）。
      baseContentMd: p['content_md'] as String? ?? '',
      baseVersion: (p['version'] as num?)?.toInt() ?? 1,
      // created/updated/restored 事件只会出现在活笔记上。
      trashed: false,
      trashedAt: null,
      archivedAt:
          DateTime.tryParse(p['archived_at'] as String? ?? '')?.toUtc(),
      promotedPageId: p['promoted_page_id'] as String?,
      ownerKey: '',
      updatedAt:
          DateTime.tryParse(p['updated_at'] as String? ?? '')?.toUtc() ??
              eventAt,
    ));
  }

  // ─── Writes (optimistic + outbox) ────────────────────────

  Future<RepoNotebook> createNotebook(String name, {double? position}) async {
    final id = 'local-${_uuid.v4()}';
    final now = DateTime.now().toUtc();
    await dao.upsertNotebook(LocalNoteNotebook(
      id: id,
      name: name,
      position: position ?? 0.0,
      ownerKey: '',
      updatedAt: now,
    ));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.createNotebook,
      entityId: id,
      payloadJson: jsonEncode({
        'name': name,
        'position': ?position,
      }),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoNotebook(
        id: id, name: name, position: position ?? 0.0, pendingCreate: true);
  }

  Future<void> updateNotebook(String id,
      {String? name, double? position}) async {
    final existing = await dao.notebookById(id);
    if (existing == null) {
      throw StateError('notebook not found: $id');
    }
    final now = DateTime.now().toUtc();
    await dao.upsertNotebook(LocalNoteNotebook(
      id: id,
      name: name ?? existing.name,
      position: position ?? existing.position,
      ownerKey: '',
      updatedAt: now,
    ));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.updateNotebook,
      entityId: id,
      payloadJson: jsonEncode({
        'name': ?name,
        'position': ?position,
      }),
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<void> deleteNotebook(String id) async {
    await dao.hardDeleteNotebook(id);
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.deleteNotebook,
      entityId: id,
      payloadJson: '{}',
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  /// 创建笔记。id 用真 uuid（非 'local-' 占位），随 create op 的 payload
  /// 上送服务端（幂等重放安全），flush 前后 id 不变，创建后立刻编辑/
  /// 删除/打标签不会因 rekey 断链。
  Future<RepoNote> createNote({
    String? notebookId,
    required String title,
    String contentMd = '',
    bool isTodo = false,
    double position = 0.0,
  }) async {
    final id = _uuid.v4();
    final now = DateTime.now().toUtc();
    await dao.upsertNote(LocalNote(
      id: id,
      notebookId: notebookId,
      title: title,
      contentMd: contentMd,
      isTodo: isTodo,
      todoCompletedAt: null,
      position: position,
      version: 1,
      trashed: false,
      trashedAt: null,
      archivedAt: null,
      promotedPageId: null,
      ownerKey: '',
      updatedAt: now,
    ));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.createNote,
      entityId: id,
      notebookId: Value(notebookId),
      payloadJson: jsonEncode({
        'id': id,
        'title': title,
        'content_md': contentMd,
        'is_todo': isTodo,
        'position': position,
        'notebook_id': ?notebookId,
      }),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoNote(
      id: id,
      notebookId: notebookId,
      title: title,
      contentMd: contentMd,
      isTodo: isTodo,
      position: position,
      version: 1,
      updatedAt: now,
      pendingCreate: true,
    );
  }

  /// 更新笔记。乐观落库（version 保持服务端基线，flush 成功后由响应
  /// 回填新版本），payload 只带变更字段，presence 语义对齐服务端：
  /// notebookId 传 '' = 移回根；clearTodoCompleted = 清完成时间。
  Future<void> updateNote(
    String id, {
    String? title,
    String? contentMd,
    String? notebookId,
    bool moveToRoot = false,
    bool? isTodo,
    DateTime? todoCompletedAt,
    bool clearTodoCompleted = false,
    double? position,
  }) async {
    final existing = await dao.noteById(id);
    if (existing == null) {
      throw StateError('note not found: $id');
    }
    final now = DateTime.now().toUtc();
    final newNotebookId =
        moveToRoot ? null : (notebookId ?? existing.notebookId);
    final newTodoCompletedAt = clearTodoCompleted
        ? null
        : (todoCompletedAt ?? existing.todoCompletedAt);
    await dao.upsertNote(LocalNote(
      id: existing.id,
      notebookId: newNotebookId,
      title: title ?? existing.title,
      contentMd: contentMd ?? existing.contentMd,
      isTodo: isTodo ?? existing.isTodo,
      todoCompletedAt: newTodoCompletedAt,
      position: position ?? existing.position,
      version: existing.version, // 保持基线，flush 成功后回填
      // 本地编辑不动 merge 基线：保留 existing 的 baseContentMd/baseVersion
      // （= 上次服务端确认态）。下次 flush 成功由 _upsertFromDto 刷新。
      baseContentMd: existing.baseContentMd,
      baseVersion: existing.baseVersion,
      trashed: existing.trashed,
      trashedAt: existing.trashedAt,
      archivedAt: existing.archivedAt,
      promotedPageId: existing.promotedPageId,
      ownerKey: '',
      updatedAt: now,
    ));
    final payload = <String, dynamic>{};
    if (title != null) payload['title'] = title;
    if (contentMd != null) payload['content_md'] = contentMd;
    if (moveToRoot) {
      payload['notebook_id'] = '';
    } else if (notebookId != null) {
      payload['notebook_id'] = notebookId;
    }
    if (isTodo != null) payload['is_todo'] = isTodo;
    if (clearTodoCompleted) {
      payload['todo_completed_at'] = '';
    } else if (todoCompletedAt != null) {
      payload['todo_completed_at'] = todoCompletedAt.toIso8601String();
    }
    if (position != null) payload['position'] = position;
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.updateNote,
      entityId: id,
      notebookId: Value(newNotebookId),
      payloadJson: jsonEncode(payload),
      baseVersion: Value(existing.version),
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<void> trashNote(String id) async {
    final existing = await dao.noteById(id);
    if (existing == null) return;
    await dao.markNoteTrashed(id, DateTime.now().toUtc());
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.trashNote,
      entityId: id,
      payloadJson: '{}',
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<void> restoreNote(String id) async {
    final existing = await dao.noteById(id);
    if (existing == null) return;
    await dao.markNoteRestored(id);
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.restoreNote,
      entityId: id,
      payloadJson: '{}',
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<void> purgeNote(String id) async {
    await dao.hardDeleteNote(id);
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.purgeNote,
      entityId: id,
      payloadJson: '{}',
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<RepoTag> createTag(String name) async {
    final id = 'local-${_uuid.v4()}';
    final now = DateTime.now().toUtc();
    await dao.upsertTag(LocalNoteTag(id: id, name: name, ownerKey: ''));
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.createTag,
      entityId: id,
      payloadJson: jsonEncode({'name': name}),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoTag(id: id, name: name, pendingCreate: true);
  }

  Future<void> setNoteTags(String noteId, List<String> tagIds) async {
    await dao.setNoteTags(noteId, tagIds);
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(NoteOutboxCompanion.insert(
      op: NoteOutboxOp.setNoteTags,
      entityId: noteId,
      payloadJson: jsonEncode({'tag_ids': tagIds}),
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  // ─── 版本历史 (N3) ───────────────────────────────────────
  //
  // 版本数据纯服务端（本地不镜像 revision），restore/save-as-copy 的返回
  // note 落 Drift，由 watch 流驱动编辑器/列表刷新。

  Future<List<api.NoteRevision>> listRevisions(String noteId,
          {int? limit, int? offset}) =>
      client.listRevisions(noteId, limit: limit, offset: offset);

  Future<api.NoteRevision> getRevision(String noteId, String revisionId) =>
      client.getRevision(noteId, revisionId);

  /// 覆盖式恢复。服务端恢复前自动备份当前状态为恢复点；返回的 note 落库
  /// 后编辑器经 noteByIdProvider 流自动刷新内容。
  Future<RepoNote> restoreRevision(String noteId, String revisionId) async {
    final note = await client.restoreRevision(noteId, revisionId);
    final local = _localFromDto(note);
    await dao.upsertNote(local);
    return RepoNote.fromLocal(local);
  }

  /// 以历史版本另存为新笔记（服务端复制标签 + 标题加「（历史副本）」），
  /// 返回的新 note 落库后出现在列表里。
  Future<RepoNote> saveRevisionAsCopy(
      String noteId, String revisionId) async {
    final note = await client.saveRevisionAsCopy(noteId, revisionId);
    final local = _localFromDto(note);
    await dao.upsertNote(local);
    return RepoNote.fromLocal(local);
  }

  // ─── 归档 / 转知识库 (N3) ─────────────────────────────────

  /// 转入知识库：服务端归档笔记 + 建 wiki page（幂等）。返回的归档 note
  /// 落库后（archivedAt 置位）自动从默认列表消失。
  Future<api.NotePromoteResult> promoteNote(
      String noteId, String projectId) async {
    final result = await client.promoteNote(noteId, projectId);
    await dao.upsertNote(_localFromDto(result.note));
    return result;
  }

  /// 取消归档（data 层先备好，归档入口 UI 后续做）。
  Future<RepoNote> unarchiveNote(String noteId) async {
    final note = await client.unarchiveNote(noteId);
    final local = _localFromDto(note);
    await dao.upsertNote(local);
    return RepoNote.fromLocal(local);
  }

  // ─── 冲突用户裁决 (设计 §4 D4) ─────────────────────────────

  /// 把本地行复制成新笔记（新 local id，标题加 [kNoteConflictCopySuffix]
  /// 后缀，content 取本地值）。供 UI 在 409 冲突后做三向选择：
  /// 保留本地草稿 → saveAsCopy，再人工与服务端版本合并。
  Future<RepoNote> saveAsCopy(String noteId) async {
    final existing = await dao.noteById(noteId);
    if (existing == null) {
      throw StateError('note not found: $noteId');
    }
    return createNote(
      notebookId: existing.notebookId,
      title: '${existing.title}$kNoteConflictCopySuffix',
      contentMd: existing.contentMd,
      isTodo: existing.isTodo,
      position: existing.position,
    );
  }
}

/// 极简 combineLatest2 —— 与 chat_repo.rxCombineLatest2 同形态，不引
/// rxdart 保持 dep 干净。两个流都至少 emit 过一次后才发；之后任一更新
/// 都重新 combine 一次（另一边的旧值复用，不重查底层表）。
Stream<R> _combineLatest2<A, B, R>(
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
