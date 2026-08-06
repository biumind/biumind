// SkillClient — thin Dart client for the runtime Skills surface.
//
// Mirrors services/runtime/internal/api/skills_handlers.go contract:
//
//   GET    /v1/skills?source=&status=&owner_id=
//   GET    /v1/skills/{id}
//   POST   /v1/skills            install (URL / Zip-base64 / inline)
//   PATCH  /v1/skills/{id}       sparse update
//   DELETE /v1/skills/{id}
//   POST   /v1/skills/{id}/toggle  per-agent enable + pin
//
// Bearer JWT carries the org claim; the client just passes the
// token through. JSON keys must match the Go server 1:1 — that's
// pinned by skill_client_test.dart.

import '_http_helpers.dart';

class SkillManifest {
  final String version;
  final String license;
  final String repository;
  final String sourceUrl;
  final String authorName;
  final String authorUrl;
  /// Visual hint shown on list cards. Emoji ("🛠") or HTTPS image URL.
  /// Empty string falls back to first-letter avatar.
  final String icon;

  const SkillManifest({
    this.version = '',
    this.license = '',
    this.repository = '',
    this.sourceUrl = '',
    this.authorName = '',
    this.authorUrl = '',
    this.icon = '',
  });

  factory SkillManifest.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const SkillManifest();
    final author = j['author'] as Map<String, dynamic>?;
    return SkillManifest(
      version: j['version'] as String? ?? '',
      license: j['license'] as String? ?? '',
      repository: j['repository'] as String? ?? '',
      sourceUrl: j['source_url'] as String? ?? '',
      authorName: author?['name'] as String? ?? '',
      authorUrl: author?['url'] as String? ?? '',
      icon: j['icon'] as String? ?? '',
    );
  }
}

/// One bundled resource entry — mirrors server-side
/// services/runtime/internal/skills.ResourceMeta. Two shapes:
///   - inline: `inline` non-empty (≤64 KB UTF-8 text); the entire body
///     is embedded in the API response.
///   - CAS:    `sha256` + `sizeBytes` point at the Files CAS; client
///     would have to fetch the body separately (not yet wired).
/// 8 bundled SKILL.md packs ship without resources, so this surface
/// stays empty for them — meaningful when users install .biuskill
/// archives with scripts/ / references/ / assets/ subdirs.
class SkillResource {
  final String sha256;
  final int sizeBytes;
  final String mimeType;
  final String inline;

  const SkillResource({
    this.sha256 = '',
    this.sizeBytes = 0,
    this.mimeType = '',
    this.inline = '',
  });

  factory SkillResource.fromJson(Map<String, dynamic> j) => SkillResource(
        sha256: j['sha256'] as String? ?? '',
        sizeBytes: (j['size_bytes'] as num?)?.toInt() ?? 0,
        mimeType: j['mime_type'] as String? ?? '',
        inline: j['inline'] as String? ?? '',
      );

  /// True when the resource body is fully present in this object.
  bool get isInline => inline.isNotEmpty;

  /// True when only the CAS pointer is set — body must be fetched
  /// separately. Currently surfaces to UI as a placeholder; the Files
  /// fetch path lands when sandbox bundle mount finalises.
  bool get isCAS => !isInline && sha256.isNotEmpty;
}

class Skill {
  final String id;
  final String orgId;
  final String? ownerId;
  final String identifier;
  final String name;
  final String description;
  /// One of bundled / org / user / marketplace / imported.
  final String source;
  /// One of active / disabled / staged / staged_org / suspended.
  final String status;
  final SkillManifest manifest;
  final String content;
  final String contentHash;
  /// `Map<vpath, ResourceMeta>` — vpath shapes:
  ///   `references/<file>`  / `scripts/<file>`  / `assets/<file>`
  /// Empty for bundled / inline-only skills.
  final Map<String, SkillResource> resources;
  final List<String> paths;
  final List<String> permissions;
  final String zipFileSha256;
  /// Set on staged skills proposing to replace an active row — the
  /// predecessor's id. Empty otherwise. Powers the diff hint in the
  /// detail drawer ("基于 `v<hash>`") so reviewers know what changed.
  final String updateOfId;
  final DateTime createdAt;
  final DateTime updatedAt;

