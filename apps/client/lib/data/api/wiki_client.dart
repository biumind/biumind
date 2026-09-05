// WikiClient — thin Dart client to Brain.Wiki API.
//
// Mirrors apps/cli/biu/internal/client/wiki/wiki.go contract.
//
//	GET    /v1/wiki/projects                                list mine
//	POST   /v1/wiki/projects                                create
//	GET    /v1/wiki/projects/{pid}/pages                    list pages
//	POST   /v1/wiki/projects/{pid}/pages                    create page
//	GET    /v1/wiki/projects/{pid}/pages/{id}/blocks        list blocks
//	POST   /v1/wiki/projects/{pid}/pages/{id}/blocks        create block
//	PUT    /v1/wiki/projects/{pid}/blocks/{id}              update block (If-Match)
//	DELETE /v1/wiki/projects/{pid}/blocks/{id}              soft delete
//	GET    /v1/wiki/projects/{pid}/changes?since={id}       catchup events

import 'dart:async';
import '_http_helpers.dart';

class WikiProject {
  final String id;
  final String name;
  const WikiProject({required this.id, required this.name});

  factory WikiProject.fromJson(Map<String, dynamic> j) =>
      WikiProject(id: j['id'] as String, name: j['name'] as String);
}

class WikiPage {
  final String id;
  final String projectId;
  final String title;
  final int version;
  final String? parentId;
  final DateTime updatedAt;
  /// §⑤ Path C 权威正文 markdown（含字面 [[wikilink]]，渲染期才 wiki:// 重写）。
  /// 空串 = 未回填/空页，编辑器 fallback 到 blocksToMarkdown(blocks)。
  final String bodyMd;
  /// Free-form metadata: type / tags / created / origin / sources /
  /// related / arbitrary user keys. Empty map when unset.
  final Map<String, dynamic> frontmatter;

  const WikiPage({
    required this.id,
    required this.projectId,
    required this.title,
    required this.version,
    this.parentId,
    required this.updatedAt,
    this.bodyMd = '',
    this.frontmatter = const {},
  });

  factory WikiPage.fromJson(Map<String, dynamic> j) => WikiPage(
        id: j['id'] as String,
        projectId: j['project_id'] as String,
        title: j['title'] as String? ?? '',
        version: (j['version'] as num? ?? 1).toInt(),
        parentId: j['parent_id'] as String?,
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
        bodyMd: j['body_md'] as String? ?? '',
        frontmatter: ((j['frontmatter'] as Map?) ?? const {})
            .cast<String, dynamic>(),
      );
}

class WikiBlock {
  final String id;
  final String pageId;
  final double position;
  final String type;
  final Map<String, dynamic> content;
  final int version;

  const WikiBlock({
    required this.id,
    required this.pageId,
    required this.position,
    required this.type,
    required this.content,
    required this.version,
  });

  factory WikiBlock.fromJson(Map<String, dynamic> j) => WikiBlock(
        id: j['id'] as String,
        pageId: j['page_id'] as String,
        position: (j['position'] as num).toDouble(),
        type: j['type'] as String? ?? 'text',
        content: (j['content'] as Map?)?.cast<String, dynamic>() ?? const {},
        version: (j['version'] as num? ?? 1).toInt(),
      );
}

/// Wiki 页历史版本快照（迁移 00065）。镜像 NoteRevision，差异：正文非单字段，
/// [blocksJson] 是写前全部 live blocks 的序列化（list 响应为 null，仅 detail 有）。
/// changeType: 'edit'（写前快照，窗口合并 + 可清理）/ 'restore'（恢复前自动备份，永久）。
class WikiPageRevision {
  final String id;
  final String pageId;
  final String projectId;
  final String actorId;
  final String title;
  final String changeType;
  final String changeSummary;
  final DateTime createdAt;
  final Map<String, dynamic> frontmatter;

  /// 详情响应才有：写前 live blocks 序列化（List<Map>）。list 响应为 null。
  final List<Map<String, dynamic>>? blocksJson;

  const WikiPageRevision({
    required this.id,
    required this.pageId,
    required this.projectId,
    required this.actorId,
    required this.title,
    required this.changeType,
    required this.changeSummary,
    required this.createdAt,
    this.frontmatter = const {},
    this.blocksJson,
  });

  factory WikiPageRevision.fromJson(Map<String, dynamic> j) => WikiPageRevision(
        id: j['id'] as String,
        pageId: j['page_id'] as String? ?? '',
        projectId: j['project_id'] as String? ?? '',
        actorId: j['actor_id'] as String? ?? '',
        title: j['title'] as String? ?? '',
        changeType: j['change_type'] as String? ?? '',
        changeSummary: j['change_summary'] as String? ?? '',
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '')
                ?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
        frontmatter:
            ((j['frontmatter'] as Map?) ?? const {}).cast<String, dynamic>(),
        blocksJson: (j['blocks_json'] as List?)
            ?.cast<Map<String, dynamic>>(),
      );
}

