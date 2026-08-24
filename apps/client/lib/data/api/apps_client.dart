// AppsClient — Dart client for the App Center HTTP surface.
//
// Mirrors services/app_center/internal/api 1:1:
//
//   GET    /v1/apps                              catalog (Manifest list)
//   GET    /v1/apps/{name}                       one Manifest
//   POST   /v1/apps/{name}/invoke                v1.0 invoke (action+input)
//   GET    /v1/apps/installs?scope=user|org      this caller's installs
//   POST   /v1/apps/installs                     install
//   GET    /v1/apps/installs/{id}                detail
//   DELETE /v1/apps/installs/{id}                uninstall
//   PATCH  /v1/apps/installs/{id}                toggle (enabled flag)
//   GET    /v1/apps/installs/{id}/agents         list agent grants
//   POST   /v1/apps/installs/{id}/agents         grant
//   DELETE /v1/apps/installs/{id}/agents/{aid}   revoke
//   POST   /v1/apps/repo/analyze                 analyze a GitHub repo (Repo Apps)
//   POST   /v1/apps/repo/installs                install from a GitHub repo
//   GET    /v1/apps/installs/{id}/runtime        repo app runtime status
//   GET    /v1/apps/installs/{id}/builds         repo app build history
//   POST   /v1/apps/installs/{id}/redeploy       redeploy (new build)
//
// Bearer JWT carries the org claim; the client just passes the token
// through. JSON keys must match the Go server 1:1 — pinned by
// apps_client_test.dart.

import '_http_helpers.dart';

/// AppCatalogEntry — one row from GET /v1/apps. Mirrors the public
/// fields of biuapp.Manifest the server returns; we don't try to
/// surface the full v1.5 manifest schema here (views / triggers /
/// skills) because those are renderer concerns and live on
/// AppDetail when fetched individually.
class AppCatalogEntry {
  final String identifier;
  final String name;        // display name (Manifest.Title or fallback to slug)
  final String description;
  final String version;
  final String author;
  final String icon;
  final String category;    // 'productivity'|'content'|'data'|'comm'|'dev'|'utility'
  final String kind;        // 'backend'|'view'|'hybrid'|'webview'|'container'
  final List<String> permissions;

  /// Repo Apps (M1): optional fields, present only for GitHub-sourced
  /// apps. Parsed defensively — absent/null on every non-repo row.
  final String tier;        // '' | 'repo' | ... (server-defined)
  final RepoMeta? repoMeta;

  const AppCatalogEntry({
    required this.identifier,
    required this.name,
    required this.description,
    required this.version,
    this.author = '',
    this.icon = '',
    this.category = 'utility',
    this.kind = 'backend',
    this.permissions = const [],
    this.tier = '',
    this.repoMeta,
  });

  factory AppCatalogEntry.fromJson(Map<String, dynamic> j) {
    // Server returns the legacy v1.0 Manifest shape: `name` is the
    // slug, `title` (if present) is the display name. Prefer
    // identifier when set; fall back to name.
    final slug = (j['identifier'] as String?) ?? (j['name'] as String? ?? '');
    final title = j['title'] as String? ?? j['name'] as String? ?? slug;
    final perms = (j['permissions'] as List?)?.whereType<String>().toList() ?? const <String>[];
    return AppCatalogEntry(
      identifier:  slug,
      name:        title,
      description: j['description'] as String? ?? '',
      version:     j['version'] as String? ?? '',
      author:      j['author'] as String? ?? '',
      icon:        j['icon'] as String? ?? '',
      category:    j['category'] as String? ?? 'utility',
      kind:        j['kind'] as String? ?? 'backend',
      permissions: perms,
      tier:        j['tier'] as String? ?? '',
      repoMeta:    RepoMeta.tryParse(j['repo_meta']),
    );
  }
}

/// Installation — one row from GET /v1/apps/installs.
class Installation {
  final String id;
  final String scope;       // 'user' | 'org'
  final String scopeId;
  final String appId;
  final String identifier;
  final String version;
  final bool enabled;
  final String pinnedVersion;
  final List<String> permissionsGranted;
  final Map<String, dynamic> config;
  final bool forced;
  final DateTime installedAt;
  final DateTime updatedAt;
  final String installedBy;

