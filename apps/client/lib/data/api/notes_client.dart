// NotesClient — thin Dart client to Brain.Notes API.
//
// 笔记域与 Wiki 完全独立（设计 docs/BiuMind-Notes-Design-Draft.md §4 D2）。
// 镜像 WikiClient 的形态：构造拿 endpoint + bearerToken，transport 走
// _http_helpers.apiRequest（30s 超时 + 401 自动刷新 retry），错误统一包成
// NotesApiError（isVersionConflict = 409，供 flusher 冲突裁决）。
//
//	POST   /v1/notebooks                  create notebook
//	GET    /v1/notebooks                  list mine
//	PUT    /v1/notebooks/{id}             update (name/position)
//	DELETE /v1/notebooks/{id}             soft delete
//	POST   /v1/notes                      create (客户端可带 id，幂等)
//	GET    /v1/notes                      list (notebook_id/tag/todo 过滤)
//	GET    /v1/notes/{id}                 get one
//	PUT    /v1/notes/{id}                 update (If-Match version → 409)
//	DELETE /v1/notes/{id}                 soft delete (回收站)
//	GET    /v1/notes/trash                回收站列表
//	POST   /v1/notes/{id}/restore         还原
//	DELETE /v1/notes/{id}/purge           物理删除
//	POST   /v1/note-tags                  create tag (幂等)
//	GET    /v1/note-tags                  list mine
//	PUT    /v1/notes/{id}/tags            整组替换标签
//	GET    /v1/notes/changes?since=N      增量事件流（含 tombstone）
//	GET    /v1/notes/search?q=&limit=     全文搜索（snippet 带 <mark> 高亮）
//	GET    /v1/notes/{id}/revisions       版本历史列表（不含 content_md）
//	GET    /v1/notes/{id}/revisions/{rid} 单版本详情（含完整 title/content_md）
//	POST   /v1/notes/{id}/revisions/{rid}/restore      覆盖式恢复（服务端自动备份）
//	POST   /v1/notes/{id}/revisions/{rid}/save-as-copy 以该版本新建笔记
//	POST   /v1/notes/{id}/promote         转入知识库（归档笔记 + 建 wiki page，幂等）
//	POST   /v1/notes/{id}/unarchive       取消归档
//	PUT    /v1/notes/{id}/share           创建/更新分享（幂等；恢复已停用分享）
//	GET    /v1/notes/{id}/share           当前分享状态（无分享 → 404）
//	DELETE /v1/notes/{id}/share           停用分享（可恢复）
//	POST   /v1/notes/{id}/share/rotate    重置 token（旧链接作废）
//	GET    /v1/notes/shares               我的分享列表（设置页管理 + 列表徽标）

import '_http_helpers.dart';

class NoteNotebook {
  final String id;
  final String name;

  /// 父笔记本 id，null = 根级（多级目录，服务端 migration 00003）。
  final String? parentId;
  final double position;
  final DateTime updatedAt;

  const NoteNotebook({
    required this.id,
    required this.name,
    this.parentId,
    required this.position,
    required this.updatedAt,
  });

  factory NoteNotebook.fromJson(Map<String, dynamic> j) => NoteNotebook(
        id: j['id'] as String,
        name: j['name'] as String? ?? '',
        // 兼容缺省/null（旧服务端或无父本）—— 都归一为 null = 根级。
        parentId: j['parent_id'] as String?,
        position: (j['position'] as num? ?? 0).toDouble(),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      );
}

class NoteNote {
  final String id;
  final String? notebookId;
  final String title;
  final String contentMd;
  final bool isTodo;
  final DateTime? todoCompletedAt;
  final double position;
  final int version;
  final DateTime? deletedAt;
  final DateTime updatedAt;

  /// 剪藏来源 URL（webclip 建的笔记），可空。
  final String? sourceUrl;

  /// 剪藏原文作者，可空。
  final String? author;

  /// 归档时间（转入知识库后置位），null = 未归档。
  final DateTime? archivedAt;

