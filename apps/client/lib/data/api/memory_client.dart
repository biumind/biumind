// MemoryClient — thin Dart client for Brain.Memory.
//
// Mirrors services/brain/internal/memory/api/api.go contract:
//
//   POST   /v1/memory                 store
//   GET    /v1/memory?project_id=&kind=&limit=
//   GET    /v1/memory/recall?project_id=&q=&kind=&limit=
//   DELETE /v1/memory/{id}
//
// All endpoints require a Bearer JWT and scope memories to the caller.
// Recall responses include a `mode: hybrid|lexical` discriminator so
// the UI can show whether semantic ranking was used.

import '_http_helpers.dart';

/// Canonical memory kinds accepted by both client and server.
const Set<String> kMemoryKinds = {'recall', 'preference', 'habit'};

/// Deprecated alias retained for input back-compat until 2026-08-25.
/// The server silently rewrites 'skill' → 'habit'; clients should
/// migrate. See docs/BiuMind-Skills-Design.md §11.
const String kDeprecatedKindSkill = 'skill';

/// True when [kind] is acceptable as input. Deprecated aliases pass.
bool isAcceptedMemoryKind(String kind) =>
    kMemoryKinds.contains(kind) || kind == kDeprecatedKindSkill;

/// Returns the canonical kind that will actually persist server-side.
/// 'skill' is rewritten to 'habit'; everything else is unchanged.
String normalizeMemoryKind(String kind) =>
    kind == kDeprecatedKindSkill ? 'habit' : kind;

class Memory {
  final String id;
  final String projectId;
  final String kind;
  final String content;
  final double salience;
  final DateTime lastAccessedAt;
  final DateTime createdAt;
  /// score is populated by recall responses only.
  final double? score;

  const Memory({
    required this.id,
    required this.projectId,
    required this.kind,
    required this.content,
    required this.salience,
    required this.lastAccessedAt,
    required this.createdAt,
    this.score,
  });

  factory Memory.fromJson(Map<String, dynamic> j) => Memory(
        id: j['id'] as String,
        projectId: j['project_id'] as String,
        kind: j['kind'] as String? ?? 'recall',
        content: j['content'] as String? ?? '',
        salience: (j['salience'] as num? ?? 0.5).toDouble(),
        lastAccessedAt:
            DateTime.tryParse(j['last_accessed_at'] as String? ?? '') ??
                DateTime.fromMillisecondsSinceEpoch(0),
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
        score: (j['score'] as num?)?.toDouble(),
      );
}

/// Discriminator for recall responses.
enum RecallMode { lexical, hybrid, unknown }

RecallMode _parseMode(String? raw) {
  switch (raw) {
    case 'lexical':
      return RecallMode.lexical;
    case 'hybrid':
      return RecallMode.hybrid;
    default:
      return RecallMode.unknown;
  }
}

class RecallResult {
  final List<Memory> memories;
  final RecallMode mode;
  final String query;
  const RecallResult({
    required this.memories,
    required this.mode,
    required this.query,
  });

  factory RecallResult.fromJson(Map<String, dynamic> j) => RecallResult(
        memories: (j['memories'] as List? ?? const [])
            .cast<Map<String, dynamic>>()
            .map(Memory.fromJson)
            .toList(),
        mode: _parseMode(j['mode'] as String?),
        query: j['query'] as String? ?? '',
      );
}

class MemoryClient {
  MemoryClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  Future<Memory> store({
    required String projectId,
    required String content,
    String kind = 'recall',
    double? salience,
  }) async {
    if (!isAcceptedMemoryKind(kind)) {
      throw ArgumentError('invalid kind "$kind"');
    }
    final body = <String, dynamic>{
      'project_id': projectId,
      'kind': normalizeMemoryKind(kind),
      'content': content,
    };
    if (salience != null) body['salience'] = salience;
    final raw = await _post('/v1/memory', body);
    return Memory.fromJson(raw);
  }

  Future<List<Memory>> list({
    required String projectId,
    String? kind,
    int limit = 100,
  }) async {
    if (kind != null && !isAcceptedMemoryKind(kind)) {
      throw ArgumentError('invalid kind "$kind"');
    }
    final qp = <String, String>{
      'project_id': projectId,
      'limit': '$limit',
    };
    if (kind != null) qp['kind'] = normalizeMemoryKind(kind);
    final raw = await _get('/v1/memory', queryParams: qp);
    return (raw['memories'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(Memory.fromJson)
        .toList();
  }

  Future<RecallResult> recall({
    required String projectId,
    required String query,
    String? kind,
    int limit = 10,
  }) async {
    if (query.trim().isEmpty) {
      throw ArgumentError('query required');
    }
    if (kind != null && !isAcceptedMemoryKind(kind)) {
      throw ArgumentError('invalid kind "$kind"');
    }
    final qp = <String, String>{
      'project_id': projectId,
      'q': query,
      'limit': '$limit',
    };
    if (kind != null) qp['kind'] = normalizeMemoryKind(kind);
    final raw = await _get('/v1/memory/recall', queryParams: qp);
    return RecallResult.fromJson(raw);
  }

  Future<void> delete(String id) async {
    await _delete('/v1/memory/$id');
  }

  // ─── HTTP plumbing ──────────────────────────────────────

  Future<Map<String, dynamic>> _get(
    String path, {
    Map<String, String>? queryParams,
  }) =>
      _request('GET', path, queryParams: queryParams);

  Future<Map<String, dynamic>> _post(
    String path,
    Map<String, dynamic> body,
  ) =>
      _request('POST', path, body: body);

  Future<void> _delete(String path) async {
    await _request('DELETE', path);
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Map<String, String>? queryParams,
    Map<String, dynamic>? body,
  }) async {
    try {
      return await apiRequest(
        method: method,
        url: baseUrl.replace(path: path, queryParameters: queryParams),
        bearerToken: bearerToken,
        body: body,
      );
    } on ApiError catch (e) {
      throw MemoryApiError(
          method: method, path: path, status: e.status, body: e.body);
    }
  }
}

class MemoryApiError implements Exception {
  final String method;
  final String path;
  final int status;
  final String body;
  const MemoryApiError({
    required this.method,
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isNotFound => status == 404;
  bool get isForbidden => status == 403;
  bool get isUnauthorized => status == 401;

  @override
  String toString() => 'MemoryApiError $status $method $path: $body';
}