  const Installation({
    required this.id,
    required this.scope,
    required this.scopeId,
    required this.appId,
    required this.identifier,
    required this.version,
    required this.enabled,
    this.pinnedVersion = '',
    this.permissionsGranted = const [],
    this.config = const {},
    this.forced = false,
    required this.installedAt,
    required this.updatedAt,
    this.installedBy = '',
  });

  factory Installation.fromJson(Map<String, dynamic> j) {
    final permsList = (j['permissions_granted'] ?? j['PermissionsGranted']) as List?;
    final cfgMap = (j['config'] ?? j['Config']) as Map<String, dynamic>? ?? const {};
    return Installation(
      id:                  j['id'] as String? ?? j['ID'] as String? ?? '',
      scope:               j['scope'] as String? ?? j['Scope'] as String? ?? 'user',
      scopeId:             j['scope_id'] as String? ?? j['ScopeID'] as String? ?? '',
      appId:               j['app_id'] as String? ?? j['AppID'] as String? ?? '',
      identifier:          j['identifier'] as String? ?? j['Identifier'] as String? ?? '',
      version:             j['version'] as String? ?? j['Version'] as String? ?? '',
      enabled:             j['enabled'] as bool? ?? j['Enabled'] as bool? ?? true,
      pinnedVersion:       j['pinned_version'] as String? ?? j['PinnedVersion'] as String? ?? '',
      permissionsGranted:  permsList?.whereType<String>().toList() ?? const <String>[],
      config:              cfgMap,
      forced:              j['forced'] as bool? ?? j['Forced'] as bool? ?? false,
      installedAt:         _parseTime(j['installed_at'] ?? j['InstalledAt']),
      updatedAt:           _parseTime(j['updated_at'] ?? j['UpdatedAt']),
      installedBy:         j['installed_by'] as String? ?? j['InstalledBy'] as String? ?? '',
    );
  }
}

class AgentGrant {
  final String agentId;
  final String installId;
  final bool enabled;
  final DateTime addedAt;

  const AgentGrant({
    required this.agentId,
    required this.installId,
    required this.enabled,
    required this.addedAt,
  });

  factory AgentGrant.fromJson(Map<String, dynamic> j) => AgentGrant(
        agentId:   j['agent_id'] as String? ?? j['AgentID'] as String? ?? '',
        installId: j['install_id'] as String? ?? j['InstallID'] as String? ?? '',
        enabled:   j['enabled'] as bool? ?? j['Enabled'] as bool? ?? true,
        addedAt:   _parseTime(j['added_at'] ?? j['AddedAt']),
      );
}

/// PermsDiff mirrors installs.PermsDiff (Go side). Three buckets so
/// the Modal can render added (red) / removed (grey) / unchanged
/// (collapsed) without re-deriving anywhere.
class PermsDiff {
  final List<String> added;
  final List<String> removed;
  final List<String> unchanged;

  const PermsDiff({
    this.added = const [],
    this.removed = const [],
    this.unchanged = const [],
  });

  bool get isBreaking => added.isNotEmpty;

  factory PermsDiff.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const PermsDiff();
    List<String> list(String k) =>
        ((j[k] as List?) ?? const []).whereType<String>().toList(growable: false);
    return PermsDiff(
      added:     list('added'),
      removed:   list('removed'),
      unchanged: list('unchanged'),
    );
  }
}

/// Result of GET /v1/apps/installs/{id}/upgrade. UI uses this to
/// decide whether to show the upgrade banner and which copy to put
/// in the Modal.
class UpgradeStatus {
  final bool available;
  final String currentVersion;
  final String targetVersion;
  final bool requiresApproval;
  final bool pinned;
  final PermsDiff permsDiff;

  const UpgradeStatus({
    required this.available,
    required this.currentVersion,
    this.targetVersion = '',
    this.requiresApproval = false,
    this.pinned = false,
    this.permsDiff = const PermsDiff(),
  });

  factory UpgradeStatus.fromJson(Map<String, dynamic> j) => UpgradeStatus(
        available:        j['available'] as bool? ?? false,
        currentVersion:   j['current_version'] as String? ?? '',
        targetVersion:    j['target_version'] as String? ?? '',
        requiresApproval: j['requires_approval'] as bool? ?? false,
        pinned:           j['pinned'] as bool? ?? false,
        permsDiff:        PermsDiff.fromJson(j['perms_diff'] as Map<String, dynamic>?),
      );
}