  /// 转入知识库后对应的 wiki page id，null = 未转入。
  final String? promotedPageId;

  const NoteNote({
    required this.id,
    this.notebookId,
    required this.title,
    required this.contentMd,
    required this.isTodo,
    this.todoCompletedAt,
    required this.position,
    required this.version,
    this.deletedAt,
    required this.updatedAt,
    this.sourceUrl,
    this.author,
    this.archivedAt,
    this.promotedPageId,
  });

  factory NoteNote.fromJson(Map<String, dynamic> j) => NoteNote(
        id: j['id'] as String,
        notebookId: j['notebook_id'] as String?,
        title: j['title'] as String? ?? '',
        contentMd: j['content_md'] as String? ?? '',
        isTodo: j['is_todo'] as bool? ?? false,
        todoCompletedAt:
            DateTime.tryParse(j['todo_completed_at'] as String? ?? '')
                ?.toUtc(),
        position: (j['position'] as num? ?? 0).toDouble(),
        version: (j['version'] as num? ?? 1).toInt(),
        deletedAt:
            DateTime.tryParse(j['deleted_at'] as String? ?? '')?.toUtc(),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        sourceUrl: j['source_url'] as String?,
        author: j['author'] as String?,
        archivedAt:
            DateTime.tryParse(j['archived_at'] as String? ?? '')?.toUtc(),
        promotedPageId: j['promoted_page_id'] as String?,
      );
}

class NoteTag {
  final String id;
  final String name;

  const NoteTag({required this.id, required this.name});

  factory NoteTag.fromJson(Map<String, dynamic> j) => NoteTag(
        id: j['id'] as String,
        name: j['name'] as String? ?? '',
      );
}

/// One row from GET /v1/notes/changes —— brain.events scope=`note:user:<uid>`。
/// 删除也是一条事件（持久 tombstone）：note.deleted / note.purged。
class NoteChangeEvent {
  final int id;
  final String eventType;
  final String actorId;
  final Map<String, dynamic> payload;
  final DateTime createdAt;

  const NoteChangeEvent({
    required this.id,
    required this.eventType,
    required this.actorId,
    required this.payload,
    required this.createdAt,
  });

  factory NoteChangeEvent.fromJson(Map<String, dynamic> j) => NoteChangeEvent(
        id: (j['id'] as num?)?.toInt() ?? 0,
        eventType: (j['event_type'] as String?) ?? '',
        actorId: (j['actor_id'] as String?) ?? '',
        payload: ((j['payload'] as Map?) ?? const {}).cast<String, dynamic>(),
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      );
}

/// changes 响应：events + latest（用户 scope 当前最大事件 id；即使本次
/// 增量为空也要把游标推进到 latest）。
class NoteChangesPage {
  final List<NoteChangeEvent> events;
  final int latest;

  const NoteChangesPage({required this.events, required this.latest});
}

/// GET /v1/notes/search 的单条命中。
///
/// [snippet] 是服务端生成的命中片段：笔记内容已做 HTML 转义，命中词包在
/// `<mark>...</mark>` 里 —— UI 渲染时需解析/剥离标签（见 notes_home_page
/// 的 searchSnippetSpans），不要原样显示尖括号。
class NoteSearchResult {
  final String id;
  final String? notebookId;
  final String title;
  final bool isTodo;
  final DateTime? todoCompletedAt;
  final DateTime updatedAt;
  final String snippet;
  final double rank;

  const NoteSearchResult({
    required this.id,
    this.notebookId,
    required this.title,
    required this.isTodo,
    this.todoCompletedAt,
    required this.updatedAt,
    required this.snippet,
    required this.rank,
  });

  factory NoteSearchResult.fromJson(Map<String, dynamic> j) =>
      NoteSearchResult(
        id: j['id'] as String,
        notebookId: j['notebook_id'] as String?,
        title: j['title'] as String? ?? '',
        isTodo: j['is_todo'] as bool? ?? false,
        todoCompletedAt:
            DateTime.tryParse(j['todo_completed_at'] as String? ?? '')
                ?.toUtc(),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        snippet: j['snippet'] as String? ?? '',
        rank: (j['rank'] as num? ?? 0).toDouble(),
      );
}

