// ReviewsClient — wiki audit queue (P2-D dedup / lint / sweep / merge).
//
// Mirrors services/brain/internal/wiki/reviews/api.go:
//
//   GET    /v1/wiki/projects/{pid}/reviews?kind=&status=
//   GET    /v1/wiki/projects/{pid}/reviews/summary
//   POST   /v1/wiki/projects/{pid}/reviews/scan {family}
//   POST   /v1/wiki/reviews/{id}/resolve
//   POST   /v1/wiki/reviews/{id}/dismiss
//   POST   /v1/wiki/pages/{id}/merge { from_id }
//
// All routes are JWT-gated and project-scoped — same auth as
// WikiClient. We share the apiRequest helper so 401-refresh-retry
// works identically to the rest of the data stack.

import 'dart:async';

import '_http_helpers.dart';

/// One row from brain.review_items.
class WikiReview {
  final String id;
  final String projectId;
  final String kind; // dedup | lint | sweep | merge | suggestion
  final String status; // open | resolved | dismissed
  final String title;
  final String description;
  final List<String> pageIds;
  final Map<String, dynamic> payload;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime? resolvedAt;

  const WikiReview({
    required this.id,
    required this.projectId,
    required this.kind,
    required this.status,
    required this.title,
    required this.description,
    required this.pageIds,
    required this.payload,
    required this.createdAt,
    required this.updatedAt,
    this.resolvedAt,
  });

  factory WikiReview.fromJson(Map<String, dynamic> j) => WikiReview(
        id: j['id'] as String,
        projectId: j['project_id'] as String,
        kind: j['kind'] as String,
        status: j['status'] as String,
        title: (j['title'] as String?) ?? '',
        description: (j['description'] as String?) ?? '',
        pageIds: (j['page_ids'] as List<dynamic>? ?? const [])
            .whereType<String>()
            .toList(growable: false),
        payload: (j['payload'] as Map<String, dynamic>? ?? const {}),
        createdAt: DateTime.parse(j['created_at'] as String).toLocal(),
        updatedAt: DateTime.parse(j['updated_at'] as String).toLocal(),
        resolvedAt: j['resolved_at'] != null
            ? DateTime.parse(j['resolved_at'] as String).toLocal()
            : null,
      );

  /// Convenience: dedup + merge findings reference exactly two pages
  /// (canonical, duplicate). The first id is treated as canonical for
  /// merge actions — caller can flip via the UI.
  bool get isPair => pageIds.length == 2;
}

/// One row of the cleanup summary — open findings grouped by
/// (kind, rule_id) for a project.
class RuleCount {
  final String kind;
  final String ruleId;
  final int count;
  const RuleCount({
    required this.kind,
    required this.ruleId,
    required this.count,
  });
  factory RuleCount.fromJson(Map<String, dynamic> j) => RuleCount(
        kind: (j['kind'] as String?) ?? '',
        ruleId: (j['rule_id'] as String?) ?? '',
        count: (j['count'] as num?)?.toInt() ?? 0,
      );
}

/// Result of POST /reviews/scan. structural runs synchronously and
/// reports how many new findings were created; semantic queues a
/// background LLM pass (queued=true, findingsAdded=0) whose results
/// land in the reviews list asynchronously.
class LintScanResult {
  final String kind; // structural | semantic
  final int findingsAdded;
  final bool queued;
  const LintScanResult({
    required this.kind,
    required this.findingsAdded,
    required this.queued,
  });
}

class ReviewsClient {
  final Uri baseUrl;
  final String? bearerToken;
  const ReviewsClient(this.baseUrl, this.bearerToken);