DateTime _parseTime(Object? v) {
  if (v is String && v.isNotEmpty) {
    return DateTime.tryParse(v)?.toLocal() ?? DateTime.fromMillisecondsSinceEpoch(0);
  }
  return DateTime.fromMillisecondsSinceEpoch(0);
}

// ─── Repo Apps (M1) — POST /v1/apps/repo/* + installs/{id}/runtime|builds ───
//
// 服务端是全新 struct, 一律带 snake_case json tag —— 这里只写 snake_case
// 解析, 不复制 Installation.fromJson 的双 key fallback 债。所有字段防御性
// 解析 (缺字段给默认值), 服务端加字段向前兼容。

/// RepoManifestDraft — analyze 返回的 manifest 草稿（identifier/title/...）。
class RepoManifestDraft {
  final String identifier;
  final String title;
  final String description;
  final String icon;
  final String version;
  final String category;

  const RepoManifestDraft({
    this.identifier = '',
    this.title = '',
    this.description = '',
    this.icon = '',
    this.version = '',
    this.category = 'utility',
  });

  factory RepoManifestDraft.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const RepoManifestDraft();
    return RepoManifestDraft(
      identifier:  j['identifier'] as String? ?? '',
      title:       j['title'] as String? ?? '',
      description: j['description'] as String? ?? '',
      icon:        j['icon'] as String? ?? '',
      version:     j['version'] as String? ?? '',
      category:    j['category'] as String? ?? 'utility',
    );
  }
}

/// RepoRuntimeReq — 运行时依赖要求（python3 / nodejs ...）。
class RepoRuntimeReq {
  final String name;
  final String version;
  final bool autoInstall;

  const RepoRuntimeReq({
    this.name = '',
    this.version = '',
    this.autoInstall = false,
  });

  factory RepoRuntimeReq.fromJson(Map<String, dynamic> j) => RepoRuntimeReq(
        name:        j['name'] as String? ?? '',
        version:     j['version'] as String? ?? '',
        autoInstall: j['auto_install'] as bool? ?? false,
      );
}

/// RepoStack — analyze 探测出的技术栈与启动方式。
class RepoStack {
  final String kind;
  final String installCmd;
  final String startCmd;
  final int port;
  final String healthPath;
  final List<RepoRuntimeReq> runtimeReqs;

  const RepoStack({
    this.kind = '',
    this.installCmd = '',
    this.startCmd = '',
    this.port = 0,
    this.healthPath = '',
    this.runtimeReqs = const [],
  });

  factory RepoStack.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const RepoStack();
    return RepoStack(
      kind:       j['kind'] as String? ?? '',
      installCmd: j['install_cmd'] as String? ?? '',
      startCmd:   j['start_cmd'] as String? ?? '',
      port:       (j['port'] as num?)?.toInt() ?? 0,
      healthPath: j['health_path'] as String? ?? '',
      runtimeReqs: ((j['runtime_reqs'] as List?) ?? const [])
          .whereType<Map<String, dynamic>>()
          .map(RepoRuntimeReq.fromJson)
          .toList(growable: false),
    );
  }
}

/// EnvField — analyze 返回的配置项 schema。`system` 字段由 CLI 注入，
/// 客户端表单不展示。
class EnvField {
  final String name;
  final String label;
  final bool secret;
  final String defaultValue;
  final bool optional;
  final bool system;

  const EnvField({
    required this.name,
    this.label = '',
    this.secret = false,
    this.defaultValue = '',
    this.optional = false,
    this.system = false,
  });

  factory EnvField.fromJson(Map<String, dynamic> j) => EnvField(
        name:         j['name'] as String? ?? '',
        label:        j['label'] as String? ?? '',
        secret:       j['secret'] as bool? ?? false,
        defaultValue: j['default'] as String? ?? '',
        optional:     j['optional'] as bool? ?? false,
        system:       j['system'] as bool? ?? false,
      );
}

