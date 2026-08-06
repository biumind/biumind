// SearchClient — biumind brain unified search.
//
// Mirrors services/brain/internal/search/api/api.go. The search API
// runs four retrieval paths (BM25 / vector / graph / web) plus a
// fifth multimodal augmentation (image refs mined from matched
// pages) and returns each path's hits separately PLUS a fused list
// when scope=all.
//
//   POST /v1/search
//   { query, scope: "wiki" | "web" | "all", project_id?, limit }

import '_http_helpers.dart';

class SearchHit {
  final String id;          // RRF stable id, e.g. "wiki:page:<uuid>"
  final double score;
  final Map<String, dynamic> meta; // includes source / title / page_id / via_seed_page

  const SearchHit({required this.id, required this.score, required this.meta});

  factory SearchHit.fromJson(Map<String, dynamic> j) => SearchHit(
        id: j['id'] as String? ?? '',
        score: (j['score'] as num?)?.toDouble() ?? 0.0,
        meta: (j['meta'] as Map<String, dynamic>? ?? const {}),
      );

  /// 'wiki' / 'vector' / 'graph' / 'web' — which retrieval path
  /// originally surfaced this hit (RRF preserves the first-seen
  /// source on collision; UI renders a small badge).
  String get source => (meta['source'] as String?) ?? '';

  /// Page id when the hit is wiki-side; empty for web hits.
  String get pageId => (meta['page_id'] as String?) ?? '';

  /// Display title (page title / web result title).
  String get title => (meta['title'] as String?) ?? '';

  /// Snippet for blocks / web; empty for page-only hits.
  String get snippet => (meta['snippet'] as String?) ?? '';

  /// External URL (web only).
  String get url => (meta['url'] as String?) ?? '';

  /// Graph-only: which BM25 seed surfaced this related page. Lets
  /// the UI render "via [[X]]" provenance.
  String get viaSeedPage => (meta['via_seed_page'] as String?) ?? '';

  /// 融合条目的来源种类。include_notes=true 时 fused 里会出现
  /// kind='note' 的条目（对应 notes 分组里的笔记）；wiki/page 命中为空串。
  String get kind => (meta['kind'] as String?) ?? '';
}

/// POST /v1/search（include_notes=true）响应里 notes 分组的单条笔记命中。
class SearchNoteHit {
  final String id;
  final String title;
  final String snippet;
  final double score;
  final DateTime? updatedAt;

  const SearchNoteHit({
    required this.id,
    required this.title,
    required this.snippet,
    required this.score,
    this.updatedAt,
  });

  factory SearchNoteHit.fromJson(Map<String, dynamic> j) => SearchNoteHit(
        id: j['id'] as String? ?? '',
        title: j['title'] as String? ?? '',
        snippet: j['snippet'] as String? ?? '',
        score: (j['score'] as num?)?.toDouble() ?? 0.0,
        updatedAt:
            DateTime.tryParse(j['updated_at'] as String? ?? '')?.toUtc(),
      );
}

class SearchImageHit {
  final String url;
  final String alt;
  final String pageId;
  final String blockId;
  final String pageTitle;
  final bool altMatchesQuery;

  const SearchImageHit({
    required this.url,
    required this.alt,
    required this.pageId,
    required this.blockId,
    required this.pageTitle,
    required this.altMatchesQuery,
  });

  factory SearchImageHit.fromJson(Map<String, dynamic> j) => SearchImageHit(
        url: j['url'] as String? ?? '',
        alt: j['alt'] as String? ?? '',
        pageId: j['page_id'] as String? ?? '',
        blockId: j['block_id'] as String? ?? '',
        pageTitle: j['page_title'] as String? ?? '',
        altMatchesQuery: j['alt_matches_query'] as bool? ?? false,
      );
}

class SearchResponse {
  final String query;
  final String scope;
  final List<SearchHit> fused;
  final List<SearchImageHit> images;
  // Per-path raw lists kept so the UI can render "where did this
  // hit come from" panels for power users / debugging. The default
  // UX renders fused + images only.
  final List<Map<String, dynamic>> wiki;
  final List<Map<String, dynamic>> web;
  final List<Map<String, dynamic>> vector;
  final List<Map<String, dynamic>> graph;

  /// 笔记命中分组 —— 仅当请求带 include_notes=true 时服务端才返回
  /// （否则为空列表）。UI 单独渲染「笔记」分组。
  final List<SearchNoteHit> notes;

  const SearchResponse({
    required this.query,
    required this.scope,
    required this.fused,
    required this.images,
    required this.wiki,
    required this.web,
    required this.vector,
    required this.graph,
    this.notes = const [],
  });