/// One row from /changelog — a brain.events entry scoped to a page.
class WikiPageEvent {
  final int id;
  final String type;
  final String actorType;
  final String actorId;
  final Map<String, dynamic> payload;
  final DateTime createdAt;

  const WikiPageEvent({
    required this.id,
    required this.type,
    required this.actorType,
    required this.actorId,
    required this.payload,
    required this.createdAt,
  });

  factory WikiPageEvent.fromJson(Map<String, dynamic> j) => WikiPageEvent(
        id: (j['id'] as num?)?.toInt() ?? 0,
        type: (j['type'] as String?) ?? '',
        actorType: (j['actor_type'] as String?) ?? '',
        actorId: (j['actor_id'] as String?) ?? '',
        payload: ((j['payload'] as Map?) ?? const {})
            .cast<String, dynamic>(),
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '') ??
            DateTime.now(),
      );
}

/// One inbound `[[wikilink]]` reference returned by /backlinks.
class WikiBacklink {
  final String pageId;
  final String pageTitle;
  final String blockId;
  final String snippet;
  final DateTime updatedAt;

  const WikiBacklink({
    required this.pageId,
    required this.pageTitle,
    required this.blockId,
    required this.snippet,
    required this.updatedAt,
  });

  factory WikiBacklink.fromJson(Map<String, dynamic> j) => WikiBacklink(
        pageId: j['page_id'] as String,
        pageTitle: (j['page_title'] as String?) ?? '',
        blockId: (j['block_id'] as String?) ?? '',
        snippet: (j['snippet'] as String?) ?? '',
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '') ??
            DateTime.now(),
      );
}

/// Wiki source (uploaded document) metadata. Mirrors brain.wiki_sources
/// 列里 API 暴露的字段；二进制内容在 brain.files 里 file_id 可取。
class WikiSource {
  final String id;
  final String projectId;
  final String? fileId;
  final String relPath;
  final String filename;
  final String? mime;
  final int byteSize;
  final String parseStatus;
  final String? parseError;
  final String? externalId;
  final DateTime createdAt;
  final DateTime updatedAt;

  const WikiSource({
    required this.id,
    required this.projectId,
    required this.relPath,
    required this.filename,
    required this.byteSize,
    required this.parseStatus,
    required this.createdAt,
    required this.updatedAt,
    this.fileId,
    this.mime,
    this.parseError,
    this.externalId,
  });

  factory WikiSource.fromJson(Map<String, dynamic> j) => WikiSource(
        id: j['id'] as String,
        projectId: j['project_id'] as String,
        fileId: (j['file_id'] as String?)?.isNotEmpty == true
            ? j['file_id'] as String
            : null,
        relPath: j['rel_path'] as String? ?? '',
        filename: j['filename'] as String? ?? '',
        mime: j['mime'] as String?,
        byteSize: (j['byte_size'] as num?)?.toInt() ?? 0,
        parseStatus: j['parse_status'] as String? ?? 'queued',
        parseError: j['parse_error'] as String?,
        externalId: j['external_id'] as String?,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );

  bool get isParsing =>
      parseStatus == 'queued' || parseStatus == 'processing';
  bool get isError => parseStatus == 'error';
}

/// 项目内搜索响应。优先吃 fused（RRF 融合 + cross-encoder rerank 后的
/// 单一总序，P1-2）；后端不返 fused（老版本）时 fallback 三类独立数组
/// 跨类合并。[WikiSearchHit.kind] 区分图标 / 来源。
class WikiSearchResult {
  final String query;
  final List<WikiSearchHit> hits;

  const WikiSearchResult({required this.query, required this.hits});

  factory WikiSearchResult.fromJson(Map<String, dynamic> j) {
    final hits = <WikiSearchHit>[];
    final fused = (j['fused'] as List?) ?? const [];
    if (fused.isNotEmpty) {
      // Prefer the RRF-fused (+ cross-encoder reranked) list when present
      // (P1-2): the backend now fuses wiki+vector+graph for scope=wiki
      // too, so fused is the relevance-ranked single ordering. Falls
      // back to per-path arrays when fused is absent (older backend).
      for (final h in fused) {
        if (h is Map) hits.add(WikiSearchHit.fromFusedJson(h.cast()));
      }
    } else {
      for (final h in (j['wiki'] as List? ?? const [])) {
        if (h is Map) hits.add(WikiSearchHit.fromWikiJson(h.cast()));
      }
      for (final h in (j['vector'] as List? ?? const [])) {
        if (h is Map) hits.add(WikiSearchHit.fromVectorJson(h.cast()));
      }
      for (final h in (j['graph'] as List? ?? const [])) {
        if (h is Map) hits.add(WikiSearchHit.fromGraphJson(h.cast()));
      }
      hits.sort((a, b) => b.score.compareTo(a.score));
    }
    return WikiSearchResult(
      query: j['query'] as String? ?? '',
      hits: hits,
    );
  }
}