/// RepoMeta — 仓库元信息（stars / license / 最新 release）。
class RepoMeta {
  final String url;
  final String defaultBranch;
  final String latestRef;
  final String latestSha;
  final int stars;
  final String license;

  const RepoMeta({
    this.url = '',
    this.defaultBranch = '',
    this.latestRef = '',
    this.latestSha = '',
    this.stars = 0,
    this.license = '',
  });

  factory RepoMeta.fromJson(Map<String, dynamic> j) => RepoMeta(
        url:           j['url'] as String? ?? '',
        defaultBranch: j['default_branch'] as String? ?? '',
        latestRef:     j['latest_ref'] as String? ?? '',
        latestSha:     j['latest_sha'] as String? ?? '',
        stars:         (j['stars'] as num?)?.toInt() ?? 0,
        license:       j['license'] as String? ?? '',
      );

  /// 防御性入口：catalog/manifest JSON 里的可选字段，缺失/类型不符 → null。
  static RepoMeta? tryParse(Object? v) {
    if (v is Map<String, dynamic>) return RepoMeta.fromJson(v);
    if (v is Map) return RepoMeta.fromJson(Map<String, dynamic>.from(v));
    return null;
  }
}

/// RepoAnalysis — POST /v1/apps/repo/analyze 的完整响应。
class RepoAnalysis {
  final RepoManifestDraft manifestDraft;
  final RepoStack stack;
  final List<EnvField> envSchema;
  final RepoMeta repoMeta;
  final List<String> warnings;

  const RepoAnalysis({
    this.manifestDraft = const RepoManifestDraft(),
    this.stack = const RepoStack(),
    this.envSchema = const [],
    this.repoMeta = const RepoMeta(),
    this.warnings = const [],
  });

  factory RepoAnalysis.fromJson(Map<String, dynamic> j) => RepoAnalysis(
        manifestDraft:
            RepoManifestDraft.fromJson(j['manifest_draft'] as Map<String, dynamic>?),
        stack: RepoStack.fromJson(j['stack'] as Map<String, dynamic>?),
        envSchema: ((j['env_schema'] as List?) ?? const [])
            .whereType<Map<String, dynamic>>()
            .map(EnvField.fromJson)
            .toList(growable: false),
        repoMeta: RepoMeta.tryParse(j['repo_meta']) ?? const RepoMeta(),
        warnings: ((j['warnings'] as List?) ?? const [])
            .whereType<String>()
            .toList(growable: false),
      );
}

/// RepoRuntimeStatus — GET /v1/apps/installs/{id}/runtime。
class RepoRuntimeStatus {
  final String mode;    // 'local' | ...
  final String status;  // 'stopped'|'starting'|'running'|'failed'
  final String url;     // running 时的 http://127.0.0.1:<port>

  const RepoRuntimeStatus({
    this.mode = '',
    this.status = '',
    this.url = '',
  });

  bool get isRunning => status == 'running' && url.isNotEmpty;

  factory RepoRuntimeStatus.fromJson(Map<String, dynamic> j) =>
      RepoRuntimeStatus(
        mode:   j['mode'] as String? ?? '',
        status: j['status'] as String? ?? '',
        url:    j['url'] as String? ?? '',
      );
}

/// RepoBuild — GET /v1/apps/installs/{id}/builds 里的一条构建记录。
class RepoBuild {
  final String id;
  final String ref;
  final String sha;
  final String status; // 'queued'|'building'|'live'|'failed' (server-defined)
  final int durationMs;
  final DateTime createdAt;

  const RepoBuild({
    this.id = '',
    this.ref = '',
    this.sha = '',
    this.status = '',
    this.durationMs = 0,
    required this.createdAt,
  });

  factory RepoBuild.fromJson(Map<String, dynamic> j) => RepoBuild(
        id:         j['id'] as String? ?? '',
        ref:        j['ref'] as String? ?? '',
        sha:        j['sha'] as String? ?? '',
        status:     j['status'] as String? ?? '',
        durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
        createdAt:  _parseTime(j['created_at']),
      );
}

