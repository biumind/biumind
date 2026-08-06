// WikiDao — typed queries against the local Drift mirror + outbox.
//
// All write paths go through here so the repository can stay free of raw
// SQL. Streams come from Drift's `watch()` so the UI updates the moment a
// row changes, regardless of whether the change came from the server or a
// pending outbox entry.

import 'package:drift/drift.dart';

import 'db.dart';

class WikiDao {
  WikiDao(this._db);

  final AppDb _db;

  // ─── projects ─────────────────────────────────────────────

  Stream<List<LocalWikiProject>> watchProjects() {
    return (_db.select(_db.wikiProjects)
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .watch();
  }

  Future<List<LocalWikiProject>> listProjects() {
    return (_db.select(_db.wikiProjects)
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .get();
  }

  Future<void> upsertProject(LocalWikiProject row) {
    return _db.into(_db.wikiProjects).insertOnConflictUpdate(row);
  }

  Future<void> upsertProjects(List<LocalWikiProject> rows) async {
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(_db.wikiProjects, rows);
    });
  }

  /// Renames the local id when the server returns a new uuid.
  ///
  /// Used after a `create_project` outbox op succeeds: the local id was a
  /// client-side uuid, the server now hands us its own — re-key the row and
  /// every page that referenced it. We have to delete-then-insert because
  /// the primary key changes.
  Future<void> renameProjectId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.wikiProjects)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.wikiProjects)..where((t) => t.id.equals(oldId)))
          .go();
      await _db.into(_db.wikiProjects).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.wikiPages)
            ..where((t) => t.projectId.equals(oldId)))
          .write(WikiPagesCompanion(projectId: Value(newId)));
    });
  }

  // ─── pages ────────────────────────────────────────────────

  Stream<List<LocalWikiPage>> watchPages(String projectId) {
    return (_db.select(_db.wikiPages)
          ..where((t) => t.projectId.equals(projectId))
          ..orderBy([(t) => OrderingTerm(expression: t.updatedAt, mode: OrderingMode.desc)]))
        .watch();
  }

  Future<void> upsertPage(LocalWikiPage row) {
    return _db.into(_db.wikiPages).insertOnConflictUpdate(row);
  }

  Future<void> upsertPages(List<LocalWikiPage> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(_db.wikiPages, rows);
    });
  }

  Future<void> renamePageId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.wikiPages)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.wikiPages)..where((t) => t.id.equals(oldId)))
          .go();
      await _db.into(_db.wikiPages).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.wikiBlocks)..where((t) => t.pageId.equals(oldId)))
          .write(WikiBlocksCompanion(pageId: Value(newId)));
    });
  }

  // ─── blocks ───────────────────────────────────────────────

  Stream<List<LocalWikiBlock>> watchBlocks(String pageId) {
    return (_db.select(_db.wikiBlocks)
          ..where((t) => t.pageId.equals(pageId) & t.deleted.equals(false))
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .watch();
  }

  Future<List<LocalWikiBlock>> listBlocks(String pageId) {
    return (_db.select(_db.wikiBlocks)
          ..where((t) => t.pageId.equals(pageId) & t.deleted.equals(false))
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .get();
  }

  /// All non-deleted blocks belonging to pages in [projectId]. Joins
  /// wiki_blocks to wiki_pages on page_id so we can filter by project
  /// without storing project_id redundantly on the blocks table.
  ///
  /// Used by the wiki in-page search: searching across the active
  /// project locally is cheap (Drift query, no network), and the
  /// result feeds into a pure-Dart ranking engine.
  Future<List<LocalWikiBlock>> listBlocksByProject(String projectId) async {
    final blocks = _db.wikiBlocks;
    final pages = _db.wikiPages;
    final query = _db.select(blocks).join([
      innerJoin(pages, pages.id.equalsExp(blocks.pageId)),
    ])
      ..where(blocks.deleted.equals(false) &
          pages.projectId.equals(projectId));
    final rows = await query.get();
    return rows.map((r) => r.readTable(blocks)).toList();
  }

  Future<LocalWikiBlock?> blockById(String id) {
    return (_db.select(_db.wikiBlocks)..where((t) => t.id.equals(id)))
        .getSingleOrNull();
  }

  Future<void> upsertBlock(LocalWikiBlock row) {
    return _db.into(_db.wikiBlocks).insertOnConflictUpdate(row);
  }

  Future<void> upsertBlocks(List<LocalWikiBlock> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(_db.wikiBlocks, rows);
    });
  }

  Future<void> renameBlockId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.wikiBlocks)
            ..where((t) => t.id.equals(oldId)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.wikiBlocks)..where((t) => t.id.equals(oldId)))
          .go();
      await _db.into(_db.wikiBlocks).insert(
            existing.copyWith(id: newId),
            mode: InsertMode.insertOrReplace,
          );
    });
  }

  Future<void> markBlockDeleted(String id) async {
    await (_db.update(_db.wikiBlocks)..where((t) => t.id.equals(id))).write(
      WikiBlocksCompanion(
        deleted: const Value(true),
        updatedAt: Value(DateTime.now().toUtc()),
      ),
    );
  }

  Future<void> hardDeleteBlock(String id) async {
    await (_db.delete(_db.wikiBlocks)..where((t) => t.id.equals(id))).go();
  }

  // ─── outbox ───────────────────────────────────────────────

  /// Returns rows that are ready to be flushed (`nextAttemptAt <= now`).
  Future<List<OutboxEntry>> dueOutbox({DateTime? now}) {
    final t = now ?? DateTime.now().toUtc();
    return (_db.select(_db.wikiOutbox)
          ..where((r) => r.nextAttemptAt.isSmallerOrEqualValue(t))
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  Future<List<OutboxEntry>> allOutbox() {
    return (_db.select(_db.wikiOutbox)
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  /// 按主键取单行 —— flusher 在 apply 前重读，拿到同批 create_* 成功
  /// 后 rekey 过的最新 entityId/projectId/pageId（flushOnce 开头的
  /// due 快照是旧的）。
  Future<OutboxEntry?> outboxById(int id) {
    return (_db.select(_db.wikiOutbox)..where((r) => r.id.equals(id)))
        .getSingleOrNull();
  }

  Stream<int> watchOutboxCount() {
    final c = _db.wikiOutbox.id.count();
    final q = _db.selectOnly(_db.wikiOutbox)..addColumns([c]);
    return q.watchSingle().map((r) => r.read(c) ?? 0);
  }

  Future<int> enqueueOutbox(WikiOutboxCompanion entry) {
    return _db.into(_db.wikiOutbox).insert(entry);
  }

  Future<void> deleteOutbox(int id) async {
    await (_db.delete(_db.wikiOutbox)..where((r) => r.id.equals(id))).go();
  }

  Future<void> bumpOutboxFailure(int id, String error, DateTime nextAttempt) async {
    await (_db.update(_db.wikiOutbox)..where((r) => r.id.equals(id))).write(
      WikiOutboxCompanion(
        attempts: const Value.absent(),
        lastError: Value(error),
        nextAttemptAt: Value(nextAttempt),
      ),
    );
    // Increment attempts via a separate raw update so the value isn't lost.
    await _db.customStatement(
      'UPDATE wiki_outbox SET attempts = attempts + 1 WHERE id = ?',
      [id],
    );
  }

  /// When a create_* op succeeds with a new server id we have to rewrite any
  /// queued ops that referenced the placeholder.
  Future<void> rekeyOutbox({
    required String oldEntityId,
    required String newEntityId,
  }) async {
    await (_db.update(_db.wikiOutbox)
          ..where((r) => r.entityId.equals(oldEntityId)))
        .write(WikiOutboxCompanion(entityId: Value(newEntityId)));
    await (_db.update(_db.wikiOutbox)..where((r) => r.projectId.equals(oldEntityId)))
        .write(WikiOutboxCompanion(projectId: Value(newEntityId)));
    await (_db.update(_db.wikiOutbox)..where((r) => r.pageId.equals(oldEntityId)))
        .write(WikiOutboxCompanion(pageId: Value(newEntityId)));
  }

  /// Wipe everything — used on logout and in tests.
  Future<void> wipe() async {
    await _db.transaction(() async {
      await _db.delete(_db.wikiOutbox).go();
      await _db.delete(_db.wikiBlocks).go();
      await _db.delete(_db.wikiPages).go();
      await _db.delete(_db.wikiProjects).go();
    });
  }
}
