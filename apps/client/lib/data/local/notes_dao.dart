// NotesDao — typed queries against the local Drift mirror + note outbox.
//
// 镜像 WikiDao 的形态（O2 先复制后收敛）：所有写路径都经过这里，
// repository 不碰裸 SQL；读走 Drift `watch()`，无论变更来自服务端
// changes 增量还是本地乐观写，UI 都即时刷新。
//
// 与 WikiDao 的差异：笔记无块层（contentMd 整篇存一行）；回收站用
// trashed/trashedAt 列表达；组织维度从 projectId 换成 nullable
// notebookId + 标签关联表。

import 'package:drift/drift.dart';

import 'db.dart';

class NotesDao {
  NotesDao(this._db);

  final AppDb _db;

  // ─── notebooks ────────────────────────────────────────────

  Stream<List<LocalNoteNotebook>> watchNotebooks() {
    return (_db.select(_db.noteNotebooks)
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .watch();
  }

  Future<List<LocalNoteNotebook>> listNotebooks() {
    return (_db.select(_db.noteNotebooks)
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .get();
  }

  Future<LocalNoteNotebook?> notebookById(String id) {
    return (_db.select(_db.noteNotebooks)..where((t) => t.id.equals(id)))
        .getSingleOrNull();
  }

  Future<void> upsertNotebook(LocalNoteNotebook row) {
    return _db.into(_db.noteNotebooks).insertOnConflictUpdate(row);
  }

  Future<void> upsertNotebooks(List<LocalNoteNotebook> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(_db.noteNotebooks, rows);
    });
  }