/// GET /v1/notes/{id}/revisions 的单条版本记录。
///
/// 列表响应不含 content_md（只有元信息）；[contentMd] 仅在
/// GET /v1/notes/{id}/revisions/{rid} 详情响应里有值。
class NoteRevision {
  final String id;
  final String noteId;
  final String title;

  /// 版本来源：'edit'（编辑快照）/ 'restore'（恢复前服务端自动备份的
  /// 恢复点）。UI 显示为「编辑」/「恢复点」。
  final String changeType;
  final String changeSummary;
  final DateTime createdAt;
  final String? contentMd;

  const NoteRevision({
    required this.id,
    required this.noteId,
    required this.title,
    required this.changeType,
    required this.changeSummary,
    required this.createdAt,
    this.contentMd,
  });

  factory NoteRevision.fromJson(Map<String, dynamic> j) => NoteRevision(
        id: j['id'] as String,
        noteId: j['note_id'] as String? ?? '',
        title: j['title'] as String? ?? '',
        changeType: j['change_type'] as String? ?? '',
        changeSummary: j['change_summary'] as String? ?? '',
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        contentMd: j['content_md'] as String?,
      );
}

/// POST /v1/notes/{id}/promote 的响应：新建的 wiki page + 已归档的笔记。
/// page 的 schema 归 wiki 域管，这里保留原始 map（客户端只需要知道成功
/// 与归档后的 note）。
class NotePromoteResult {
  final Map<String, dynamic> page;
  final NoteNote note;

  const NotePromoteResult({required this.page, required this.note});
}

/// 分享状态机（S1 契约：active / disabled / expired；S2 + exhausted）。
/// 推导规则与 brain 服务端一致（优先级 disabled > exhausted > expired >
/// active）：disabled_at 非空 → disabled；max_views 非空且
/// view_count >= max_views → exhausted；expires_at 已过 → expired；
/// 否则 active。[now] 显式传入以便单测。
enum NoteShareStatus { active, disabled, expired, exhausted }

NoteShareStatus noteShareStatusOf({
  required DateTime? disabledAt,
  required DateTime? expiresAt,
  int viewCount = 0,
  int? maxViews,
  required DateTime now,
}) {
  if (disabledAt != null) return NoteShareStatus.disabled;
  if (maxViews != null && viewCount >= maxViews) {
    return NoteShareStatus.exhausted;
  }
  if (expiresAt != null && !expiresAt.isAfter(now)) {
    return NoteShareStatus.expired;
  }
  return NoteShareStatus.active;
}

NoteShareStatus noteShareStatusFromString(String s) => switch (s) {
      'disabled' => NoteShareStatus.disabled,
      'expired' => NoteShareStatus.expired,
      'exhausted' => NoteShareStatus.exhausted,
      _ => NoteShareStatus.active,
    };

/// 管理端 share 对象 —— S1 冻结契约（docs/BiuMind-Technical-Architecture.md
/// §7.6「API 契约」）：所有管理端接口的返回体。**服务端不返回 url 字段**，
/// 分享 URL 由客户端用 origin 自行拼接 `${origin}/s/${token}`。
class NoteShare {
  final String token;
  final bool passwordSet;
  final DateTime? expiresAt;
  final int credentialVersion;
  final int viewCount;

  /// 访问次数上限（S2）；null = 不限。view_count >= max_views 时分享
  /// 进入 exhausted（链接 410）。
  final int? maxViews;
  final DateTime? disabledAt;
  final DateTime createdAt;
  final DateTime updatedAt;

  const NoteShare({
    required this.token,
    required this.passwordSet,
    this.expiresAt,
    required this.credentialVersion,
    required this.viewCount,
    this.maxViews,
    this.disabledAt,
    required this.createdAt,
    required this.updatedAt,
  });

