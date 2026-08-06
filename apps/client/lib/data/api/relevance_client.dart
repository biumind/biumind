// RelevanceClient — page-to-page relatedness.
//
// Mirrors services/brain/internal/wiki/relevance/api.go:
//
//   GET /v1/wiki/pages/{id}/related?limit=N
//
// Used by the wiki page editor to show a "see also" rail. The MCP
// tool wiki.related_pages is the agent-side equivalent and consumes
// the same backend table (brain.page_relevance).

import '_http_helpers.dart';

class RelatedPage {
  final String pageId;
  final String title;
  final double score;
  final Map<String, double> signals;

  const RelatedPage({
    required this.pageId,
    required this.title,
    required this.score,
    required this.signals,
  });

  factory RelatedPage.fromJson(Map<String, dynamic> j) {
    final raw = j['signals'] as Map<String, dynamic>? ?? const {};
    final signals = <String, double>{};
    for (final e in raw.entries) {
      final v = e.value;
      if (v is num) signals[e.key] = v.toDouble();
    }
    return RelatedPage(
      pageId: j['page_id'] as String,
      title: (j['title'] as String?) ?? '',
      score: (j['score'] as num?)?.toDouble() ?? 0.0,
      signals: signals,
    );
  }
}

class RelevanceClient {
  final Uri baseUrl;
  final String? bearerToken;
  const RelevanceClient(this.baseUrl, this.bearerToken);

  /// List the top-K related pages for `pageId`, ranked by graph
  /// relevance score. Returns empty list when the relevance worker
  /// hasn't populated rows for this project yet (404 → empty).
  Future<List<RelatedPage>> listRelated(String pageId, {int limit = 10}) async {
    final url = baseUrl.replace(
      path: '/v1/wiki/pages/$pageId/related',
      queryParameters: {'limit': '$limit'},
    );
    try {
      final m = await apiRequest(
        method: 'GET', url: url, bearerToken: bearerToken,
      );
      final raw = m['related'] as List<dynamic>? ?? const [];
      return raw
          .whereType<Map<String, dynamic>>()
          .map(RelatedPage.fromJson)
          .toList(growable: false);
    } on ApiError catch (e) {
      if (e.status == 404) return const [];
      throw RelevanceApiError(
          path: 'GET ${url.path}', status: e.status, body: e.body);
    }
  }
}

class RelevanceApiError implements Exception {
  final String path;
  final int status;
  final String body;
  const RelevanceApiError({
    required this.path,
    required this.status,
    required this.body,
  });

  @override
  String toString() => 'RelevanceApiError $status $path: $body';
}
