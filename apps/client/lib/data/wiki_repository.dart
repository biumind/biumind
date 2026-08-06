// WikiRepository — local-first façade over Drift + WikiClient.
//
// Reads always come from Drift (so the UI works offline); writes are
// optimistically applied to Drift first, then persisted to the outbox, then
// flushed against the network. The flusher (in `outbox/wiki_outbox_flusher.dart`)
// drains the outbox in the background.
//
// This is the only layer in the app that knows both about server DTOs
// (`api.WikiProject` etc.) and Drift rows (`LocalWikiProject` etc.). Everything
// upstream — controllers, widgets — sees a uniform set of data classes
// (`RepoProject`, `RepoPage`, `RepoBlock`) that include sync state.

import 'dart:async';
import 'dart:convert';

import 'package:drift/drift.dart' show Value;
import 'package:meta/meta.dart';
import 'package:uuid/uuid.dart';

import 'api/wiki_client.dart' as api;
import 'local/db.dart';
import 'local/wiki_dao.dart';


@immutable
class RepoProject {
  final String id;
  final String name;
  final bool pendingCreate;
  const RepoProject({
    required this.id,
    required this.name,
    this.pendingCreate = false,
  });

  factory RepoProject.fromLocal(LocalWikiProject row, {bool pendingCreate = false}) =>
      RepoProject(id: row.id, name: row.name, pendingCreate: pendingCreate);
}

@immutable
class RepoPage {
  final String id;
  final String projectId;
  final String title;
  final int version;
  final String? parentId;
  final DateTime updatedAt;
  final bool pendingCreate;

  /// Server-side frontmatter（type / path / tags / sources / related / 任意键）。
  /// B1.x：当前仅在 [WikiRepository] 内存 cache 维护，未持久化到 Drift；
  /// listPages/getPage 网络刷新时回填，应用重启后清空（重新登录 listPages
  /// 拉一次即可恢复）。后续 schema migration 后改为本地表 frontmatter_json 列。
  final Map<String, dynamic> frontmatter;

  const RepoPage({
    required this.id,
    required this.projectId,
    required this.title,
    required this.version,
    this.parentId,
    required this.updatedAt,
    this.pendingCreate = false,
    this.frontmatter = const <String, dynamic>{},
  });

  factory RepoPage.fromLocal(
    LocalWikiPage row, {
    bool pendingCreate = false,
    Map<String, dynamic> frontmatter = const <String, dynamic>{},
  }) =>
      RepoPage(
        id: row.id,
        projectId: row.projectId,
        title: row.title,
        version: row.version,
        parentId: row.parentId,
        updatedAt: row.updatedAt,
        pendingCreate: pendingCreate,
        frontmatter: frontmatter,
      );
}

@immutable
class RepoBlock {
  final String id;
  final String pageId;
  final double position;
  final String type;
  final Map<String, dynamic> content;
  final int version;
  final bool pendingCreate;
  final bool pendingUpdate;
  const RepoBlock({
    required this.id,
    required this.pageId,
    required this.position,
    required this.type,
    required this.content,
    required this.version,
    this.pendingCreate = false,
    this.pendingUpdate = false,
  });

  factory RepoBlock.fromLocal(
    LocalWikiBlock row, {
    bool pendingCreate = false,
    bool pendingUpdate = false,
  }) =>
      RepoBlock(
        id: row.id,
        pageId: row.pageId,
        position: row.position,
        type: row.type,
        content: jsonDecode(row.contentJson) as Map<String, dynamic>,
        version: row.version,
        pendingCreate: pendingCreate,
        pendingUpdate: pendingUpdate,
      );
}

/// Outbox op codes — kept as strings so they survive schema migrations.
class OutboxOp {
  static const createProject = 'create_project';
  static const createPage = 'create_page';
  static const createBlock = 'create_block';
  static const updateBlock = 'update_block';
  static const deleteBlock = 'delete_block';
}

class WikiRepository {
  WikiRepository({
    required this.dao,
    required this.client,
    Uuid? uuid,
  }) : _uuid = uuid ?? const Uuid();

  final WikiDao dao;
  final api.WikiClient client;
  final Uuid _uuid;

