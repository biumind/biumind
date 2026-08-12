// NotesDao — typed queries against the local Drift mirror + note outbox.
//
// 镜像 WikiDao 的形态（O2 先复制后收敛）：所有写路径都经过这里，
// repository 不碰裸 SQL；读走 Drift `watch()`，无论变更来自服务端
// changes 增量还是本地乐观写，UI 都即时刷新。
//
// 与 WikiDao 的差异：笔记无块层（contentMd 整篇存一行）；回收站用
// trashed/trashedAt 列表达；组织维度从 projectId 换成 nullable
// notebookId + 标签关联表。
//
// P0 数据隔离（v33 Phase 33，对齐 chat 的 ownerKey 隔离，
// docs/BiuMind-Local-Data-Isolation-Design.md §2/§3）：构造时绑定 ownerKey
// scope（sha256(环境) + ":" + userId），**所有读强制 `ownerKey = scope`
// 过滤、所有写一律盖章 `ownerKey = scope`**。'' 为非法值（查询永不匹配，
// v33 migration 已清空全部无归属存量行）。compile 期 scope 必填非空，
// 不存在「不过滤 / 不盖章」的调用路径。笔记表此前无任何隔离，本地持久化
// 的笔记不按账号过滤、登出也不清——重新部署 + 重新注册登录后桌面端会把
// 上一账号的笔记直接展示给新账号（跨账号泄露）；本隔离根除该问题。

import 'dart:convert';

import 'package:drift/drift.dart';

import 'db.dart';

class NotesDao {
  NotesDao(this._db, {required this.scope}) : assert(scope.isNotEmpty);

  final AppDb _db;

  /// 当前登录态的 owner scope（见 chat_scope.dart）。所有查询/更新/删除
  /// 一律带 `ownerKey = scope` 条件，写入一律落 scope。
  final String scope;

  // ─── notebooks ────────────────────────────────────────────