  /// Renames the local id when the server returns its own uuid.
  ///
  /// Used after a `create_notebook` outbox op succeeds — re-key the row and
  /// every note that referenced the placeholder. We have to
  /// delete-then-insert because the primary key changes.
  Future<void> renameNotebookId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.noteNotebooks)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteNotebooks)..where((t) => t.id.equals(oldId)))
          .go();
      await _db.into(_db.noteNotebooks).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNotes)
            ..where((t) => t.notebookId.equals(oldId)))
          .write(NoteNotesCompanion(notebookId: Value(newId)));
    });
  }

  /// 服务端软删笔记本后本地直接删行（本地表无软删列）；挂着的笔记
  /// 由服务端还原逻辑置根，changes 增量会把它们刷成 notebook_id=NULL。
  Future<void> hardDeleteNotebook(String id) async {
    await (_db.delete(_db.noteNotebooks)..where((t) => t.id.equals(id))).go();
  }

  // ─── notes ────────────────────────────────────────────────

  /// 活笔记列表（回收站 + 已归档除外，对齐服务端默认列表语义）。
  /// rootOnly=true 只看未分桶笔记；todoOnly=true 只看待办。
  Stream<List<LocalNote>> watchNotes({
    String? notebookId,
    bool rootOnly = false,
    bool todoOnly = false,
  }) {
    final q = _db.select(_db.noteNotes)
      ..where((t) => t.trashed.equals(false))
      ..where((t) => t.archivedAt.isNull())
      ..orderBy([
        (t) => OrderingTerm(expression: t.position),
        (t) =>
            OrderingTerm(expression: t.updatedAt, mode: OrderingMode.desc),
      ]);
    if (rootOnly) {
      q.where((t) => t.notebookId.isNull());
    } else if (notebookId != null) {
      q.where((t) => t.notebookId.equals(notebookId));
    }
    if (todoOnly) {
      q.where((t) => t.isTodo.equals(true));
    }
    return q.watch();
  }

  /// 回收站视图，按丢弃时间倒序。
  Stream<List<LocalNote>> watchTrash() {
    return (_db.select(_db.noteNotes)
          ..where((t) => t.trashed.equals(true))
          ..orderBy([
            (t) =>
                OrderingTerm(expression: t.trashedAt, mode: OrderingMode.desc),
          ]))
        .watch();
  }

  /// 按标签过滤（join note_note_tags）。
  Stream<List<LocalNote>> watchNotesForTag(String tagId) {
    final notes = _db.noteNotes;
    final links = _db.noteNoteTags;
    final q = _db.select(notes).join([
      innerJoin(links, links.noteId.equalsExp(notes.id)),
    ])
      ..where(links.tagId.equals(tagId) & notes.trashed.equals(false))
      ..orderBy([OrderingTerm(expression: notes.position)]);
    return q.watch().map(
        (rows) => rows.map((r) => r.readTable(notes)).toList());
  }

  Future<LocalNote?> noteById(String id) {
    return (_db.select(_db.noteNotes)..where((t) => t.id.equals(id)))
        .getSingleOrNull();
  }

  /// id 以 [prefix] 开头的笔记行（含回收站）。用于启动时扫描历史
  /// 'local-' 占位 id 的孤儿笔记（见 repository.recoverOrphanedLocalNotes）。
  Future<List<LocalNote>> notesWithIdPrefix(String prefix) {
    return (_db.select(_db.noteNotes)..where((t) => t.id.like('$prefix%')))
        .get();
  }

  /// 整行覆盖语义：这里存的都是「服务端/本地的完整快照」（LocalNote 数据
  /// 类每列都有值），必须连 null 也写进去。insertOnConflictUpdate 走
  /// toColumns(nullToAbsent: true)，null 列会被当成 absent 而保留旧值
  /// —— unarchive 清 archivedAt、还原清 trashedAt、移回根清 notebookId
  /// 都依赖 replace 才能真正清掉（N3 修复）。
  Future<void> upsertNote(LocalNote row) {
    return _db.into(_db.noteNotes).insert(row, mode: InsertMode.replace);
  }

  Future<void> upsertNotes(List<LocalNote> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAll(_db.noteNotes, rows, mode: InsertMode.replace);
    });
  }

  Future<void> renameNoteId(String oldId, String newId) async {
    // create_note 上送客户端 uuid 时服务端 id 与本地相同 —— no-op。
    if (oldId == newId) return;
    await _db.transaction(() async {
      final existing = await (_db.select(_db.noteNotes)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteNotes)..where((t) => t.id.equals(oldId)))
          .go();
      await _db.into(_db.noteNotes).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNoteTags)
            ..where((t) => t.noteId.equals(oldId)))
          .write(NoteNoteTagsCompanion(noteId: Value(newId)));
    });
  }

  /// 进回收站（本地乐观写；tombstone 事件到达时同样走这里）。
  Future<void> markNoteTrashed(String id, DateTime trashedAt) async {
    await (_db.update(_db.noteNotes)..where((t) => t.id.equals(id))).write(
      NoteNotesCompanion(
        trashed: const Value(true),
        trashedAt: Value(trashedAt),
        updatedAt: Value(DateTime.now().toUtc()),
      ),
    );
  }

  Future<void> markNoteRestored(String id) async {
    await (_db.update(_db.noteNotes)..where((t) => t.id.equals(id))).write(
      NoteNotesCompanion(
        trashed: const Value(false),
        trashedAt: const Value(null),
        updatedAt: Value(DateTime.now().toUtc()),
      ),
    );
  }

  /// 物理删除（purge）；连标签关联一起清。
  Future<void> hardDeleteNote(String id) async {
    await _db.transaction(() async {
      await (_db.delete(_db.noteNoteTags)..where((t) => t.noteId.equals(id)))
          .go();
      await (_db.delete(_db.noteNotes)..where((t) => t.id.equals(id))).go();
    });
  }

  // ─── tags ─────────────────────────────────────────────────

  Stream<List<LocalNoteTag>> watchTags() {
    return (_db.select(_db.noteTags)
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .watch();
  }

  Future<List<LocalNoteTag>> listTags() {
    return (_db.select(_db.noteTags)
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .get();
  }

  Future<void> upsertTag(LocalNoteTag row) {
    return _db.into(_db.noteTags).insertOnConflictUpdate(row);
  }

  Future<void> upsertTags(List<LocalNoteTag> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(_db.noteTags, rows);
    });
  }

  Future<void> renameTagId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.noteTags)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteTags)..where((t) => t.id.equals(oldId))).go();
      await _db.into(_db.noteTags).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNoteTags)
            ..where((t) => t.tagId.equals(oldId)))
          .write(NoteNoteTagsCompanion(tagId: Value(newId)));
    });
  }

  /// 整组替换笔记的标签关联（对齐服务端 PUT /v1/notes/{id}/tags）。
  Future<void> setNoteTags(String noteId, List<String> tagIds) async {
    await _db.transaction(() async {
      await (_db.delete(_db.noteNoteTags)
            ..where((t) => t.noteId.equals(noteId)))
          .go();
      await _db.batch((b) {
        b.insertAll(
          _db.noteNoteTags,
          [for (final tagId in tagIds) NoteNoteTag(noteId: noteId, tagId: tagId)],
          mode: InsertMode.insertOrIgnore,
        );
      });
    });
  }

  Future<List<String>> listTagIdsForNote(String noteId) async {
    final rows = await (_db.select(_db.noteNoteTags)
          ..where((t) => t.noteId.equals(noteId)))
        .get();
    return rows.map((r) => r.tagId).toList();
  }

  // ─── outbox ───────────────────────────────────────────────

  /// Returns rows that are ready to be flushed (`nextAttemptAt <= now`).
  Future<List<NoteOutboxEntry>> dueOutbox({DateTime? now}) {
    final t = now ?? DateTime.now().toUtc();
    return (_db.select(_db.noteOutbox)
          ..where((r) => r.nextAttemptAt.isSmallerOrEqualValue(t))
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  Future<List<NoteOutboxEntry>> allOutbox() {
    return (_db.select(_db.noteOutbox)
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  /// 按主键取单行 —— flusher 在 apply 前重读，拿到同批 create_* 成功
  /// 后 rekey 过的最新 entityId/payloadJson（flushOnce 开头的 due 快照
  /// 是旧的）。
  Future<NoteOutboxEntry?> outboxById(int id) {
    return (_db.select(_db.noteOutbox)..where((r) => r.id.equals(id)))
        .getSingleOrNull();
  }

  /// 整表 watch —— outbox 很小（未冲刷的乐观写），repository 用它作触发
  /// 流，让 watch 流里的 pending 标志在订阅期内随 outbox 变化刷新。
  Stream<List<NoteOutboxEntry>> watchOutbox() {
    return (_db.select(_db.noteOutbox)
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .watch();
  }

  Stream<int> watchOutboxCount() {
    final c = _db.noteOutbox.id.count();
    final q = _db.selectOnly(_db.noteOutbox)..addColumns([c]);
    return q.watchSingle().map((r) => r.read(c) ?? 0);
  }

  Future<int> enqueueOutbox(NoteOutboxCompanion entry) {
    return _db.into(_db.noteOutbox).insert(entry);
  }

  Future<void> deleteOutbox(int id) async {
    await (_db.delete(_db.noteOutbox)..where((r) => r.id.equals(id))).go();
  }

  Future<void> bumpOutboxFailure(
      int id, String error, DateTime nextAttempt) async {
    await (_db.update(_db.noteOutbox)..where((r) => r.id.equals(id))).write(
      NoteOutboxCompanion(
        attempts: const Value.absent(),
        lastError: Value(error),
        nextAttemptAt: Value(nextAttempt),
      ),
    );
    // Increment attempts via a separate raw update so the value isn't lost.
    await _db.customStatement(
      'UPDATE note_outbox SET attempts = attempts + 1 WHERE id = ?',
      [id],
    );
  }

  /// When a create_* op succeeds with a new server id we have to rewrite any
  /// queued ops that referenced the placeholder — entityId/notebookId 两列
  /// 以及 payloadJson 里的引用（create_note/update_note 的 notebook_id、
  /// set_note_tags 的 tag_ids 列表）。id 是 uuid 形态，在 JSON 里只会以
  /// 完整字符串值出现（content_md 等内容里的引号已被转义），所以精确
  /// 替换 '"old"' → '"new"' 是安全的。
  Future<void> rekeyOutbox({
    required String oldEntityId,
    required String newEntityId,
  }) async {
    await _db.transaction(() async {
      await (_db.update(_db.noteOutbox)
            ..where((r) => r.entityId.equals(oldEntityId)))
          .write(NoteOutboxCompanion(entityId: Value(newEntityId)));
      await (_db.update(_db.noteOutbox)
            ..where((r) => r.notebookId.equals(oldEntityId)))
          .write(NoteOutboxCompanion(notebookId: Value(newEntityId)));
      final stale = await (_db.select(_db.noteOutbox)
            ..where((r) => r.payloadJson.like('%$oldEntityId%')))
          .get();
      for (final row in stale) {
        final patched =
            row.payloadJson.replaceAll('"$oldEntityId"', '"$newEntityId"');
        if (patched == row.payloadJson) continue;
        await (_db.update(_db.noteOutbox)
              ..where((r) => r.id.equals(row.id)))
            .write(NoteOutboxCompanion(payloadJson: Value(patched)));
      }
    });
  }

  /// Wipe everything — used on logout and in tests.
  Future<void> wipe() async {
    await _db.transaction(() async {
      await _db.delete(_db.noteOutbox).go();
      await _db.delete(_db.noteNoteTags).go();
      await _db.delete(_db.noteTags).go();
      await _db.delete(_db.noteNotes).go();
      await _db.delete(_db.noteNotebooks).go();
    });
  }
}
