// Riverpod providers for the App Center surface.
//
// Mirrors data/skill_providers.dart and data/memory_providers.dart:
// the underlying client is null when credentials aren't set, and
// the UI degrades to a "configure Settings first" message rather
// than crashing. App Center HTTP routes (/v1/apps*) mount on the
// app_center service; site nginx reverse-proxies them —— client 单 origin,
// 不换端口(app_center :7011 从不公网直连).

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/apps_client.dart';

final appsClientProvider = Provider<AppsClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return AppsClient(creds.endpoint);
});

/// Bearer token slice — separate provider so refresh patterns can
/// invalidate the token without rebuilding the AppsClient (which
/// only depends on the endpoint).
final appsBearerProvider = Provider<String?>((ref) {
  return ref.watch(hubCredentialsProvider)?.bearerToken;
});

/// SWR-style auto-fetch of the public catalog. UI invalidates this
/// after install / uninstall to keep the "已安装" badge in sync.
final appsCatalogProvider = FutureProvider<List<AppCatalogEntry>>((ref) async {
  ref.watch(appsClientProvider.select((c) => c?.baseUrl));
  final client = ref.read(appsClientProvider);
  final token = ref.read(hubCredentialsProvider)?.bearerToken;
  if (client == null || token == null) return const [];
  return client.listCatalog(token: token);
});

/// Installation list scope. The UI exposes a toggle (我的 / 团队);
/// the provider key is the scope, so switching tabs hits a fresh
/// query rather than re-filtering on the client.
final installationsProvider = FutureProvider.family<List<Installation>, String>(
  (ref, scope) async {
    // select(baseUrl): token 轮换不重拉 (App Center 列表不闪); token 现读保新鲜.
    ref.watch(appsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return const [];
    return client.listInstalls(scope: scope, token: token);
  },
);

/// Single-installation provider used by the App detail / settings
/// pages. Family on installId so multiple opens don't share state.
final installationProvider = FutureProvider.family<Installation?, String>(
  (ref, installId) async {
    // select(baseUrl): token 轮换不重拉 (App Center 列表不闪); token 现读保新鲜.
    ref.watch(appsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return null;
    return client.getInstall(installId: installId, token: token);
  },
);

/// Manifest cache for the detail page. The catalog endpoint already
/// returns a slim view; this is the full manifest (views / triggers /
/// skills) used to render the install dialog's permission breakdown.
final manifestProvider = FutureProvider.family<Map<String, dynamic>, String>(
  (ref, identifier) async {
    // select(baseUrl): token 轮换不重拉 (App Center 列表不闪); token 现读保新鲜.
    ref.watch(appsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return const {};
    return client.getManifest(identifier: identifier, token: token);
  },
);

/// Helper: derived "is this app installed?" boolean. Pages use this
/// to swap "安装" / "已安装 / 卸载" buttons.
final isInstalledProvider = Provider.family<bool, String>((ref, identifier) {
  final installs = ref.watch(installationsProvider('user')).valueOrNull;
  if (installs == null) return false;
  return installs.any((i) => i.identifier == identifier);
});

/// Helper: which Installation row matches a slug? Returns null when
/// not installed for the current user. Used to wire detail page →
/// uninstall / settings actions.
final installationByIdentifierProvider =
    Provider.family<Installation?, String>((ref, identifier) {
  final installs = ref.watch(installationsProvider('user')).valueOrNull;
  if (installs == null) return null;
  for (final i in installs) {
    if (i.identifier == identifier) return i;
  }
  return null;
});

/// Refresh helpers — call from controllers post-success.
///
/// 设计原则: 失效粒度尽可能精准, 避免一次操作触发整个 catalog/install
/// list 重拉。家族 provider (installationsProvider / installationProvider /
/// upgradeStatusProvider) 都按 key 失效, 不动其他 key 的缓存。
///
/// 使用模板:
///   - install / uninstall / createUserWebView:
///       ref.invalidateInstallScope(scope);  // 'user' or 'org'
///       ref.invalidate(appsCatalogProvider);  // catalog "已安装" 徽章
///   - toggle / 单条 install 状态变化:
///       ref.invalidateInstall(installId);  // 单条 + 升级状态
///       ref.invalidateInstallScope(scope); // 列表 derived 状态
///   - upgrade 完成:
///       ref.invalidateInstall(installId);
///       ref.invalidateInstallScope(scope);
extension AppsRefresh on WidgetRef {
  /// 失效单条 installation 的 detail provider + 它的 upgrade 状态。
  void invalidateInstall(String installId) {
    invalidate(installationProvider(installId));
    invalidate(upgradeStatusProvider(installId));
  }

  /// 失效指定 scope 的安装列表（'user' 或 'org'）。比 `invalidate(installationsProvider)`
  /// 精准 —— 不会把另一个 scope 的缓存也清掉。
  void invalidateInstallScope(String scope) {
    invalidate(installationsProvider(scope));
  }

  /// 全量刷新 —— 仅在 sign-in / 切换组织等 broad context 切换时使用。
  void refreshAppCenterAll() {
    invalidate(appsCatalogProvider);
    invalidate(installationsProvider);
    invalidate(upgradeStatusProvider);
  }
}

/// Ref 版本（供 Riverpod 内部 / 非 widget 上下文使用）。
extension AppsRefreshOnRef on Ref {
  void invalidateInstall(String installId) {
    invalidate(installationProvider(installId));
    invalidate(upgradeStatusProvider(installId));
  }

  void invalidateInstallScope(String scope) {
    invalidate(installationsProvider(scope));
  }
}

/// Per-installation upgrade status. Family-keyed on install id so
/// the Modal + the upgrade banner share the same fetch result.
final upgradeStatusProvider = FutureProvider.family<UpgradeStatus?, String>(
  (ref, installId) async {
    // select(baseUrl): token 轮换不重拉 (App Center 列表不闪); token 现读保新鲜.
    ref.watch(appsClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return null;
    return client.checkUpgrade(installId: installId, token: token);
  },
);

/// Aggregate: every installation that has an upgrade waiting. The
/// settings page renders a "升级中 (N)" badge from this. We compute
/// it by mapping each install's upgradeStatusProvider — the family
/// caches per id so re-renders don't re-fetch.
final pendingUpgradesProvider = Provider<List<UpgradeRow>>((ref) {
  final installs = ref.watch(installationsProvider('user')).valueOrNull
      ?? const <Installation>[];
  final out = <UpgradeRow>[];
  for (final inst in installs) {
    final status = ref.watch(upgradeStatusProvider(inst.id)).valueOrNull;
    if (status == null || !status.available) continue;
    out.add(UpgradeRow(install: inst, status: status));
  }
  return out;
});

class UpgradeRow {
  final Installation install;
  final UpgradeStatus status;
  const UpgradeRow({required this.install, required this.status});
}