  Stream<List<LocalNoteNotebook>> watchNotebooks() {
    return (_db.select(_db.noteNotebooks)
          ..where((t) => t.ownerKey.equals(scope))
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .watch();
  }

  Future<List<LocalNoteNotebook>> listNotebooks() {
    return (_db.select(_db.noteNotebooks)
          ..where((t) => t.ownerKey.equals(scope))
          ..orderBy([(t) => OrderingTerm(expression: t.position)]))
        .get();
  }

  Future<LocalNoteNotebook?> notebookById(String id) {
    return (_db.select(_db.noteNotebooks)
          ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
        .getSingleOrNull();
  }

  Future<void> upsertNotebook(LocalNoteNotebook row) {
    return _db.into(_db.noteNotebooks)
        .insertOnConflictUpdate(row.copyWith(ownerKey: scope));
  }

  Future<void> upsertNotebooks(List<LocalNoteNotebook> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(
        _db.noteNotebooks,
        [for (final r in rows) r.copyWith(ownerKey: scope)],
      );
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
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteNotebooks)
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .go();
      await _db.into(_db.noteNotebooks).insert(
            existing.copyWith(id: newId, ownerKey: scope),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNotes)
            ..where((t) =>
                t.notebookId.equals(oldId) & t.ownerKey.equals(scope)))
          .write(NoteNotesCompanion(notebookId: Value(newId)));
    });
  }

  /// 服务端软删笔记本后本地直接删行（本地表无软删列）；挂着的笔记
  /// 由服务端还原逻辑置根，changes 增量会把它们刷成 notebook_id=NULL。
  Future<void> hardDeleteNotebook(String id) async {
    await (_db.delete(_db.noteNotebooks)
          ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
        .go();
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
      ..where((t) => t.ownerKey.equals(scope))
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
          ..where((t) => t.ownerKey.equals(scope))
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
      ..where(links.tagId.equals(tagId) &
          notes.ownerKey.equals(scope) &
          notes.trashed.equals(false))
      ..orderBy([OrderingTerm(expression: notes.position)]);
    return q.watch().map(
        (rows) => rows.map((r) => r.readTable(notes)).toList());
  }

  Future<LocalNote?> noteById(String id) {
    return (_db.select(_db.noteNotes)
          ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
        .getSingleOrNull();
  }

  /// id 以 [prefix] 开头的笔记行（含回收站）。用于启动时扫描历史
  /// 'local-' 占位 id 的孤儿笔记（见 repository.recoverOrphanedLocalNotes）。
  Future<List<LocalNote>> notesWithIdPrefix(String prefix) {
    return (_db.select(_db.noteNotes)
          ..where((t) => t.id.like('$prefix%') & t.ownerKey.equals(scope)))
        .get();
  }

  /// 整行覆盖语义：这里存的都是「服务端/本地的完整快照」（LocalNote 数据
  /// 类每列都有值），必须连 null 也写进去。insertOnConflictUpdate 走
  /// toColumns(nullToAbsent: true)，null 列会被当成 absent 而保留旧值
  /// —— unarchive 清 archivedAt、还原清 trashedAt、移回根清 notebookId
  /// 都依赖 replace 才能真正清掉（N3 修复）。ownerKey 由本方法强制盖当前
  /// scope（repository 传入的占位 '' 被覆盖），保证写入必落正确归属。
  Future<void> upsertNote(LocalNote row) {
    return _db.into(_db.noteNotes)
        .insert(row.copyWith(ownerKey: scope), mode: InsertMode.replace);
  }

  Future<void> upsertNotes(List<LocalNote> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAll(
        _db.noteNotes,
        [for (final r in rows) r.copyWith(ownerKey: scope)],
        mode: InsertMode.replace,
      );
    });
  }

  Future<void> renameNoteId(String oldId, String newId) async {
    // create_note 上送客户端 uuid 时服务端 id 与本地相同 —— no-op。
    if (oldId == newId) return;
    await _db.transaction(() async {
      final existing = await (_db.select(_db.noteNotes)
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteNotes)
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .go();
      await _db.into(_db.noteNotes).insert(
            existing.copyWith(id: newId, ownerKey: scope),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNoteTags)
            ..where((t) =>
                t.noteId.equals(oldId) & t.ownerKey.equals(scope)))
          .write(NoteNoteTagsCompanion(noteId: Value(newId)));
    });
  }

  /// 进回收站（本地乐观写；tombstone 事件到达时同样走这里）。
  Future<void> markNoteTrashed(String id, DateTime trashedAt) async {
    await (_db.update(_db.noteNotes)
          ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
        .write(
      NoteNotesCompanion(
        trashed: const Value(true),
        trashedAt: Value(trashedAt),
        updatedAt: Value(DateTime.now().toUtc()),
      ),
    );
  }

  Future<void> markNoteRestored(String id) async {
    await (_db.update(_db.noteNotes)
          ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
        .write(
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
      await (_db.delete(_db.noteNoteTags)
            ..where((t) =>
                t.noteId.equals(id) & t.ownerKey.equals(scope)))
          .go();
      await (_db.delete(_db.noteNotes)
            ..where((t) => t.id.equals(id) & t.ownerKey.equals(scope)))
          .go();
    });
  }

  // ─── tags ─────────────────────────────────────────────────

  Stream<List<LocalNoteTag>> watchTags() {
    return (_db.select(_db.noteTags)
          ..where((t) => t.ownerKey.equals(scope))
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .watch();
  }

  Future<List<LocalNoteTag>> listTags() {
    return (_db.select(_db.noteTags)
          ..where((t) => t.ownerKey.equals(scope))
          ..orderBy([(t) => OrderingTerm(expression: t.name)]))
        .get();
  }

  Future<void> upsertTag(LocalNoteTag row) {
    return _db.into(_db.noteTags)
        .insertOnConflictUpdate(row.copyWith(ownerKey: scope));
  }

  Future<void> upsertTags(List<LocalNoteTag> rows) async {
    if (rows.isEmpty) return;
    await _db.batch((b) {
      b.insertAllOnConflictUpdate(
        _db.noteTags,
        [for (final r in rows) r.copyWith(ownerKey: scope)],
      );
    });
  }

  Future<void> renameTagId(String oldId, String newId) async {
    await _db.transaction(() async {
      final existing = await (_db.select(_db.noteTags)
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .getSingleOrNull();
      if (existing == null) return;
      await (_db.delete(_db.noteTags)
            ..where((t) => t.id.equals(oldId) & t.ownerKey.equals(scope)))
          .go();
      await _db.into(_db.noteTags).insert(
            existing.copyWith(id: newId, ownerKey: scope),
            mode: InsertMode.insertOrReplace,
          );
      await (_db.update(_db.noteNoteTags)
            ..where((t) =>
                t.tagId.equals(oldId) & t.ownerKey.equals(scope)))
          .write(NoteNoteTagsCompanion(tagId: Value(newId)));
    });
  }

  /// 整组替换笔记的标签关联（对齐服务端 PUT /v1/notes/{id}/tags）。
  Future<void> setNoteTags(String noteId, List<String> tagIds) async {
    await _db.transaction(() async {
      await (_db.delete(_db.noteNoteTags)
            ..where((t) =>
                t.noteId.equals(noteId) & t.ownerKey.equals(scope)))
          .go();
      await _db.batch((b) {
        b.insertAll(
          _db.noteNoteTags,
          [
            for (final tagId in tagIds)
              NoteNoteTag(noteId: noteId, tagId: tagId, ownerKey: scope),
          ],
          mode: InsertMode.insertOrIgnore,
        );
      });
    });
  }

  Future<List<String>> listTagIdsForNote(String noteId) async {
    final rows = await (_db.select(_db.noteNoteTags)
          ..where((t) =>
              t.noteId.equals(noteId) & t.ownerKey.equals(scope)))
        .get();
    return rows.map((r) => r.tagId).toList();
  }

  // ─── outbox ───────────────────────────────────────────────

  /// Returns rows that are ready to be flushed (`nextAttemptAt <= now`).
  /// 只看当前 scope 的 op —— flusher 永不冲刷别的账号的写盒。
  Future<List<NoteOutboxEntry>> dueOutbox({DateTime? now}) {
    final t = now ?? DateTime.now().toUtc();
    return (_db.select(_db.noteOutbox)
          ..where((r) =>
              r.nextAttemptAt.isSmallerOrEqualValue(t) &
              r.ownerKey.equals(scope))
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  Future<List<NoteOutboxEntry>> allOutbox() {
    return (_db.select(_db.noteOutbox)
          ..where((r) => r.ownerKey.equals(scope))
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .get();
  }

  /// 按主键取单行 —— flusher 在 apply 前重读，拿到同批 create_* 成功
  /// 后 rekey 过的最新 entityId/payloadJson（flushOnce 开头的 due 快照
  /// 是旧的）。
  Future<NoteOutboxEntry?> outboxById(int id) {
    return (_db.select(_db.noteOutbox)
          ..where((r) => r.id.equals(id) & r.ownerKey.equals(scope)))
        .getSingleOrNull();
  }

  /// 整表 watch —— outbox 很小（未冲刷的乐观写），repository 用它作触发
  /// 流，让 watch 流里的 pending 标志在订阅期内随 outbox 变化刷新。
  /// 只暴露当前 scope 的 op。
  Stream<List<NoteOutboxEntry>> watchOutbox() {
    return (_db.select(_db.noteOutbox)
          ..where((r) => r.ownerKey.equals(scope))
          ..orderBy([(r) => OrderingTerm(expression: r.id)]))
        .watch();
  }

  Stream<int> watchOutboxCount() {
    final c = _db.noteOutbox.id.count();
    final q = _db.selectOnly(_db.noteOutbox)
      ..addColumns([c])
      ..where(_db.noteOutbox.ownerKey.equals(scope));
    return q.watchSingle().map((r) => r.read(c) ?? 0);
  }

  /// 入队一条 outbox op。ownerKey 强制盖当前 scope（调用方构造 companion
  /// 时不带 ownerKey，本方法补上），杜绝跨账号串写。
  Future<int> enqueueOutbox(NoteOutboxCompanion entry) {
    return _db
        .into(_db.noteOutbox)
        .insert(entry.copyWith(ownerKey: Value(scope)));
  }

  Future<void> deleteOutbox(int id) async {
    await (_db.delete(_db.noteOutbox)
          ..where((r) => r.id.equals(id) & r.ownerKey.equals(scope)))
        .go();
  }

  Future<void> bumpOutboxFailure(
      int id, String error, DateTime nextAttempt) async {
    await (_db.update(_db.noteOutbox)
          ..where((r) => r.id.equals(id) & r.ownerKey.equals(scope)))
        .write(
      NoteOutboxCompanion(
        attempts: const Value.absent(),
        lastError: Value(error),
        nextAttemptAt: Value(nextAttempt),
      ),
    );
    // Increment attempts via a separate raw update so the value isn't lost.
    await _db.customStatement(
      'UPDATE note_outbox SET attempts = attempts + 1 '
      'WHERE id = ? AND owner_key = ?',
      [id, scope],
    );
  }

  /// 合并同笔记的 pending `update_note` op —— 在 flushOnce 起点（_flushing
  /// 守卫内）调用，消除单机编辑会话堆积的多条 update_note。
  ///
  /// 根因：每次 autosave 入一条 update_note，baseVersion 锁定入队瞬间的本地
  /// note.version（repository.updateNote 保持基线，只有 flush 成功才回填）。
  /// 若一轮 flush 里多条同笔记 update_note 排队，第一条 PUT 成功 → 服务端
  /// version+1 + 回填本地 → 第二条仍带旧 baseVersion → 服务端
  /// `cur.Version != IfMatchVersion` → 409。这是**单机自伤**（非真多端冲突），
  /// 全量快照语义下只有最新一条有意义。
  ///
  /// 合并：按 entityId 分组，组内多条折叠进最老那行 —— payloadJson 字段并集
  /// （id 大的盖 id 小的），baseVersion 取最老行的（= 该笔记上次 flush 成功
  /// 回填的服务端 version），attempts/nextAttemptAt 重置、lastError 清零，删
  /// 其余行。合并后同笔记队列 ≤1 条 update_note → 单次 PUT → 无自伤 409。
  ///
  /// payload 合并安全性：所有 update_note 都是同笔记的全意图 patch（presence
  /// = set，absence = don't-change）。字段并集 + 新值盖旧 = 折叠后的正确状态。
  /// notebook_id='' 是 moveToRoot 哨兵，作为普通值覆盖也正确（最新意图胜）。
  Future<void> compactUpdateNotes() async {
    await _db.transaction(() async {
      final rows = await (_db.select(_db.noteOutbox)
            ..where((r) =>
                r.op.equals('update_note') & r.ownerKey.equals(scope))
            ..orderBy([(r) => OrderingTerm(expression: r.id)]))
          .get();
      final byEntity = <String, List<NoteOutboxEntry>>{};
      for (final r in rows) {
        byEntity.putIfAbsent(r.entityId, () => []).add(r);
      }
      final now = DateTime.now().toUtc();
      for (final group in byEntity.values) {
        if (group.length < 2) continue;
        final merged = <String, dynamic>{};
        for (final row in group) {
          final p = jsonDecode(row.payloadJson) as Map<String, dynamic>;
          merged.addAll(p); // id 升序遍历，后者盖前者
        }
        final oldest = group.first;
        await (_db.update(_db.noteOutbox)
              ..where((r) => r.id.equals(oldest.id) & r.ownerKey.equals(scope)))
            .write(NoteOutboxCompanion(
          payloadJson: Value(jsonEncode(merged)),
          attempts: const Value(0),
          nextAttemptAt: Value(now),
          lastError: const Value(null),
        ));
        for (final row in group.skip(1)) {
          await (_db.delete(_db.noteOutbox)
                ..where((r) => r.id.equals(row.id) & r.ownerKey.equals(scope)))
              .go();
        }
      }
    });
  }

  /// flusher 成功 flush 一条 update_note 后调用：把同笔记**其余** pending
  /// update_note 行的 baseVersion 抬到服务端刚回填的新 version。
  ///
  /// 必要性：compact 只折叠 flush **起点**已存在的行。flush _applyOne 网络
  /// 往返期间，autosave 仍可插入新 update_note（compact 已跑完，不再合并）。
  /// 这条新行 baseVersion 取自本地 note.version —— 而本地 version 在
  /// _applyOne 返回前还没被 _upsertFromDto 回填，仍是旧值 → 下轮 flush 必
  /// 409（自伤）。cascade 在成功后立即抬剩余行，下轮 PUT 带新 baseVersion
  /// 必中。已 delete 的当前行也在 UPDATE 范围内，但紧接着被 flushOnce 删除，
  /// 抬一个将死行无副作用。
  ///
  /// 仅成功路径调用：409 不 cascade（服务端非因本 op 前进，剩余行陈旧是真
  /// 他端冲突，应各自 409 走用户裁决）。
  Future<void> bumpOutboxBaseVersion(String entityId, int newVersion) async {
    await (_db.update(_db.noteOutbox)
          ..where((r) =>
              r.entityId.equals(entityId) &
              r.op.equals('update_note') &
              r.ownerKey.equals(scope)))
        .write(NoteOutboxCompanion(baseVersion: Value(newVersion)));
  }

  /// When a create_* op succeeds with a new server id we have to rewrite any
  /// queued ops that referenced the placeholder — entityId/notebookId 两列
  /// 以及 payloadJson 里的引用（create_note/update_note 的 notebook_id、
  /// set_note_tags 的 tag_ids 列表）。id 是 uuid 形态，在 JSON 里只会以
  /// 完整字符串值出现（content_md 等内容里的引号已被转义），所以精确
  /// 替换 '"old"' → '"new"' 是安全的。全部限定当前 scope。
  Future<void> rekeyOutbox({
    required String oldEntityId,
    required String newEntityId,
  }) async {
    await _db.transaction(() async {
      await (_db.update(_db.noteOutbox)
            ..where((r) =>
                r.entityId.equals(oldEntityId) & r.ownerKey.equals(scope)))
          .write(NoteOutboxCompanion(entityId: Value(newEntityId)));
      await (_db.update(_db.noteOutbox)
            ..where((r) =>
                r.notebookId.equals(oldEntityId) & r.ownerKey.equals(scope)))
          .write(NoteOutboxCompanion(notebookId: Value(newEntityId)));
      final stale = await (_db.select(_db.noteOutbox)
            ..where((r) =>
                r.payloadJson.like('%$oldEntityId%') &
                r.ownerKey.equals(scope)))
          .get();
      for (final row in stale) {
        final patched =
            row.payloadJson.replaceAll('"$oldEntityId"', '"$newEntityId"');
        if (patched == row.payloadJson) continue;
        await (_db.update(_db.noteOutbox)
              ..where((r) => r.id.equals(row.id) & r.ownerKey.equals(scope)))
            .write(NoteOutboxCompanion(payloadJson: Value(patched)));
      }
    });
  }

  /// Wipe everything — used on logout and in tests. 不按 scope 过滤：
  /// 登出语义就是清掉本域全部本地行（与 wiki/aigc 同）。
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