  const Skill({
    required this.id,
    required this.orgId,
    this.ownerId,
    required this.identifier,
    required this.name,
    required this.description,
    required this.source,
    required this.status,
    required this.manifest,
    required this.content,
    required this.contentHash,
    this.resources = const {},
    required this.paths,
    required this.permissions,
    required this.zipFileSha256,
    this.updateOfId = '',
    required this.createdAt,
    required this.updatedAt,
  });

  factory Skill.fromJson(Map<String, dynamic> j) => Skill(
        id: j['id'] as String,
        orgId: j['org_id'] as String? ?? '',
        ownerId: j['owner_id'] as String?,
        identifier: j['identifier'] as String? ?? '',
        name: j['name'] as String? ?? '',
        description: j['description'] as String? ?? '',
        source: j['source'] as String? ?? 'user',
        status: j['status'] as String? ?? 'active',
        manifest: SkillManifest.fromJson(j['manifest'] as Map<String, dynamic>?),
        content: j['content'] as String? ?? '',
        contentHash: j['content_hash'] as String? ?? '',
        resources: _resourcesMap(j['resources']),
        paths: _stringList(j['paths']),
        permissions: _stringList(j['permissions']),
        zipFileSha256: j['zip_file_sha256'] as String? ?? '',
        updateOfId: j['update_of_id'] as String? ?? '',
        createdAt: DateTime.tryParse(j['created_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
        updatedAt: DateTime.tryParse(j['updated_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
      );
}

/// Decode the resources JSON object into a typed Dart map, gracefully
/// dropping entries whose value isn't a JSON object (server should
/// never emit those, but tolerate forward-compat shape changes
/// rather than crash the page render).
Map<String, SkillResource> _resourcesMap(Object? raw) {
  if (raw is! Map) return const {};
  final out = <String, SkillResource>{};
  raw.forEach((k, v) {
    if (k is String && v is Map<String, dynamic>) {
      out[k] = SkillResource.fromJson(v);
    } else if (k is String && v is Map) {
      out[k] = SkillResource.fromJson(v.cast<String, dynamic>());
    }
  });
  return out;
}

class AgentSkill {
  final String agentId;
  final String skillId;
  final bool isEnabled;
  final bool pinned;
  final DateTime addedAt;
  const AgentSkill({
    required this.agentId,
    required this.skillId,
    required this.isEnabled,
    required this.pinned,
    required this.addedAt,
  });

  factory AgentSkill.fromJson(Map<String, dynamic> j) => AgentSkill(
        agentId: j['agent_id'] as String? ?? '',
        skillId: j['skill_id'] as String? ?? '',
        isEnabled: j['is_enabled'] as bool? ?? false,
        pinned: j['pinned'] as bool? ?? false,
        addedAt: DateTime.tryParse(j['added_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
      );
}

List<String> _stringList(Object? v) {
  if (v is List) {
    return v.whereType<String>().toList(growable: false);
  }
  return const [];
}

/// Compact view of the prior skill row that a propose-update points at.
/// Surfaced in ProposeResult.previous so the approver UI can render a
/// diff without a follow-up GET — the handler at
/// services/runtime/internal/api/skills_propose_handlers.go inlines
/// the same snippet under the JSON key `update_of`.
class PreviousSkillVersion {
  final String id;
  final String identifier;
  final String contentHash;
  final String content;

  const PreviousSkillVersion({
    required this.id,
    required this.identifier,
    required this.contentHash,
    required this.content,
  });

  factory PreviousSkillVersion.fromJson(Map<String, dynamic> j) =>
      PreviousSkillVersion(
        id: j['id'] as String? ?? '',
        identifier: j['identifier'] as String? ?? '',
        contentHash: j['content_hash'] as String? ?? '',
        content: j['content'] as String? ?? '',
      );

  /// 16-char prefix of the new vs old content_hash, suitable for a
  /// one-line summary like "abcd1234… → ef567890…". Empty when either
  /// side is missing.
  String diffSummary(String newContentHash) {
    if (contentHash.isEmpty || newContentHash.isEmpty) return '';
    String trim(String h) => h.length >= 16 ? h.substring(0, 16) : h;
    return '${trim(contentHash)}… → ${trim(newContentHash)}…';
  }
}

class ProposeResult {
  final Skill skill;
  /// Non-null when the propose carried an `update_of` pointing to an
  /// existing skill in the same org. Null on a fresh propose.
  final PreviousSkillVersion? previous;

  const ProposeResult({required this.skill, this.previous});

  factory ProposeResult.fromJson(Map<String, dynamic> j) {
    PreviousSkillVersion? prev;
    final raw = j['update_of'];
    if (raw is Map<String, dynamic>) {
      prev = PreviousSkillVersion.fromJson(raw);
    }
    return ProposeResult(skill: Skill.fromJson(j), previous: prev);
  }
}

/// One row from runtime.skill_activations. Matches the wire shape
/// emitted by GET /v1/skills/{id}/activations (services/runtime/
/// internal/api/skills_handlers.go handleListSkillActivations).
class SkillActivation {
  final String id;
  final String sessionId;
  final String trigger;
  final String traceId;
  final int tokensIn;
  final int tokensOut;
  final DateTime occurredAt;

  const SkillActivation({
    required this.id,
    required this.sessionId,
    required this.trigger,
    required this.traceId,
    required this.tokensIn,
    required this.tokensOut,
    required this.occurredAt,
  });

  factory SkillActivation.fromJson(Map<String, dynamic> j) => SkillActivation(
        id: j['id'] as String? ?? '',
        sessionId: j['session_id'] as String? ?? '',
        trigger: j['trigger'] as String? ?? '',
        traceId: j['trace_id'] as String? ?? '',
        tokensIn: (j['tokens_in'] as num?)?.toInt() ?? 0,
        tokensOut: (j['tokens_out'] as num?)?.toInt() ?? 0,
        occurredAt: DateTime.tryParse(j['occurred_at'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
      );
}

class SkillActivationsResult {
  /// Total count across all rows (not just the returned page).
  final int count;
  /// Last (most recent) occurred_at — null when count == 0.
  final DateTime? lastAt;
  /// Most-recent-first slice, capped by the request's limit.
  final List<SkillActivation> items;

  const SkillActivationsResult({
    required this.count,
    required this.lastAt,
    required this.items,
  });

  factory SkillActivationsResult.fromJson(Map<String, dynamic> j) {
    final stats = (j['stats'] as Map?)?.cast<String, dynamic>() ?? {};
    final rawItems = (j['items'] as List?) ?? [];
    return SkillActivationsResult(
      count: (stats['count'] as num?)?.toInt() ?? 0,
      lastAt: stats['last_at'] is String
          ? DateTime.tryParse(stats['last_at'] as String)
          : null,
      items: rawItems
          .whereType<Map<String, dynamic>>()
          .map(SkillActivation.fromJson)
          .toList(growable: false),
    );
  }
}

class SkillClient {
  SkillClient(this.baseUrl, this.bearerToken);

  final Uri baseUrl;
  final String bearerToken;

  // ─── List ──────────────────────────────────────────────

  Future<List<Skill>> list({String? source, String? status, String? ownerId}) async {
    final qp = <String, String>{};
    if (source != null) qp['source'] = source;
    if (status != null) qp['status'] = status;
    if (ownerId != null) qp['owner_id'] = ownerId;
    final raw = await _get('/v1/skills', queryParams: qp.isEmpty ? null : qp);
    return (raw['skills'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(Skill.fromJson)
        .toList();
  }

  // ─── Get ───────────────────────────────────────────────

  Future<Skill> get(String id) async {
    final raw = await _get('/v1/skills/$id');
    return Skill.fromJson(raw);
  }

  // ─── Install (3 source variants) ───────────────────────

  /// Inline install — frontmatter + body composed in the UI editor.
  Future<Skill> installInline({
    required String identifier,
    required String name,
    required String description,
    required String body,
    List<String>? paths,
    List<String>? permissions,
    String? targetAgentId,
    bool pin = false,
  }) async {
    final reqBody = <String, dynamic>{
      'identifier': identifier,
      'name': name,
      'description': description,
      'body': body,
      'paths': ?paths,
      'permissions': ?permissions,
      'target_agent_id': ?targetAgentId,
      if (pin) 'pin': true,
    };
    final raw = await _post('/v1/skills', reqBody);
    return Skill.fromJson(raw);
  }

  /// URL install — server fetches the SKILL.md.
  Future<Skill> installFromUrl(String url, {String? targetAgentId, bool pin = false}) async {
    final reqBody = <String, dynamic>{
      'url': url,
      'target_agent_id': ?targetAgentId,
      if (pin) 'pin': true,
    };
    final raw = await _post('/v1/skills', reqBody);
    return Skill.fromJson(raw);
  }

  /// Zip install — base64-encoded .biuskill bundle.
  Future<Skill> installFromZip(String zipBase64, {String? targetAgentId, bool pin = false}) async {
    final reqBody = <String, dynamic>{
      'zip_b64': zipBase64,
      'target_agent_id': ?targetAgentId,
      if (pin) 'pin': true,
    };
    final raw = await _post('/v1/skills', reqBody);
    return Skill.fromJson(raw);
  }

  // ─── Update / Delete ───────────────────────────────────

  Future<Skill> update(String id, {
    String? description,
    String? body,
    List<String>? paths,
    List<String>? permissions,
  }) async {
    final reqBody = <String, dynamic>{
      'description': ?description,
      'body': ?body,
      'paths': ?paths,
      'permissions': ?permissions,
    };
    final raw = await _patch('/v1/skills/$id', reqBody);
    return Skill.fromJson(raw);
  }

  Future<void> delete(String id) async {
    await _delete('/v1/skills/$id');
  }

  // ─── Self-authoring workflow (PS3.1) ───────────────────

  /// Result of /v1/skills/propose. When the request carried an
  /// `update_of` pointer, the handler attaches a snippet of the
  /// previous version (content + content_hash) so the approver UI
  /// can render a diff without an extra round-trip. `previous` is
  /// null on a fresh propose.
  Future<ProposeResult> propose({
    required String identifier,
    required String name,
    required String description,
    required String body,
    List<String>? paths,
    List<String>? permissions,
    String? updateOf,
  }) async {
    final reqBody = <String, dynamic>{
      'identifier': identifier,
      'name': name,
      'description': description,
      'body': body,
      'paths': ?paths,
      'permissions': ?permissions,
      'update_of': ?updateOf,
    };
    final raw = await _post('/v1/skills/propose', reqBody);
    return ProposeResult.fromJson(raw);
  }

  Future<Skill> approve(String id, {bool enableOnDefaultAgent = false}) async {
    final raw = await _post('/v1/skills/$id/approve', {
      'enable_on_default_agent': enableOnDefaultAgent,
    });
    return Skill.fromJson(raw);
  }

  Future<Skill> reject(String id, {String reason = ''}) async {
    final raw = await _post('/v1/skills/$id/reject', {'reason': reason});
    return Skill.fromJson(raw);
  }

  Future<Skill> shareOrg(String id) async {
    final raw = await _post('/v1/skills/$id/share-org', {});
    return Skill.fromJson(raw);
  }

  // ─── Toggle ────────────────────────────────────────────

  Future<AgentSkill> toggle(String skillId,
      {required String agentId, required bool isEnabled, bool pinned = false}) async {
    final raw = await _post('/v1/skills/$skillId/toggle', {
      'agent_id': agentId,
      'is_enabled': isEnabled,
      'pinned': pinned,
    });
    return AgentSkill.fromJson(raw);
  }

  // ─── Activations ───────────────────────────────────────

  /// GET /v1/skills/{id}/activations — recent activation ledger plus
  /// summary stats for the detail drawer's "调用 N 次 / 最后 X 时间前"
  /// panel. limit caps the items list (server caps at 1000 anyway).
  Future<SkillActivationsResult> activations(String skillId, {int limit = 50}) async {
    final raw = await _get('/v1/skills/$skillId/activations',
        queryParams: {'limit': '$limit'});
    return SkillActivationsResult.fromJson(raw);
  }

  // ─── HTTP plumbing ─────────────────────────────────────

  Future<Map<String, dynamic>> _get(String path, {Map<String, String>? queryParams}) =>
      _request('GET', path, queryParams: queryParams);
  Future<Map<String, dynamic>> _post(String path, Map<String, dynamic> body) =>
      _request('POST', path, body: body);
  Future<Map<String, dynamic>> _patch(String path, Map<String, dynamic> body) =>
      _request('PATCH', path, body: body);
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
      throw SkillApiError(method: method, path: path, status: e.status, body: e.body);
    }
  }
}

class SkillApiError implements Exception {
  final String method;
  final String path;
  final int status;
  final String body;
  const SkillApiError({
    required this.method,
    required this.path,
    required this.status,
    required this.body,
  });

  bool get isNotFound => status == 404;
  bool get isForbidden => status == 403;
  bool get isUnauthorized => status == 401;
  bool get isConflict => status == 409;
  bool get isTooLarge => status == 413;

  @override
  String toString() => 'SkillApiError $status $method $path: $body';
}