  factory SearchResponse.fromJson(Map<String, dynamic> j) {
    List<Map<String, dynamic>> listOf(String key) {
      final raw = j[key] as List<dynamic>? ?? const [];
      return raw.whereType<Map<String, dynamic>>().toList(growable: false);
    }
    final fusedRaw = j['fused'] as List<dynamic>? ?? const [];
    final imgsRaw = j['images'] as List<dynamic>? ?? const [];
    final notesRaw = j['notes'] as List<dynamic>? ?? const [];
    return SearchResponse(
      query: j['query'] as String? ?? '',
      scope: j['scope'] as String? ?? 'all',
      fused: fusedRaw
          .whereType<Map<String, dynamic>>()
          .map(SearchHit.fromJson)
          .toList(growable: false),
      images: imgsRaw
          .whereType<Map<String, dynamic>>()
          .map(SearchImageHit.fromJson)
          .toList(growable: false),
      notes: notesRaw
          .whereType<Map<String, dynamic>>()
          .map(SearchNoteHit.fromJson)
          .toList(growable: false),
      wiki: listOf('wiki'),
      web: listOf('web'),
      vector: listOf('vector'),
      graph: listOf('graph'),
    );
  }
}

class SearchClient {
  final Uri baseUrl;
  final String? bearerToken;
  const SearchClient(this.baseUrl, this.bearerToken);

  /// Run a unified search. `scope=all` is the canonical choice for
  /// this UI — it triggers RRF fusion + the image augmentation.
  /// `wiki` / `web` skip RRF (single source).
  ///
  /// [includeNotes] = true 时请求带 `include_notes: true`，响应附带
  /// notes 分组（且 fused 里出现 kind='note' 的条目）。默认 false。
  Future<SearchResponse> search({
    required String query,
    String scope = 'all',
    String? projectId,
    int limit = 20,
    bool includeNotes = false,
  }) async {
    final body = <String, dynamic>{
      'query': query,
      'scope': scope,
      'limit': limit,
    };
    if (projectId != null && projectId.isNotEmpty) {
      body['project_id'] = projectId;
    }
    if (includeNotes) {
      body['include_notes'] = true;
    }
    try {
      final m = await apiRequest(
        method: 'POST',
        url: baseUrl.replace(path: '/v1/search'),
        bearerToken: bearerToken,
        body: body,
      );
      return SearchResponse.fromJson(m);
    } on ApiError catch (e) {
      throw SearchApiError(status: e.status, body: e.body);
    }
  }
}

extension SearchClientFeedback on SearchClient {
  /// Fetch the calling user's existing verdicts for one query. Returns
  /// `{ pageId: "up" | "down" }`. The search UI calls this once per
  /// completed search so thumb buttons render with the right state on
  /// first paint (rather than always neutral until clicked).
  Future<Map<String, String>> listFeedback({required String query}) async {
    final url = baseUrl.replace(
      path: '/v1/search/feedback',
      queryParameters: {'query': query},
    );
    try {
      final m = await apiRequest(
        method: 'GET', url: url, bearerToken: bearerToken,
      );
      final raw = m['verdicts'] as List<dynamic>? ?? const [];
      final out = <String, String>{};
      for (final e in raw) {
        if (e is! Map<String, dynamic>) continue;
        final pid = e['page_id'] as String?;
        final sig = e['signal'] as String?;
        if (pid != null && sig != null && pid.isNotEmpty) {
          out[pid] = sig;
        }
      }
      return out;
    } on ApiError catch (e) {
      throw SearchApiError(status: e.status, body: e.body);
    }
  }

  /// Submit a thumbs up/down on one search result.
  ///
  /// Backend dedupes by `(user, lower(query), page_id)` so re-submitting
  /// the same thumb is a no-op; flipping the thumb updates in place.
  /// `rank` is the result's position in the fused list at click time —
  /// captured for offline RRF tuning.
  Future<void> submitFeedback({
    required String query,
    required String pageId,
    required String signal, // "up" | "down"
    int rank = 0,
    String? source,
  }) async {
    final body = <String, dynamic>{
      'query': query,
      'page_id': pageId,
      'signal': signal,
      'rank': rank,
    };
    if (source != null && source.isNotEmpty) {
      body['meta'] = <String, dynamic>{'source': source};
    }
    try {
      await apiRequest(
        method: 'POST',
        url: baseUrl.replace(path: '/v1/search/feedback'),
        bearerToken: bearerToken,
        body: body,
      );
    } on ApiError catch (e) {
      throw SearchApiError(status: e.status, body: e.body);
    }
  }

  /// Clear the user's verdict for one (query, page) pair. Called when
  /// the UI's thumb toggles off (user clicks an already-active thumb).
  Future<void> clearFeedback({
    required String query,
    required String pageId,
  }) async {
    try {
      await apiRequest(
        method: 'DELETE',
        url: baseUrl.replace(path: '/v1/search/feedback'),
        bearerToken: bearerToken,
        body: <String, dynamic>{'query': query, 'page_id': pageId},
      );
    } on ApiError catch (e) {
      throw SearchApiError(status: e.status, body: e.body);
    }
  }
}

class SearchApiError implements Exception {
  final int status;
  final String body;
  const SearchApiError({required this.status, required this.body});

  @override
  String toString() => 'SearchApiError $status: $body';
}