  factory NoteShare.fromJson(Map<String, dynamic> j) => NoteShare(
        token: j['token'] as String? ?? '',
        passwordSet: j['password_set'] as bool? ?? false,
        expiresAt:
            DateTime.tryParse(j['expires_at'] as String? ?? '')?.toUtc(),
        credentialVersion: (j['credential_version'] as num? ?? 1).toInt(),
        viewCount: (j['view_count'] as num? ?? 0).toInt(),
        maxViews: (j['max_views'] as num?)?.toInt(),
        disabledAt:
            DateTime.tryParse(j['disabled_at'] as String? ?? '')?.toUtc(),
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      );

  /// 单篇分享状态（GET /v1/notes/{id}/share 响应不带 status 字段，客户端
  /// 按同一状态机推导；列表接口直接用服务端给的 status，见
  /// [NoteShareListItem]）。
  NoteShareStatus status(DateTime now) => noteShareStatusOf(
        disabledAt: disabledAt,
        expiresAt: expiresAt,
        viewCount: viewCount,
        maxViews: maxViews,
        now: now,
      );
}

/// GET /v1/notes/shares 列表项 = share 对象 + note_id / note_title /
/// status（status 由服务端计算，客户端直接用，不重复推导）。
class NoteShareListItem {
  final NoteShare share;
  final String noteId;
  final String noteTitle;
  final NoteShareStatus status;

  const NoteShareListItem({
    required this.share,
    required this.noteId,
    required this.noteTitle,
    required this.status,
  });

  factory NoteShareListItem.fromJson(Map<String, dynamic> j) =>
      NoteShareListItem(
        share: NoteShare.fromJson(j),
        noteId: j['note_id'] as String? ?? '',
        noteTitle: j['note_title'] as String? ?? '',
        status: noteShareStatusFromString(j['status'] as String? ?? ''),
      );
}

class NotesClient {
  NotesClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  // ─── Notebooks ───────────────────────────────────────────