/// RepoRedeployResult — POST /v1/apps/installs/{id}/redeploy 的响应。
/// M2 服务端在 build_id 之外补 ref/sha（本次构建锁定的 git 引用与
/// commit，客户端透传给 `biu repo-app update --ref`）；老服务端只有
/// build_id —— 全部防御性解析，缺字段给空串。
class RepoRedeployResult {
  final String buildId;
  final String ref;
  final String sha;

  const RepoRedeployResult({
    this.buildId = '',
    this.ref = '',
    this.sha = '',
  });

  factory RepoRedeployResult.fromJson(Map<String, dynamic> j) =>
      RepoRedeployResult(
        buildId: j['build_id'] as String? ?? '',
        ref:     j['ref'] as String? ?? '',
        sha:     j['sha'] as String? ?? '',
      );
}

/// AppsClient is the thin transport layer. Construct once with the
/// service base URL; pass the bearer token per-call so logout /
/// token rotation flows don't have to rebuild the client.
class AppsClient {
  final Uri baseUrl;
  AppsClient(this.baseUrl);

  /// GET /v1/apps — public catalogue. Note v1.5 keeps this path
  /// shape inherited from v1.0; the server returns whatever the
  /// caller's tenant sees (bundled + visible org/marketplace rows).
  Future<List<AppCatalogEntry>> listCatalog({required String token}) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps'),
      bearerToken: token,
    );
    final list = (j['apps'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(AppCatalogEntry.fromJson)
        .toList(growable: false);
  }

  /// GET /v1/apps/{name} — full manifest as opaque map. Callers that
  /// need only the catalog fields use listCatalog; the full map is
  /// for the detail page (which shows views / triggers / skills /
  /// permission breakdown).
  Future<Map<String, dynamic>> getManifest({
    required String identifier,
    required String token,
  }) async {
    return apiRequest(
      method: 'GET',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/$identifier'),
      bearerToken: token,
    );
  }

  /// POST /v1/apps/installs — install. Returns the new Installation.
  Future<Installation> install({
    required String identifier,
    required String scope,                   // 'user' | 'org'
    required List<String> grantedPermissions,
    Map<String, dynamic>? config,
    bool forced = false,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs'),
      bearerToken: token,
      body: {
        'identifier': identifier,
        'scope': scope,
        'granted_permissions': grantedPermissions,
        'config': ?config,
        if (forced) 'forced': true,
      },
    );
    return Installation.fromJson(j);
  }

  /// POST /v1/apps/user_webview — create-and-install a user webview
  /// app in one round-trip (M12). Caller-scope is always the current
  /// user; org-scope webview apps are not supported in v2.0.
  Future<Installation> createUserWebView({
    required String title,
    required String url,
    String? iconFileHash,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/user_webview'),
      bearerToken: token,
      body: {
        'title': title,
        'url': url,
        if (iconFileHash != null && iconFileHash.isNotEmpty)
          'icon_file_hash': iconFileHash,
      },
    );
    return Installation.fromJson(j);
  }

  /// GET /v1/apps/installs?scope=...
  Future<List<Installation>> listInstalls({
    String scope = 'user',
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs',
        queryParameters: {'scope': scope},
      ),
      bearerToken: token,
    );
    final list = (j['installations'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(Installation.fromJson)
        .toList(growable: false);
  }

  Future<Installation> getInstall({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs/$installId'),
      bearerToken: token,
    );
    return Installation.fromJson(j);
  }

  Future<void> uninstall({
    required String installId,
    required String token,
  }) async {
    await apiRequest(
      method: 'DELETE',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs/$installId'),
      bearerToken: token,
      expectNoBody: true,
    );
  }

  /// POST /v1/apps/{name}/invoke — call one App action with the given
  /// input. The runtime / app_center service routes through the
  /// in-process biuapp.Registry; result shape is whatever the App
  /// returned (apps that don't return a JSON object get wrapped under
  /// `result`). This is the v1.5 path AppViewHost uses to fetch view
  /// data — the dedicated /views/{view_id}/data endpoint lands in v2.0.
  Future<Map<String, dynamic>> invoke({
    required String identifier,
    required String action,
    Map<String, dynamic>? input,
    required String token,
  }) async {
    return apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/$identifier/invoke'),
      bearerToken: token,
      body: {
        'action': action,
        'input': input ?? const {},
      },
    );
  }

  /// PATCH toggles enabled. Pass null to no-op (server returns
  /// current row); the dialog uses an explicit bool.
  Future<Installation> toggle({
    required String installId,
    required bool enabled,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'PATCH',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs/$installId'),
      bearerToken: token,
      body: {'enabled': enabled},
    );
    return Installation.fromJson(j);
  }

  /// GET /v1/apps/installs/{id}/upgrade — read-only check.
  /// Returns the parsed UpgradeStatus shape; the Modal renders from it.
  Future<UpgradeStatus> checkUpgrade({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs/$installId/upgrade'),
      bearerToken: token,
    );
    return UpgradeStatus.fromJson(j);
  }

  /// POST /v1/apps/installs/{id}/upgrade — apply.
  /// acceptedNewPermissions is required iff upgradeStatus.requiresApproval
  /// AND status.permsDiff.added is non-empty; the server rejects with 400
  /// permissions_not_accepted otherwise.
  Future<Installation> upgrade({
    required String installId,
    List<String> acceptedNewPermissions = const [],
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/installs/$installId/upgrade'),
      bearerToken: token,
      body: {
        'accepted_new_permissions': acceptedNewPermissions,
      },
    );
    return Installation.fromJson(j);
  }

  Future<List<AgentGrant>> listAgentGrants({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/agents',
      ),
      bearerToken: token,
    );
    final list = (j['grants'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(AgentGrant.fromJson)
        .toList(growable: false);
  }

  Future<AgentGrant> grantAgent({
    required String installId,
    required String agentId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/agents',
      ),
      bearerToken: token,
      body: {'agent_id': agentId},
    );
    return AgentGrant.fromJson(j);
  }

  Future<void> revokeAgent({
    required String installId,
    required String agentId,
    required String token,
  }) async {
    await apiRequest(
      method: 'DELETE',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/agents/$agentId',
      ),
      bearerToken: token,
      expectNoBody: true,
    );
  }

  /// POST /v1/apps/repo/analyze — 分析一个 GitHub 仓库，返回 manifest
  /// 草稿 + 技术栈 + env schema。服务端对不支持的仓库类型返回 4xx +
  /// {"error":{"code","message"}}，UI 层直接展示 message（"不支持"原因）。
  Future<RepoAnalysis> analyzeRepo({
    required String repoUrl,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/repo/analyze'),
      bearerToken: token,
      body: {'repo_url': repoUrl},
    );
    return RepoAnalysis.fromJson(j);
  }

  /// POST /v1/apps/repo/installs — 从 GitHub 仓库安装。refType 为
  /// 'release' | 'branch'。config 只放非机密配置（D9：机密不上服务端，
  /// 由客户端经本机 CLI ensure 通道下发写入实例 .env）。
  Future<Installation> installRepo({
    required String repoUrl,
    required String refType,
    Map<String, dynamic> config = const {},
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(path: '${baseUrl.path}/v1/apps/repo/installs'),
      bearerToken: token,
      body: {
        'repo_url': repoUrl,
        'ref_type': refType,
        'config': config,
      },
    );
    return Installation.fromJson(j);
  }

  /// GET /v1/apps/installs/{id}/runtime — repo app 的运行时状态。
  Future<RepoRuntimeStatus> getRepoRuntime({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/runtime',
      ),
      bearerToken: token,
    );
    return RepoRuntimeStatus.fromJson(j);
  }

  /// GET /v1/apps/installs/{id}/builds — repo app 构建历史。
  Future<List<RepoBuild>> listRepoBuilds({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'GET',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/builds',
      ),
      bearerToken: token,
    );
    final list = (j['builds'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(RepoBuild.fromJson)
        .toList(growable: false);
  }

  /// POST /v1/apps/installs/{id}/redeploy — 触发一次新构建，返回
  /// build_id（M2 起另带 ref/sha，见 [RepoRedeployResult]）。
  Future<RepoRedeployResult> redeployRepo({
    required String installId,
    required String token,
  }) async {
    final j = await apiRequest(
      method: 'POST',
      url: baseUrl.replace(
        path: '${baseUrl.path}/v1/apps/installs/$installId/redeploy',
      ),
      bearerToken: token,
      body: const {},
    );
    return RepoRedeployResult.fromJson(j);
  }
}