  /// 内存 frontmatter cache（B1.x 临时方案；schema migration 后下沉 Drift）。
  /// listPages/getPage 后由 [_cacheFrontmatter] 回填；watchPages 流出 RepoPage
  /// 时从此处读注入。重启清空（无持久化）；listPages 重拉即可恢复。
  final Map<String, Map<String, dynamic>> _frontmatter =
      <String, Map<String, dynamic>>{};

  void _cacheFrontmatter(String pageId, Map<String, dynamic> fm) {
    if (fm.isEmpty) {
      _frontmatter.remove(pageId);
    } else {
      _frontmatter[pageId] = fm;
    }
  }

  Map<String, dynamic> _frontmatterFor(String pageId) =>
      _frontmatter[pageId] ?? const <String, dynamic>{};

  // ─── Reads ───────────────────────────────────────────────

  Stream<List<RepoProject>> watchProjects() async* {
    final pendingIds = await _pendingCreateIds(OutboxOp.createProject);
    yield* dao.watchProjects().map((rows) => rows
        .map((r) => RepoProject.fromLocal(r, pendingCreate: pendingIds.contains(r.id)))
        .toList());
  }

  Stream<List<RepoPage>> watchPages(String projectId) async* {
    final pendingIds = await _pendingCreateIds(OutboxOp.createPage);
    yield* dao.watchPages(projectId).map((rows) => rows
        .map((r) => RepoPage.fromLocal(
              r,
              pendingCreate: pendingIds.contains(r.id),
              frontmatter: _frontmatterFor(r.id),
            ))
        .toList());
  }

  Stream<List<RepoBlock>> watchBlocks(String pageId) async* {
    final outbox = await dao.allOutbox();
    final pendingCreate = <String>{
      for (final e in outbox)
        if (e.op == OutboxOp.createBlock) e.entityId,
    };
    final pendingUpdate = <String>{
      for (final e in outbox)
        if (e.op == OutboxOp.updateBlock) e.entityId,
    };
    yield* dao.watchBlocks(pageId).map((rows) => rows
        .map((r) => RepoBlock.fromLocal(
              r,
              pendingCreate: pendingCreate.contains(r.id),
              pendingUpdate: pendingUpdate.contains(r.id),
            ))
        .toList());
  }

  Stream<int> watchPendingCount() => dao.watchOutboxCount();

  Future<Set<String>> _pendingCreateIds(String op) async {
    final outbox = await dao.allOutbox();
    return {
      for (final e in outbox)
        if (e.op == op) e.entityId,
    };
  }

  // ─── Refresh from server ─────────────────────────────────

  /// Pulls projects/pages/blocks for the given project and writes them into
  /// Drift. Safe to call on every login + whenever the user opens the page.
  /// Network failures are intentionally swallowed — local cache is the
  /// source of truth for the UI.
  Future<void> refreshProjects() async {
    final projects = await client.listProjects();
    await dao.upsertProjects([
      for (final p in projects)
        LocalWikiProject(
          id: p.id,
          name: p.name,
          updatedAt: DateTime.now().toUtc(),
        ),
    ]);
  }

  Future<void> refreshPages(String projectId) async {
    final pages = await client.listPages(projectId);
    await dao.upsertPages([
      for (final p in pages)
        LocalWikiPage(
          id: p.id,
          projectId: p.projectId,
          title: p.title,
          version: p.version,
          parentId: p.parentId,
          updatedAt: p.updatedAt,
        ),
    ]);
    // B1.x: frontmatter 走内存 cache，等下次 watchPages tick 注入到 RepoPage。
    for (final p in pages) {
      _cacheFrontmatter(p.id, p.frontmatter);
    }
  }

  Future<void> refreshBlocks(String projectId, String pageId) async {
    final blocks = await client.listBlocks(projectId, pageId);
    await dao.upsertBlocks([
      for (final b in blocks)
        LocalWikiBlock(
          id: b.id,
          pageId: b.pageId,
          position: b.position,
          type: b.type,
          contentJson: jsonEncode(b.content),
          version: b.version,
          deleted: false,
          updatedAt: DateTime.now().toUtc(),
        ),
    ]);
  }

  /// §⑤ 取 page 含 body_md（编辑器首屏正文）。本地 Drift 不存 body_md，
  /// 故编辑器经此拉 server 权威值。
  Future<api.WikiPage> getPage(String projectId, String pageId) =>
      client.getPage(projectId, pageId);