  /// List reviews for one project. [kind] / [status] are optional
  /// filters; empty maps to "all". Default [status] on the server side
  /// is "open" — pass an explicit value (or "all") to inspect history.
  Future<List<WikiReview>> list({
    required String projectId,
    String? kind,
    String? status,
    int limit = 100,
  }) async {
    final qp = <String, String>{'limit': limit.toString()};
    if (kind != null && kind.isNotEmpty) qp['kind'] = kind;
    if (status != null && status.isNotEmpty) qp['status'] = status;
    final m = await _request(
      'GET',
      '/v1/wiki/projects/$projectId/reviews',
      query: qp,
    );
    final raw = m['reviews'] as List<dynamic>? ?? const [];
    return raw
        .whereType<Map<String, dynamic>>()
        .map(WikiReview.fromJson)
        .toList(growable: false);
  }

  /// Mark a review as resolved (the suggestion was acted on).
  Future<void> resolve(String reviewId) async {
    await _request('POST', '/v1/wiki/reviews/$reviewId/resolve');
  }

  /// Mark a review as dismissed (the suggestion was wrong; don't
  /// re-flag on subsequent scans).
  Future<void> dismiss(String reviewId) async {
    await _request('POST', '/v1/wiki/reviews/$reviewId/dismiss');
  }

  /// Fold [duplicateId] into [canonicalId]. Returns true on success;
  /// any open dedup review for the pair is auto-resolved on the server.
  Future<void> mergePages({
    required String canonicalId,
    required String duplicateId,
  }) async {
    await _request(
      'POST',
      '/v1/wiki/pages/$canonicalId/merge',
      body: <String, dynamic>{'from_id': duplicateId},
    );
  }

  /// Per-rule open-review counts for the cleanup dashboard.
  Future<List<RuleCount>> summary(String projectId) async {
    final m = await _request(
      'GET',
      '/v1/wiki/projects/$projectId/reviews/summary',
    );
    final raw = m['counts'] as List<dynamic>? ?? const [];
    return raw
        .whereType<Map<String, dynamic>>()
        .map(RuleCount.fromJson)
        .toList(growable: false);
  }

  /// Soft-delete the page tied to a review and resolve the review in
  /// one shot. Used by the cleanup dashboard for the rules where "fix"
  /// means "delete" (orphan / empty / stub / untitled).
  Future<void> deletePageForReview(String reviewId) async {
    await _request('POST', '/v1/wiki/reviews/$reviewId/delete-page');
  }

  /// Trigger an on-demand lint scan. [family] "structural" runs
  /// synchronously (pure Go rules) and returns the count of newly-
  /// created findings; "semantic" fires a background LLM pass and
  /// returns immediately — findings land in the reviews queue async.
  /// Replaces the deleted /lint/run + /lint/semantic pair (B-10).
  Future<LintScanResult> triggerScan({
    required String projectId,
    String family = 'structural',
  }) async {
    final m = await _request(
      'POST',
      '/v1/wiki/projects/$projectId/reviews/scan',
      body: <String, dynamic>{'family': family},
    );
    return LintScanResult(
      kind: (m['kind'] as String?) ?? family,
      findingsAdded: (m['findings_added'] as num?)?.toInt() ?? 0,
      queued: (m['queued'] as bool?) ?? false,
    );
  }

  // ─── HTTP plumbing ──────────────────────────────────────

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? query,
    Map<String, dynamic>? body,
  }) async {
    var url = baseUrl.replace(path: path);
    if (query != null && query.isNotEmpty) {
      url = url.replace(queryParameters: <String, String>{
        ...url.queryParameters,
        ...query,
      });
    }
    try {
      return await apiRequest(
        method: method,
        url: url,
        bearerToken: bearerToken,
        body: body,
      );
    } on ApiError catch (e) {
      throw ReviewsApiError(
        method: method,
        path: path,
        status: e.status,
        body: e.body,
      );
    }
  }
}

class ReviewsApiError implements Exception {
  final String method;
  final String path;
  final int status;
  final String body;
  const ReviewsApiError({
    required this.method,
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isNotFound => status == 404;
  bool get isConflict => status == 409;

  @override
  String toString() => 'ReviewsApiError $status $method $path: $body';
}