/// 一条搜索命中。kind 在 'wiki' / 'vector' / 'graph' 中取一，UI 用它选
/// 图标 + 显示来源徽章。
class WikiSearchHit {
  final String kind; // wiki | vector | graph
  final String pageId;
  final String? blockId;
  final String projectId;
  final String title;
  final String snippet;
  final double score;
  final String? viaSeedPageId; // 仅 graph hit

  const WikiSearchHit({
    required this.kind,
    required this.pageId,
    required this.projectId,
    required this.title,
    required this.snippet,
    required this.score,
    this.blockId,
    this.viaSeedPageId,
  });

  factory WikiSearchHit.fromWikiJson(Map<String, dynamic> j) => WikiSearchHit(
        kind: 'wiki',
        pageId: j['page_id']?.toString() ?? '',
        projectId: j['project_id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        snippet: j['snippet']?.toString() ?? '',
        score: (j['score'] as num?)?.toDouble() ?? 0.0,
      );

  factory WikiSearchHit.fromVectorJson(Map<String, dynamic> j) => WikiSearchHit(
        kind: 'vector',
        pageId: j['page_id']?.toString() ?? '',
        blockId: (j['block_id'] as String?)?.isNotEmpty == true
            ? j['block_id'] as String
            : null,
        projectId: j['project_id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        snippet: j['snippet']?.toString() ?? '',
        score: (j['score'] as num?)?.toDouble() ?? 0.0,
      );

  factory WikiSearchHit.fromGraphJson(Map<String, dynamic> j) => WikiSearchHit(
        kind: 'graph',
        pageId: j['page_id']?.toString() ?? '',
        projectId: '',
        title: j['title']?.toString() ?? '',
        snippet: '',
        score: (j['score'] as num?)?.toDouble() ?? 0.0,
        viaSeedPageId: j['via_seed_page_id']?.toString(),
      );

  /// fused hit（RRF 融合 + rerank 后的单一条目）。字段在 meta 里；
  /// score 优先取 cross-encoder 的 reranked_score（无 rerank 时 fallback
  /// RRF fusion score）。source ∈ wiki/vector/graph 决定 kind。
  factory WikiSearchHit.fromFusedJson(Map<String, dynamic> j) {
    final meta = (j['meta'] as Map<String, dynamic>?) ?? const {};
    final reranked = (j['reranked_score'] as num?)?.toDouble();
    final blockId = meta['block_id'] as String?;
    final viaSeed = meta['via_seed_page'] as String?;
    return WikiSearchHit(
      kind: (meta['source'] as String?) ?? 'wiki',
      pageId: meta['page_id']?.toString() ?? '',
      projectId: meta['project_id']?.toString() ?? '',
      title: meta['title']?.toString() ?? '',
      snippet: meta['snippet']?.toString() ?? '',
      score: reranked ?? (j['score'] as num?)?.toDouble() ?? 0.0,
      blockId: (blockId != null && blockId.isNotEmpty) ? blockId : null,
      viaSeedPageId:
          (viaSeed != null && viaSeed.isNotEmpty) ? viaSeed : null,
    );
  }
}

/// 项目 wiki 关系图谱。Brain 端 Louvain 社区检测的产物。
/// 节点 = wiki page；边 = 直接 wikilink 引用 + Adamic-Adar 相似度。
class WikiGraphData {
  final List<WikiGraphNode> nodes;
  final List<WikiGraphEdge> edges;

  const WikiGraphData({required this.nodes, required this.edges});

  factory WikiGraphData.fromJson(Map<String, dynamic> j) {
    return WikiGraphData(
      nodes: (j['nodes'] as List? ?? const [])
          .whereType<Map>()
          .map((m) => WikiGraphNode.fromJson(m.cast()))
          .toList(),
      edges: (j['edges'] as List? ?? const [])
          .whereType<Map>()
          .map((m) => WikiGraphEdge.fromJson(m.cast()))
          .toList(),
    );
  }
}

class WikiGraphNode {
  final String id; // page_id
  final String title;
  final String? pageType; // overview / entity / concept / ...
  final int community; // Louvain community id；用于着色
  final double weight; // 节点重要性（degree / PageRank 派生）

  const WikiGraphNode({
    required this.id,
    required this.title,
    required this.community,
    required this.weight,
    this.pageType,
  });

  factory WikiGraphNode.fromJson(Map<String, dynamic> j) => WikiGraphNode(
        id: j['id']?.toString() ?? j['page_id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        pageType: j['page_type'] as String?,
        community: (j['community'] as num?)?.toInt() ?? 0,
        weight: (j['weight'] as num?)?.toDouble() ?? 1.0,
      );
}

class WikiGraphEdge {
  final String source; // page_id
  final String target; // page_id
  final double weight;

  const WikiGraphEdge({
    required this.source,
    required this.target,
    required this.weight,
  });

  factory WikiGraphEdge.fromJson(Map<String, dynamic> j) => WikiGraphEdge(
        source: j['source']?.toString() ?? j['from']?.toString() ?? '',
        target: j['target']?.toString() ?? j['to']?.toString() ?? '',
        weight: (j['weight'] as num?)?.toDouble() ?? 1.0,
      );
}

/// Graph insights — 结构启发式 surprising connections + knowledge gaps。
/// 后端 services/brain/internal/wiki/graph/insights.go（纯算法，零 LLM）。
class WikiInsights {
  final List<WikiSurprisingConnection> surprising;
  final List<WikiKnowledgeGap> gaps;
  final WikiInsightStats stats;

  const WikiInsights({
    required this.surprising,
    required this.gaps,
    required this.stats,
  });

  factory WikiInsights.fromJson(Map<String, dynamic> j) => WikiInsights(
        surprising: (j['surprising_connections'] as List? ?? const [])
            .whereType<Map>()
            .map((m) => WikiSurprisingConnection.fromJson(m.cast()))
            .toList(),
        gaps: (j['knowledge_gaps'] as List? ?? const [])
            .whereType<Map>()
            .map((m) => WikiKnowledgeGap.fromJson(m.cast()))
            .toList(),
        stats: WikiInsightStats.fromJson(
            (j['stats'] as Map?)?.cast<String, dynamic>() ?? const {}),
      );
}

class WikiInsightNodeBrief {
  final String id;
  final String title;
  final String type;

  const WikiInsightNodeBrief({
    required this.id,
    required this.title,
    required this.type,
  });

  factory WikiInsightNodeBrief.fromJson(Map<String, dynamic> j) =>
      WikiInsightNodeBrief(
        id: j['id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        type: j['type']?.toString() ?? '',
      );
}

class WikiSurprisingConnection {
  final WikiInsightNodeBrief source;
  final WikiInsightNodeBrief target;
  final int score;
  final List<String> reasons;
  final String key; // 稳定 dismiss key

  const WikiSurprisingConnection({
    required this.source,
    required this.target,
    required this.score,
    required this.reasons,
    required this.key,
  });

  factory WikiSurprisingConnection.fromJson(Map<String, dynamic> j) =>
      WikiSurprisingConnection(
        source: WikiInsightNodeBrief.fromJson(
            (j['source'] as Map?)?.cast<String, dynamic>() ?? const {}),
        target: WikiInsightNodeBrief.fromJson(
            (j['target'] as Map?)?.cast<String, dynamic>() ?? const {}),
        score: (j['score'] as num?)?.toInt() ?? 0,
        reasons: (j['reasons'] as List? ?? const [])
            .map((r) => r.toString())
            .toList(),
        key: j['key']?.toString() ?? '',
      );
}

class WikiKnowledgeGap {
  /// isolated-node | sparse-community | bridge-node
  final String type;
  final String title;
  final String description;
  final List<String> nodeIds;
  final String suggestion;

  const WikiKnowledgeGap({
    required this.type,
    required this.title,
    required this.description,
    required this.nodeIds,
    required this.suggestion,
  });

  factory WikiKnowledgeGap.fromJson(Map<String, dynamic> j) =>
      WikiKnowledgeGap(
        type: j['type']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        description: j['description']?.toString() ?? '',
        nodeIds: (j['node_ids'] as List? ?? const [])
            .map((n) => n.toString())
            .toList(),
        suggestion: j['suggestion']?.toString() ?? '',
      );
}

class WikiInsightStats {
  final int nodeCount;
  final int edgeCount;
  final int communityCount;

  const WikiInsightStats({
    required this.nodeCount,
    required this.edgeCount,
    required this.communityCount,
  });

  factory WikiInsightStats.fromJson(Map<String, dynamic> j) =>
      WikiInsightStats(
        nodeCount: (j['node_count'] as num?)?.toInt() ?? 0,
        edgeCount: (j['edge_count'] as num?)?.toInt() ?? 0,
        communityCount: (j['community_count'] as num?)?.toInt() ?? 0,
      );
}

/// 用户反馈 / 路线图条目。category ∈ feature / bug / idea / other。
class WikiSuggestion {
  final String id;
  final String title;
  final String body;
  final String category;
  final String status; // open | planned | shipped | rejected
  final int votes;
  final bool myVote;
  final String? authorEmail;
  final DateTime createdAt;
  final DateTime updatedAt;

  const WikiSuggestion({
    required this.id,
    required this.title,
    required this.body,
    required this.category,
    required this.status,
    required this.votes,
    required this.myVote,
    required this.createdAt,
    required this.updatedAt,
    this.authorEmail,
  });

  factory WikiSuggestion.fromJson(Map<String, dynamic> j) => WikiSuggestion(
        id: j['id']?.toString() ?? '',
        title: j['title']?.toString() ?? '',
        body: j['body']?.toString() ?? '',
        category: j['category']?.toString() ?? 'feature',
        status: j['status']?.toString() ?? 'open',
        votes: (j['votes'] as num?)?.toInt() ?? 0,
        myVote: j['my_vote'] == true,
        authorEmail: j['author_email'] as String?,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc() ??
                DateTime.now().toUtc(),
      );
}

/// 项目内对话会话头部（不含 messages）。
/// 项目内对话的独立通道（brain wiki/chat stub）已随 ProjectChatPage
/// 半桩一起退役 —— 会话能力统一走 Agent Plane V2（ThreadsShellPage）。

class WikiClient {
  WikiClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  Future<List<WikiProject>> listProjects() async {
    final raw = await _get('/v1/wiki/projects');
    final list = (raw['projects'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(WikiProject.fromJson).toList();
  }

  Future<WikiProject> createProject(String name, {String? templateId}) async {
    final body = <String, dynamic>{'name': name};
    if (templateId != null) body['template_id'] = templateId;
    final raw = await _post('/v1/wiki/projects', body);
    return WikiProject.fromJson(raw);
  }

  Future<List<WikiPage>> listPages(String projectId) async {
    final raw = await _get('/v1/wiki/projects/$projectId/pages');
    final list = (raw['pages'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(WikiPage.fromJson).toList();
  }

  /// Fetch one page (with frontmatter and version). Used by the
  /// frontmatter editor when it needs an authoritative server-side
  /// snapshot (the local Drift cache only stores title + version).
  Future<WikiPage> getPage(String projectId, String pageId) async {
    final raw =
        await _get('/v1/wiki/projects/$projectId/pages/$pageId');
    return WikiPage.fromJson(raw);
  }

  Future<WikiPage> createPage(
    String projectId, {
    required String title,
    String? parentId,
    Map<String, dynamic>? frontmatter,
  }) async {
    final body = <String, dynamic>{'title': title};
    if (parentId != null) body['parent_id'] = parentId;
    if (frontmatter != null) body['frontmatter'] = frontmatter;
    final raw = await _post('/v1/wiki/projects/$projectId/pages', body);
    return WikiPage.fromJson(raw);
  }

  /// Update a page's title / frontmatter. Optimistic concurrency:
  /// pass [ifMatchVersion] (the current page.version); on mismatch the
  /// server returns 409 with `{server_version, server_payload}` so the
  /// caller can prompt the user to merge.
  ///
  /// Either [title] or [frontmatter] (or both) may be omitted — the
  /// server PATCHes only the fields present.
  Future<WikiPage> updatePage(
    String projectId,
    String pageId, {
    required int ifMatchVersion,
    String? title,
    Map<String, dynamic>? frontmatter,
  }) async {
    final body = <String, dynamic>{};
    if (title != null) body['title'] = title;
    if (frontmatter != null) body['frontmatter'] = frontmatter;
    final raw = await _put(
      '/v1/wiki/projects/$projectId/pages/$pageId',
      body,
      headers: {'If-Match': ifMatchVersion.toString()},
    );
    return WikiPage.fromJson(raw);
  }

  /// §⑤ Path C body_md 权威写（Milkdown 整篇）。服务端事务内 mdparse 重算 blocks
  /// 投影（保 block_id）+ emit page.updated。If-Match OCC；409 返 server_payload。
  Future<WikiPage> updatePageBody(
    String projectId,
    String pageId, {
    required int ifMatchVersion,
    required String bodyMd,
  }) async {
    final raw = await _put(
      '/v1/wiki/projects/$projectId/pages/$pageId/body',
      {'body_md': bodyMd},
      headers: {'If-Match': ifMatchVersion.toString()},
    );
    return WikiPage.fromJson(raw);
  }

  Future<List<WikiBlock>> listBlocks(String projectId, String pageId) async {
    final raw = await _get('/v1/wiki/projects/$projectId/pages/$pageId/blocks');
    final list = (raw['blocks'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(WikiBlock.fromJson).toList();
  }

  /// Reverse wikilink lookup — every block on another page that
  /// references this page via `[[Title]]` / `[[Title|alias]]`.
  Future<List<WikiBacklink>> listBacklinks(
    String projectId,
    String pageId,
  ) async {
    final raw = await _get(
      '/v1/wiki/projects/$projectId/pages/$pageId/backlinks',
    );
    final list = (raw['backlinks'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return list.map(WikiBacklink.fromJson).toList();
  }

  /// Chronological event timeline for one page (page.created /
  /// block.created / block.updated / ...).
  Future<List<WikiPageEvent>> listChangelog(
    String projectId,
    String pageId,
  ) async {
    final raw = await _get(
      '/v1/wiki/projects/$projectId/pages/$pageId/changelog',
    );
    final list = (raw['events'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return list.map(WikiPageEvent.fromJson).toList();
  }

  // ─── Revisions (页版本历史，迁移 00065) ────────────────────

  /// 版本列表（不含 blocks_json），按时间倒序由服务端保证。
  Future<List<WikiPageRevision>> listPageRevisions(
    String projectId,
    String pageId, {
    int? limit,
    int? offset,
  }) async {
    final query = <String>[];
    if (limit != null) query.add('limit=$limit');
    if (offset != null) query.add('offset=$offset');
    final qs = query.isEmpty ? '' : '?${query.join('&')}';
    final raw = await _get(
      '/v1/wiki/projects/$projectId/pages/$pageId/revisions$qs',
    );
    final list =
        (raw['revisions'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(WikiPageRevision.fromJson).toList();
  }

  /// 单版本详情（含完整 blocks_json）。
  Future<WikiPageRevision> getPageRevision(
    String projectId,
    String pageId,
    String revisionId,
  ) async {
    final raw = await _get(
      '/v1/wiki/projects/$projectId/pages/$pageId/revisions/$revisionId',
    );
    return WikiPageRevision.fromJson(raw);
  }

  /// 覆盖式恢复到该版本（服务端恢复前自动备份当前态为恢复点），
  /// 返回恢复后的 page。block 对账 in-place 由服务端处理。
  Future<WikiPage> restorePageRevision(
    String projectId,
    String pageId,
    String revisionId,
  ) async {
    final raw = await _post(
      '/v1/wiki/projects/$projectId/pages/$pageId/revisions/$revisionId/restore',
      const {},
    );
    return WikiPage.fromJson(raw);
  }

  /// 以该版本新建页（同 project/parent，标题加「（历史副本）」，复制 blocks）。
  Future<WikiPage> savePageRevisionAsCopy(
    String projectId,
    String pageId,
    String revisionId,
  ) async {
    final raw = await _post(
      '/v1/wiki/projects/$projectId/pages/$pageId/revisions/$revisionId/save-as-copy',
      const {},
    );
    return WikiPage.fromJson(raw);
  }

  Future<WikiBlock> createBlock(
    String projectId,
    String pageId, {
    required String type,
    required double position,
    required Map<String, dynamic> content,
  }) async {
    final raw = await _post(
      '/v1/wiki/projects/$projectId/pages/$pageId/blocks',
      {'type': type, 'position': position, 'content': content},
    );
    return WikiBlock.fromJson(raw);
  }

  /// updateBlock uses If-Match for optimistic concurrency. The server returns
  /// 409 with `{server_version, error}` on mismatch.
  Future<WikiBlock> updateBlock(
    String projectId,
    String blockId, {
    required Map<String, dynamic> content,
    required int ifMatchVersion,
    double? position,
  }) async {
    final body = <String, dynamic>{'content': content};
    if (position != null) body['position'] = position;
    final raw = await _put(
      '/v1/wiki/projects/$projectId/blocks/$blockId',
      body,
      headers: {'If-Match': ifMatchVersion.toString()},
    );
    return WikiBlock.fromJson(raw);
  }

  Future<void> deleteBlock(String projectId, String blockId) async {
    await _delete('/v1/wiki/projects/$projectId/blocks/$blockId');
  }

  // ─── Sources (B2) ───────────────────────────────────────

  Future<List<WikiSource>> listSources(String projectId) async {
    final raw = await _get('/v1/wiki/projects/$projectId/sources');
    final list =
        (raw['sources'] as List? ?? const []).cast<Map<String, dynamic>>();
    return list.map(WikiSource.fromJson).toList();
  }

  /// Create / upsert a source. Pass [fileId] from a prior /v1/files/upload
  /// call; same (projectId, relPath) replaces the previous row in place.
  ///
  /// 本机解析路径（docproc-web，设计文档 §3.1）：不传 fileId，改传
  /// [rawText]（解析出的全文）+ [contentHash]（sha256 hex，幂等）+
  /// [parseMeta]（{parser, version, format, page_count}）。
  Future<WikiSource> createSource(
    String projectId, {
    required String relPath,
    String? fileId,
    String? filename,
    String? mime,
    int? byteSize,
    String? externalId,
    String? parseStatus,
    String? rawText,
    String? contentHash,
    Map<String, dynamic>? parseMeta,
  }) async {
    final body = <String, dynamic>{'rel_path': relPath};
    if (fileId != null) body['file_id'] = fileId;
    if (filename != null) body['filename'] = filename;
    if (mime != null) body['mime'] = mime;
    if (byteSize != null) body['byte_size'] = byteSize;
    if (externalId != null) body['external_id'] = externalId;
    if (parseStatus != null) body['parse_status'] = parseStatus;
    if (rawText != null) body['raw_text'] = rawText;
    if (contentHash != null) body['content_hash'] = contentHash;
    if (parseMeta != null) body['parse_meta'] = parseMeta;
    final raw = await _post('/v1/wiki/projects/$projectId/sources', body);
    return WikiSource.fromJson(raw);
  }

  Future<void> deleteSource(String projectId, String sourceId) async {
    await _delete('/v1/wiki/projects/$projectId/sources/$sourceId');
  }

  /// Pages that would lose their only-source link if [sourceId] is deleted.
  /// B2.x stub: brain 当前返回空数组 + note；UI 把空当成"无受影响页"处理。
  Future<List<String>> deletePreview(
      String projectId, String sourceId) async {
    final raw = await _get(
      '/v1/wiki/projects/$projectId/sources/$sourceId/delete-preview',
    );
    final list = (raw['affected_pages'] as List? ?? const []).cast<dynamic>();
    return list.map((e) => e is Map ? (e['id'] as String? ?? '') : '$e')
        .where((e) => e.isNotEmpty)
        .toList();
  }

  /// Enqueue an ingest task. brain returns the new task row; caller can
  /// open `/v1/wiki/projects/{pid}/ingest/tasks/{tid}/events` SSE for
  /// progress streaming.
  ///
  /// 传 sourceId 时 worker 反查 brain internal_api 取 extracted_text（Phase 1
  /// 合并后 ingest_tasks.source_id → wiki_sources）。rawText 仅 free-form
  /// 粘贴文本 ingest 用。`title` 走 source filename 即可。
  ///
  /// [processor] = 'client' 时创建**镜像任务**（本机解析可见性 + 云端接管，
  /// 设计文档 §3.5）：任务建行但不 publish 给 wiki-llm，生命周期由客户端
  /// 经 [patchIngestTask] 推进；默认 'server'（现有行为不变）。
  Future<({String taskId, String status, String processor})> createIngestTask(
    String projectId, {
    String rawText = '',
    String title = '',
    String? sourceId,
    String? processor,
  }) async {
    final body = <String, dynamic>{
      'raw_text': rawText,
      'title': title,
    };
    if (sourceId != null) body['source_id'] = sourceId;
    if (processor != null) body['processor'] = processor;
    final raw = await _post('/v1/wiki/projects/$projectId/ingest', body);
    return (
      taskId: raw['id']?.toString() ?? '',
      status: raw['status']?.toString() ?? 'pending',
      processor: raw['processor']?.toString() ?? 'server',
    );
  }

  /// 推进 processor=client 的镜像任务（设计文档 §3.5 / W2 契约）：
  /// [status] ∈ running/done/failed/cancelled；[progress] 是**整体替换**
  /// 的完整进度对象（如 {phase, percent}），不是合并；[error] 仅 failed
  /// 时有意义。status 与 progress 至少给一个。
  ///
  /// 仅 processor=client 的任务可 PATCH（否则 409 not_client_task）；
  /// 任务已终态则 409 already_terminal。返回更新后的 task。
  Future<({String taskId, String status, String processor})> patchIngestTask(
    String projectId,
    String taskId, {
    String? status,
    Map<String, dynamic>? progress,
    String? error,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (progress != null) body['progress'] = progress;
    if (error != null) body['error'] = error;
    final raw = await _patch(
      '/v1/wiki/projects/$projectId/ingest/tasks/$taskId',
      body,
    );
    return (
      taskId: raw['id']?.toString() ?? taskId,
      status: raw['status']?.toString() ?? status ?? '',
      processor: raw['processor']?.toString() ?? 'client',
    );
  }

  // ─── Graph (B3.x) ───────────────────────────────────────

  Future<WikiGraphData> getGraph(String projectId) async {
    final raw = await _get('/v1/wiki/projects/$projectId/graph');
    return WikiGraphData.fromJson(raw);
  }

  Future<WikiGraphData> recomputeGraph(String projectId) async {
    final raw = await _post(
      '/v1/wiki/projects/$projectId/graph/recompute',
      const {},
    );
    return WikiGraphData.fromJson(raw);
  }

  /// Graph insights — surprising connections + knowledge gaps（纯结构启发式）。
  Future<WikiInsights> getGraphInsights(String projectId) async {
    final raw = await _post(
      '/v1/wiki/projects/$projectId/graph/insights',
      const {},
    );
    return WikiInsights.fromJson(raw);
  }

  // ─── Suggestions (B6.3) ─────────────────────────────────

  Future<List<WikiSuggestion>> listSuggestions({
    String scope = 'public',
    String? category,
  }) async {
    final path = scope == 'mine'
        ? '/v1/wiki/suggestions/me'
        : '/v1/wiki/suggestions';
    final raw = await _get(path);
    final items = (raw['items'] as List? ?? const []);
    var list = items
        .whereType<Map>()
        .map((m) => WikiSuggestion.fromJson(m.cast()))
        .toList();
    if (category != null && category.isNotEmpty) {
      list = list.where((s) => s.category == category).toList();
    }
    return list;
  }

  Future<WikiSuggestion> getSuggestion(String id) async {
    final raw = await _get('/v1/wiki/suggestions/$id');
    return WikiSuggestion.fromJson(raw);
  }

  Future<WikiSuggestion> submitSuggestion({
    required String title,
    required String body,
    String category = 'feature',
  }) async {
    final raw = await _post('/v1/wiki/suggestions', {
      'title': title,
      'body': body,
      'category': category,
    });
    return WikiSuggestion.fromJson(raw);
  }

  Future<void> voteSuggestion(String id, {bool up = true}) async {
    if (up) {
      await _post('/v1/wiki/suggestions/$id/votes', const {});
    } else {
      await _delete('/v1/wiki/suggestions/$id/votes');
    }
  }

  /// 项目内搜索 —— 直接走 brain 顶层 `POST /v1/search`（已支持
  /// project_id + scope='wiki' 过滤），不调用 wiki 自己的 search stub。
  ///
  /// 返回结构：`fused`（RRF 融合 + rerank 后单一总序，UI 主消费）+
  /// `wiki`/`vector`/`graph`（per-path raw，debug）+ `images`。
  Future<WikiSearchResult> searchInProject(
    String projectId, {
    required String query,
    int limit = 30,
  }) async {
    final raw = await _post('/v1/search', {
      'query': query,
      'scope': 'wiki',
      'project_id': projectId,
      'limit': limit,
    });
    return WikiSearchResult.fromJson(raw);
  }

  /// Activity Feed cold-start：项目级所有 task 行（ingest / research /
  /// lint / dedup / sweep）的 REST 投影。后续 B2.9 sync ws 接入后改为
  /// "REST 引导 + WS 增量"模式。返回原始 row map，由调用方喂给
  /// `ActivityFeedReducer.applyBackfill`。
  Future<List<Map<String, dynamic>>> listActivity(String projectId) async {
    final raw = await _get('/v1/wiki/projects/$projectId/activity');
    return (raw['items'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .toList();
  }

  /// All external_id values currently recorded for a project. Connector
  /// import dialogs use this to skip already-imported items.
  Future<List<String>> externalIdsInProject(String projectId) async {
    final raw =
        await _get('/v1/wiki/projects/$projectId/sources/external-ids');
    return (raw['external_ids'] as List? ?? const [])
        .map((e) => e.toString())
        .toList();
  }

  // ─── HTTP plumbing ──────────────────────────────────────

  Future<Map<String, dynamic>> _get(String path) async {
    return _request('GET', path, body: null);
  }

  Future<Map<String, dynamic>> _post(String path, Map<String, dynamic> body) async {
    return _request('POST', path, body: body);
  }

  Future<Map<String, dynamic>> _put(
    String path,
    Map<String, dynamic> body, {
    Map<String, String>? headers,
  }) async {
    return _request('PUT', path, body: body, extraHeaders: headers);
  }

  Future<Map<String, dynamic>> _patch(
    String path,
    Map<String, dynamic> body,
  ) async {
    return _request('PATCH', path, body: body);
  }

  Future<void> _delete(String path) async {
    await _request('DELETE', path, body: null);
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, dynamic>? body,
    Map<String, String>? extraHeaders,
  }) async {
    try {
      return await apiRequest(
        method: method,
        url: baseUrl.replace(path: path),
        bearerToken: bearerToken,
        extraHeaders: extraHeaders,
        body: body,
      );
    } on ApiError catch (e) {
      throw WikiApiError(
          method: method, path: path, status: e.status, body: e.body);
    }
  }
}

class WikiApiError implements Exception {
  final String method;
  final String path;
  final int status;
  final String body;
  const WikiApiError({
    required this.method,
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isVersionConflict => status == 409;
  bool get isNotFound => status == 404;

  @override
  String toString() => 'WikiApiError $status $method $path: $body';
}