  // ─── Revisions (页版本历史，迁移 00065) ────────────────────
  //
  // revisions 本身纯服务端（本地不镜像），与 note 一致；restore 后本地 Drift
  // 须对账（server in-place 软删的 block 要本地标删，否则编辑器 watch 流显 stale）。

  Future<List<api.WikiPageRevision>> listPageRevisions(
    String projectId,
    String pageId, {
    int? limit,
    int? offset,
  }) =>
      client.listPageRevisions(projectId, pageId, limit: limit, offset: offset);

  Future<api.WikiPageRevision> getPageRevision(
          String projectId, String pageId, String revisionId) =>
      client.getPageRevision(projectId, pageId, revisionId);

  /// 覆盖式恢复：server 对账 blocks（update/revive/soft-delete/insert）后，
  /// 本地 Drift 同步——upsert server live blocks + 标删本地多余的（restore 软删的），
  /// 再 upsert page 行（title/frontmatter/version 变了）。
  Future<void> restorePageRevision(
    String projectId,
    String pageId,
    String revisionId,
  ) async {
    final page = await client.restorePageRevision(projectId, pageId, revisionId);
    await _reconcileBlocksFromServer(projectId, pageId);
    await dao.upsertPages([
      LocalWikiPage(
        id: page.id,
        projectId: page.projectId,
        title: page.title,
        version: page.version,
        parentId: page.parentId,
        updatedAt: page.updatedAt,
      ),
    ]);
    _cacheFrontmatter(page.id, page.frontmatter);
  }

  /// §⑤ body_md 权威写：client PUT body → server reconcile blocks 投影 →
  /// 本地 Drift 对账 blocks（_reconcileBlocksFromServer）+ upsert page（version 刷新）。
  /// 编辑器经 syncws page.updated 流自动收远端变更；本地 blocks 同步供 read 模式 reader。
  Future<void> updatePageBody(
    String projectId,
    String pageId,
    String bodyMd,
    int ifMatchVersion,
  ) async {
    final page = await client.updatePageBody(
      projectId, pageId,
      ifMatchVersion: ifMatchVersion, bodyMd: bodyMd,
    );
    await _reconcileBlocksFromServer(projectId, pageId);
    await dao.upsertPages([
      LocalWikiPage(
        id: page.id,
        projectId: page.projectId,
        title: page.title,
        version: page.version,
        parentId: page.parentId,
        updatedAt: page.updatedAt,
      ),
    ]);
    _cacheFrontmatter(page.id, page.frontmatter);
  }

  /// 另存为新页：server 建页 + 复制 blocks，本地 upsert 新页行（page 列表
  /// watch 流自动刷新出现新页）。
  Future<void> savePageRevisionAsCopy(
    String projectId,
    String pageId,
    String revisionId,
  ) async {
    final page = await client.savePageRevisionAsCopy(projectId, pageId, revisionId);
    await dao.upsertPages([
      LocalWikiPage(
        id: page.id,
        projectId: page.projectId,
        title: page.title,
        version: page.version,
        parentId: page.parentId,
        updatedAt: page.updatedAt,
      ),
    ]);
    _cacheFrontmatter(page.id, page.frontmatter);
  }

  /// 拉 server live blocks 对账本地 Drift：upsert server 集合 + 标删本地多余
  /// （restore 软删了某些 block；refreshBlocks 只 upsert 不删，故 restore 专用此法）。
  Future<void> _reconcileBlocksFromServer(String projectId, String pageId) async {
    final server = await client.listBlocks(projectId, pageId);
    final serverIds = <String>{for (final b in server) b.id};
    await dao.upsertBlocks([
      for (final b in server)
        LocalWikiBlock(
          id: b.id,
          pageId: b.pageId,
          position: b.position,
          type: b.type,
          contentJson: jsonEncode(b.content),
          version: b.version,
          deleted: false,
          updatedAt: DateTime.now().toUtc(),
        ),
    ]);
    final local = await dao.listBlocks(pageId);
    for (final lb in local) {
      if (!serverIds.contains(lb.id) && !lb.deleted) {
        await dao.markBlockDeleted(lb.id);
      }
    }
  }