  Future<List<NoteNotebook>> listNotebooks() async {
    final raw = await _get('/v1/notebooks');
    final list =
        (raw['notebooks'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteNotebook.fromJson).toList();
  }

  Future<NoteNotebook> createNotebook(String name,
      {double? position, String? parentId}) async {
    final body = <String, dynamic>{'name': name};
    if (position != null) body['position'] = position;
    if (parentId != null) body['parent_id'] = parentId;
    final raw = await _post('/v1/notebooks', body);
    return NoteNotebook.fromJson(raw);
  }

  /// 更新。parentId presence 语义对齐服务端：不传 = 不动；传 '' = 升到根；
  /// 传 uuid = 移到该父本（同 updateNote 的 notebookId 惯例）。
  Future<NoteNotebook> updateNotebook(
    String id, {
    String? name,
    double? position,
    String? parentId,
  }) async {
    final body = <String, dynamic>{};
    if (name != null) body['name'] = name;
    if (position != null) body['position'] = position;
    if (parentId != null) body['parent_id'] = parentId; // '' = 升到根
    final raw = await _put('/v1/notebooks/$id', body);
    return NoteNotebook.fromJson(raw);
  }

  Future<void> deleteNotebook(String id) async {
    await _delete('/v1/notebooks/$id');
  }

  // ─── Notes ───────────────────────────────────────────────

  /// 列表（只看活笔记）。notebookId='root' 由 [rootOnly] 表达；tag 是标签名。
  /// [archivedOnly] = true 时走 `?archived=only` 只看已归档笔记（默认
  /// 列表服务端已排除归档）。
  Future<List<NoteNote>> listNotes({
    String? notebookId,
    bool rootOnly = false,
    String? tag,
    bool todoOnly = false,
    bool archivedOnly = false,
    int? limit,
    int? offset,
  }) async {
    final params = <String, String>{};
    if (rootOnly) {
      params['notebook_id'] = 'root';
    } else if (notebookId != null) {
      params['notebook_id'] = notebookId;
    }
    if (tag != null && tag.isNotEmpty) params['tag'] = tag;
    if (todoOnly) params['todo'] = 'true';
    if (archivedOnly) params['archived'] = 'only';
    if (limit != null) params['limit'] = '$limit';
    if (offset != null) params['offset'] = '$offset';
    final raw = await _get('/v1/notes', query: params);
    final list =
        (raw['notes'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteNote.fromJson).toList();
  }

  Future<NoteNote> getNote(String id) async {
    final raw = await _get('/v1/notes/$id');
    return NoteNote.fromJson(raw);
  }

  Future<List<NoteNote>> listTrash({int? limit}) async {
    final params = <String, String>{};
    if (limit != null) params['limit'] = '$limit';
    final raw = await _get('/v1/notes/trash', query: params);
    final list =
        (raw['notes'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteNote.fromJson).toList();
  }

  /// 创建。服务端幂等：同 id（客户端 uuid）重放直接返回已存在记录。
  /// [id] 传客户端预生成的 uuid 时随 body 上送，服务端直接使用该 id ——
  /// 本地行从创建起就持有最终 id，flush 后无需 rekey，UI 持有的引用
  /// 不会失效。不传则由服务端分配（历史 `local-<uuid>` 占位行的恢复路径）。
  Future<NoteNote> createNote({
    String? id,
    String? notebookId,
    required String title,
    String contentMd = '',
    bool isTodo = false,
    DateTime? todoCompletedAt,
    double? position,
  }) async {
    final body = <String, dynamic>{
      'title': title,
      'content_md': contentMd,
      'is_todo': isTodo,
    };
    if (id != null) body['id'] = id;
    if (notebookId != null) body['notebook_id'] = notebookId;
    if (todoCompletedAt != null) {
      body['todo_completed_at'] = todoCompletedAt.toUtc().toIso8601String();
    }
    if (position != null) body['position'] = position;
    final raw = await _post('/v1/notes', body);
    return NoteNote.fromJson(raw);
  }

  /// 更新。乐观锁：[ifMatchVersion] 走 If-Match 头，版本不匹配服务端
  /// 返回 409（body 带 current_version + current 快照），客户端做用户裁决
  /// （设计 §4 D4）。
  ///
  /// 字段 presence 语义对齐服务端：notebookId 传 '' = 移回根；
  /// [clearTodoCompleted] = true 时清完成时间。
  Future<NoteNote> updateNote(
    String id, {
    required int ifMatchVersion,
    String? title,
    String? contentMd,
    String? notebookId,
    bool? isTodo,
    DateTime? todoCompletedAt,
    bool clearTodoCompleted = false,
    double? position,
  }) async {
    final body = <String, dynamic>{};
    if (title != null) body['title'] = title;
    if (contentMd != null) body['content_md'] = contentMd;
    if (notebookId != null) body['notebook_id'] = notebookId; // '' = 移回根
    if (isTodo != null) body['is_todo'] = isTodo;
    if (clearTodoCompleted) {
      body['todo_completed_at'] = '';
    } else if (todoCompletedAt != null) {
      body['todo_completed_at'] = todoCompletedAt.toUtc().toIso8601String();
    }
    if (position != null) body['position'] = position;
    final raw = await _put(
      '/v1/notes/$id',
      body,
      headers: {'If-Match': ifMatchVersion.toString()},
    );
    return NoteNote.fromJson(raw);
  }

  /// 进回收站（软删）。
  Future<void> trashNote(String id) async {
    await _delete('/v1/notes/$id');
  }

  Future<NoteNote> restoreNote(String id) async {
    final raw = await _post('/v1/notes/$id/restore', const {});
    return NoteNote.fromJson(raw);
  }

  Future<void> purgeNote(String id) async {
    await _delete('/v1/notes/$id/purge');
  }

  // ─── Revisions (版本历史) ─────────────────────────────────

  /// 版本历史列表（不含 content_md），按时间倒序由服务端保证。
  Future<List<NoteRevision>> listRevisions(
    String noteId, {
    int? limit,
    int? offset,
  }) async {
    final params = <String, String>{};
    if (limit != null) params['limit'] = '$limit';
    if (offset != null) params['offset'] = '$offset';
    final raw = await _get('/v1/notes/$noteId/revisions', query: params);
    final list =
        (raw['revisions'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteRevision.fromJson).toList();
  }

  /// 单版本详情（含完整 title/content_md）。
  Future<NoteRevision> getRevision(String noteId, String revisionId) async {
    final raw = await _get('/v1/notes/$noteId/revisions/$revisionId');
    return NoteRevision.fromJson(raw);
  }

  /// 覆盖式恢复到该版本（服务端恢复前自动备份当前状态为恢复点），
  /// 返回更新后的 note。
  Future<NoteNote> restoreRevision(String noteId, String revisionId) async {
    final raw =
        await _post('/v1/notes/$noteId/revisions/$revisionId/restore', const {});
    return NoteNote.fromJson(raw);
  }

  /// 以该版本新建笔记（同笔记本、复制标签、标题加「（历史副本）」），
  /// 返回新 note。
  Future<NoteNote> saveRevisionAsCopy(
      String noteId, String revisionId) async {
    final raw = await _post(
        '/v1/notes/$noteId/revisions/$revisionId/save-as-copy', const {});
    return NoteNote.fromJson(raw);
  }

  // ─── Archive / Promote (归档 / 转知识库) ───────────────────

  /// 转入知识库：笔记归档 + 在 [projectId] 下建 wiki page。服务端幂等，
  /// 重复调用返回同一 page。
  Future<NotePromoteResult> promoteNote(
      String noteId, String projectId) async {
    final raw = await _post('/v1/notes/$noteId/promote', {
      'project_id': projectId,
    });
    return NotePromoteResult(
      page: (raw['page'] as Map? ?? const {}).cast<String, dynamic>(),
      note: NoteNote.fromJson(
          (raw['note'] as Map? ?? const {}).cast<String, dynamic>()),
    );
  }

  /// 取消归档，返回更新后的 note。
  Future<NoteNote> unarchiveNote(String noteId) async {
    final raw = await _post('/v1/notes/$noteId/unarchive', const {});
    return NoteNote.fromJson(raw);
  }

  // ─── Tags ────────────────────────────────────────────────

  Future<List<NoteTag>> listTags() async {
    final raw = await _get('/v1/note-tags');
    final list =
        (raw['tags'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteTag.fromJson).toList();
  }

  /// 创建标签（服务端 (scope_key, lower(name)) 幂等，重名直接返回已存在）。
  Future<NoteTag> createTag(String name) async {
    final raw = await _post('/v1/note-tags', {'name': name});
    return NoteTag.fromJson(raw);
  }

  /// 整组替换笔记的标签关联。
  Future<void> setNoteTags(String noteId, List<String> tagIds) async {
    await _put('/v1/notes/$noteId/tags', {'tag_ids': tagIds});
  }

  // ─── Changes (catchup) ───────────────────────────────────

  /// 增量事件流。since=0 从头拉；响应的 latest 用于推进本地游标
  /// （即使 events 为空）。
  Future<NoteChangesPage> changes(int since, {int? limit}) async {
    final params = <String, String>{'since': '$since'};
    if (limit != null) params['limit'] = '$limit';
    final raw = await _get('/v1/notes/changes', query: params);
    final events = (raw['events'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(NoteChangeEvent.fromJson)
        .toList();
    return NoteChangesPage(
      events: events,
      latest: (raw['latest'] as num?)?.toInt() ?? since,
    );
  }

  // ─── Search ──────────────────────────────────────────────

  /// 全文搜索（服务端契约：q 空 → 400；limit > 50 被服务端静默收敛为 50）。
  /// q 由 _request 的 queryParameters 统一 urlencode。
  Future<List<NoteSearchResult>> searchNotes(String q,
      {int limit = 20}) async {
    final raw = await _get('/v1/notes/search',
        query: {'q': q, 'limit': '$limit'});
    final list =
        (raw['results'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteSearchResult.fromJson).toList();
  }

  // ─── Share (笔记分享，S1) ────────────────────────────────

  /// 创建或更新分享（幂等，一篇笔记一条；对已停用分享 = 以原 token 恢复
  /// 并更新配置）。presence 语义（三个字段一致）：null = 字段缺省；
  /// [password] '' = 移除密码、有值 = 重设（服务端 bcrypt +
  /// credential_version+1）；[expiresIn]（1d/7d/30d/never）缺省 = 保持
  /// 现有 expires_at 不变（契约修订：原"每次必传"已放宽；新建分享缺省
  /// = never），只有用户真的切换有效期档位时才传；[maxViews]（S2）
  /// 正整数 = 设置/调整上限、`0` = 移除上限、缺省 = 保持不变。
  Future<NoteShare> putShare(
    String noteId, {
    String? password,
    String? expiresIn,
    int? maxViews,
  }) async {
    final body = <String, dynamic>{
      'expires_in': ?expiresIn,
      'password': ?password, // '' = 移除密码
      'max_views': ?maxViews, // 0 = 移除上限
    };
    final raw = await _put('/v1/notes/$noteId/share', body);
    return NoteShare.fromJson(raw);
  }

  /// 当前分享状态。无分享 → 服务端 404（NotesApiError.isNotFound），
  /// 由调用方归一为 null。
  Future<NoteShare> getShare(String noteId) async {
    final raw = await _get('/v1/notes/$noteId/share');
    return NoteShare.fromJson(raw);
  }

  /// 停用分享（链接立即 404，可经 putShare 恢复）。服务端 204。
  Future<void> deleteShare(String noteId) async {
    await _delete('/v1/notes/$noteId/share');
  }

  /// 重置 token：旧链接立即作废，credential_version+1，返回新 share 对象。
  Future<NoteShare> rotateShare(String noteId) async {
    final raw = await _post('/v1/notes/$noteId/share/rotate', const {});
    return NoteShare.fromJson(raw);
  }

  /// 我的分享列表（设置页管理列表 + 笔记列表徽标共用同一数据源）。
  Future<List<NoteShareListItem>> listShares() async {
    final raw = await _get('/v1/notes/shares');
    final list =
        (raw['shares'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(NoteShareListItem.fromJson).toList();
  }

  // ─── HTTP plumbing ───────────────────────────────────────

  Future<Map<String, dynamic>> _get(String path,
      {Map<String, String>? query}) async {
    return _request('GET', path, query: query);
  }

  Future<Map<String, dynamic>> _post(
      String path, Map<String, dynamic> body) async {
    return _request('POST', path, body: body);
  }

  Future<Map<String, dynamic>> _put(
    String path,
    Map<String, dynamic> body, {
    Map<String, String>? headers,
  }) async {
    return _request('PUT', path, body: body, extraHeaders: headers);
  }

  Future<void> _delete(String path) async {
    await _request('DELETE', path);
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, dynamic>? body,
    Map<String, String>? query,
    Map<String, String>? extraHeaders,
  }) async {
    var url = baseUrl.replace(path: path);
    if (query != null && query.isNotEmpty) {
      url = url.replace(queryParameters: query);
    }
    try {
      return await apiRequest(
        method: method,
        url: url,
        bearerToken: bearerToken,
        extraHeaders: extraHeaders,
        body: body,
      );
    } on ApiError catch (e) {
      throw NotesApiError(
          method: method, path: path, status: e.status, body: e.body);
    }
  }
}

class NotesApiError implements Exception {
  final String method;
  final String path;
  final int status;
  final String body;
  const NotesApiError({
    required this.method,
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isVersionConflict => status == 409;
  bool get isNotFound => status == 404;

  @override
  String toString() => 'NotesApiError $status $method $path: $body';
}