  /// Returns every non-deleted block belonging to a page in [projectId]
  /// from the local Drift mirror. Used by the in-page search to scan
  /// content offline. Network is never hit — searching only finds
  /// blocks the user has already loaded into a page (refreshBlocks is
  /// what populates them).
  Future<List<RepoBlock>> listBlocksForProject(String projectId) async {
    final rows = await dao.listBlocksByProject(projectId);
    return rows.map((r) => RepoBlock.fromLocal(r)).toList();
  }

  // ─── Writes (optimistic + outbox) ────────────────────────

  Future<RepoProject> createProject(String name, {String? templateId}) async {
    final id = 'local-${_uuid.v4()}';
    final now = DateTime.now().toUtc();
    await dao.upsertProject(LocalWikiProject(id: id, name: name, updatedAt: now));
    await dao.enqueueOutbox(WikiOutboxCompanion.insert(
      op: OutboxOp.createProject,
      entityId: id,
      payloadJson: jsonEncode({'name': name, 'template_id': templateId}),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoProject(id: id, name: name, pendingCreate: true);
  }

  Future<RepoPage> createPage(String projectId, {required String title}) async {
    final id = 'local-${_uuid.v4()}';
    final now = DateTime.now().toUtc();
    await dao.upsertPage(LocalWikiPage(
      id: id,
      projectId: projectId,
      title: title,
      version: 1,
      parentId: null,
      updatedAt: now,
    ));
    await dao.enqueueOutbox(WikiOutboxCompanion.insert(
      op: OutboxOp.createPage,
      entityId: id,
      projectId: Value(projectId),
      payloadJson: jsonEncode({'title': title}),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoPage(
      id: id,
      projectId: projectId,
      title: title,
      version: 1,
      updatedAt: now,
      pendingCreate: true,
    );
  }

  Future<RepoBlock> createBlock(
    String projectId,
    String pageId, {
    required String type,
    required double position,
    required Map<String, dynamic> content,
  }) async {
    final id = 'local-${_uuid.v4()}';
    final now = DateTime.now().toUtc();
    await dao.upsertBlock(LocalWikiBlock(
      id: id,
      pageId: pageId,
      position: position,
      type: type,
      contentJson: jsonEncode(content),
      version: 1,
      deleted: false,
      updatedAt: now,
    ));
    await dao.enqueueOutbox(WikiOutboxCompanion.insert(
      op: OutboxOp.createBlock,
      entityId: id,
      projectId: Value(projectId),
      pageId: Value(pageId),
      payloadJson: jsonEncode({
        'type': type,
        'position': position,
        'content': content,
      }),
      createdAt: now,
      nextAttemptAt: now,
    ));
    return RepoBlock(
      id: id,
      pageId: pageId,
      position: position,
      type: type,
      content: content,
      version: 1,
      pendingCreate: true,
    );
  }

  Future<void> updateBlock(
    String projectId,
    String blockId, {
    required Map<String, dynamic> content,
    double? position,
  }) async {
    final existing = await dao.blockById(blockId);
    if (existing == null) {
      throw StateError('block not found: $blockId');
    }
    final now = DateTime.now().toUtc();
    await dao.upsertBlock(LocalWikiBlock(
      id: existing.id,
      pageId: existing.pageId,
      position: position ?? existing.position,
      type: existing.type,
      contentJson: jsonEncode(content),
      version: existing.version,
      deleted: existing.deleted,
      updatedAt: now,
    ));
    final payload = <String, dynamic>{'content': content};
    if (position != null) payload['position'] = position;
    await dao.enqueueOutbox(WikiOutboxCompanion.insert(
      op: OutboxOp.updateBlock,
      entityId: blockId,
      projectId: Value(projectId),
      pageId: Value(existing.pageId),
      payloadJson: jsonEncode(payload),
      baseVersion: Value(existing.version),
      createdAt: now,
      nextAttemptAt: now,
    ));
  }

  Future<void> deleteBlock(String projectId, String blockId) async {
    final existing = await dao.blockById(blockId);
    if (existing == null) return;
    await dao.markBlockDeleted(blockId);
    final now = DateTime.now().toUtc();
    await dao.enqueueOutbox(WikiOutboxCompanion.insert(
      op: OutboxOp.deleteBlock,
      entityId: blockId,
      projectId: Value(projectId),
      pageId: Value(existing.pageId),
      payloadJson: '{}',
      createdAt: now,
      nextAttemptAt: now,
    ));
  }
}
