// AppSettingsPage — Settings → 应用管理.
//
// Layout (top to bottom):
//   1. Upgrades section (only when pendingUpgradesProvider non-empty)
//      — banner + list of upgradable installs with "升级" button each
//   2. Installed list — per-row enable/disable switch + 卸载 button
//
// Tapping an install navigates to AppDetailPage. Upgrade buttons
// open UpgradeDialog (M15 permission diff Modal).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../data/agent_plane/repo_app_launcher.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/apps_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../shared/page_scaffold.dart';
import 'apps_error.dart';
import 'upgrade_dialog.dart';

class AppSettingsPage extends ConsumerWidget {
  const AppSettingsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final installsAsync = ref.watch(installationsProvider('user'));
    final pending = ref.watch(pendingUpgradesProvider);
    return PageScaffold(
      title: l10n.appsManageTitle,
      // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
      leading: const PhoneBackButton(),
      child: installsAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: SelectableText('Error: $e')),
        data: (rows) {
          if (rows.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(BiuTokens.space5),
                child: Text(l10n.appsNoInstalls,
                    style: Theme.of(context).textTheme.bodyMedium),
              ),
            );
          }
          return ListView(
            children: [
              if (pending.isNotEmpty) ...[
                _UpgradeSection(pending: pending),
                const Divider(height: 1),
              ],
              for (var i = 0; i < rows.length; i++) ...[
                _Row(
                  install: rows[i],
                  onTap: () => context.push('/apps/detail/${rows[i].identifier}'),
                  onToggle: (enabled) async {
                    final client = ref.read(appsClientProvider);
                    final token = ref.read(appsBearerProvider);
                    if (client == null || token == null) return;
                    await client.toggle(installId: rows[i].id, enabled: enabled, token: token);
                    ref.invalidateInstall(rows[i].id);
                    ref.invalidateInstallScope('user');
                  },
                  onUninstall: () async {
                    final client = ref.read(appsClientProvider);
                    final token = ref.read(appsBearerProvider);
                    if (client == null || token == null) return;
                    final ok = await showDialog<bool>(
                      context: context,
                      // dialogCtx 而非外层 context:对话框 push 在根 Navigator,
                      // 用 page context(ShellRoute 子 Navigator)pop 会弹掉本页、
                      // 对话框反而卡住。见 app_detail_page 同处注释。
                      builder: (dialogCtx) => AlertDialog(
                        title: Text(l10n.appsUninstallTitle),
                        content: Text(l10n.appsUninstallConfirm(rows[i].identifier)),
                        actions: [
                          TextButton(
                              onPressed: () => Navigator.of(dialogCtx).pop(false),
                              child: Text(l10n.appsCancel)),
                          FilledButton.tonal(
                              onPressed: () => Navigator.of(dialogCtx).pop(true),
                              child: Text(l10n.appsUninstall)),
                        ],
                      ),
                    );
                    if (ok != true) return;
                    await client.uninstall(installId: rows[i].id, token: token);
                    ref.invalidateInstall(rows[i].id);
                    ref.invalidateInstallScope('user');
                    ref.invalidate(appsCatalogProvider);
                  },
                ),
                if (i < rows.length - 1) const Divider(height: 1),
              ],
            ],
          );
        },
      ),
    );
  }
}

// ─── Upgrade section ─────────────────────────────────────────────

class _UpgradeSection extends ConsumerWidget {
  const _UpgradeSection({required this.pending});
  final List<UpgradeRow> pending;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final scheme = Theme.of(context).colorScheme;
    return Container(
      color: scheme.surfaceContainer,
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(
              BiuTokens.space3, BiuTokens.space2, BiuTokens.space3, 4,
            ),
            child: Row(
              children: [
                Icon(Icons.upgrade_rounded, size: 18, color: scheme.primary),
                const SizedBox(width: 6),
                Text(
                  l10n.upgradeBannerTitle,
                  style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      fontWeight: FontWeight.w700, color: scheme.primary),
                ),
                const SizedBox(width: 6),
                Text(
                  l10n.upgradeBannerSubtitle(pending.length),
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: scheme.onSurfaceVariant),
                ),
              ],
            ),
          ),
          for (final row in pending)
            ListTile(
              dense: true,
              leading: const Icon(Icons.widgets_outlined),
              title: Text(row.install.identifier),
              subtitle: Text(_formatVersion(l10n, row.status)),
              trailing: FilledButton(
                onPressed: () => _onUpgrade(context, ref, row),
                child: Text(l10n.upgradeApply),
              ),
            ),
        ],
      ),
    );
  }

  String _formatVersion(AppLocalizations l, UpgradeStatus s) =>
      l.upgradeRowVersion(s.currentVersion, s.targetVersion);

  Future<void> _onUpgrade(BuildContext context, WidgetRef ref, UpgradeRow row) async {
    final l10n = AppLocalizations.of(context)!;
    // Always re-fetch the status before showing the Modal — the
    // pending list may be stale (last refresh was N seconds ago and
    // the App might have already been bumped on another device).
    // 真 fresh：先失效缓存再 await —— 直接 read .future 命中的是
    // pendingUpgradesProvider 填入的缓存（伪 fresh，DevPlan M2.5）。
    ref.invalidate(upgradeStatusProvider(row.install.id));
    final freshAsync = await ref.read(upgradeStatusProvider(row.install.id).future);
    if (!context.mounted) return;
    if (freshAsync == null || !freshAsync.available) return;

    // Repo Apps (M2)：repo 应用走 redeploy + 本机 `biu repo-app
    // update`，跳过 UpgradeDialog（perms_diff 恒空，弹窗无意义）。
    // await future 而不是 valueOrNull —— 本页没人 watch catalog，read
    // 拿到的是尚未 resolve 的 AsyncValue（会把 repo 应用误判成普通
    // 应用）。
    final catalog = await ref.read(appsCatalogProvider.future);
    if (!context.mounted) return;
    final repoMeta = _repoMetaOf(catalog, row.install);
    if (repoMeta != null) {
      return _onRepoUpgrade(context, ref, row, freshAsync, repoMeta);
    }

    final accepted = await UpgradeDialog.show(
      context,
      appName: row.install.identifier,
      status: freshAsync,
    );
    if (accepted == null) return;
    if (!context.mounted) return;

    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      await client.upgrade(
        installId: row.install.id,
        acceptedNewPermissions: accepted,
        token: token,
      );
      ref.invalidateInstall(row.install.id);
      ref.invalidateInstallScope('user');
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.upgradeAppliedToast)),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(humanizeAppsError(context, e))),
        );
      }
    }
  }

  /// repo 判定：Installation 不带 repo_meta —— 按 identifier 从
  /// catalog 查（AppCatalogEntry.repoMeta 防御性解析，非 repo 应用
  /// 为 null）。catalog 缺该 identifier → 视为非 repo，走原升级流程。
  RepoMeta? _repoMetaOf(List<AppCatalogEntry> catalog, Installation install) {
    for (final e in catalog) {
      if (e.identifier == install.identifier) return e.repoMeta;
    }
    return null;
  }

  /// Repo Apps 一键更新（M2）：确认 → 服务端 redeploy 触发新构建 →
  /// 本机 `biu repo-app update` 执行 stop→fetch→装依赖→start（失败
  /// CLI 自动回切旧版）→ 精准失效 → toast。
  Future<void> _onRepoUpgrade(
    BuildContext context,
    WidgetRef ref,
    UpgradeRow row,
    UpgradeStatus status,
    RepoMeta repoMeta,
  ) async {
    final l10n = AppLocalizations.of(context)!;
    // 平台门控（C5：caps 判定，不写裸 Platform.isXXX）：update 依赖
    // 本机 CLI runner，仅 macOS / Linux。
    if (!ref.read(platformCapsProvider).hasRepoAppRunner) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.repoUpgradeUnsupportedPlatform)),
      );
      return;
    }
    final versionLabel = status.targetVersion.isEmpty
        ? l10n.repoUpgradeLatestVersion
        : 'v${status.targetVersion}';
    final ok = await showDialog<bool>(
      context: context,
      // dialogCtx 而非外层 context —— 同上方卸载确认框注释。
      builder: (dialogCtx) => AlertDialog(
        title: Text(l10n.repoUpgradeConfirmTitle),
        content: Text(l10n.repoUpgradeConfirmBody(versionLabel)),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(dialogCtx).pop(false),
              child: Text(l10n.appsCancel)),
          FilledButton(
              onPressed: () => Navigator.of(dialogCtx).pop(true),
              child: Text(l10n.upgradeApply)),
        ],
      ),
    );
    if (ok != true) return;
    if (!context.mounted) return;

    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      // 1) 服务端触发新构建（M2 起返回 ref/sha；老服务端只有
      //    build_id，ref/sha 空串）。
      final redeploy =
          await client.redeployRepo(installId: row.install.id, token: token);
      // 2) slug 与 CLI repoapp.sanitiseForFS 同规则推导。
      final slug = RepoAppLauncher.slugFromRepoUrl(repoMeta.url);
      if (slug == null) {
        throw RepoAppEnsureException(l10n.repoUpgradeBadRepoUrl(repoMeta.url));
      }
      // 3) 本机 runner 执行更新。ref 缺失时退化用 sha，再空则省略
      //    --ref（CLI 回退已安装的 ref）。
      await ref.read(repoAppLauncherProvider).updateRepoApp(
            slug: slug,
            installId: row.install.id,
            buildId: redeploy.buildId,
            reportUrl: client.baseUrl.toString(),
            ref: redeploy.ref.isNotEmpty ? redeploy.ref : redeploy.sha,
          );
      ref.invalidateInstall(row.install.id);
      ref.invalidateInstallScope('user');
      ref.invalidateRepoBuilds(row.install.id);
      ref.invalidate(appsCatalogProvider);
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.upgradeAppliedToast)),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(humanizeAppsError(context, e))),
        );
      }
    }
  }
}

// ─── Per-install row ─────────────────────────────────────────────

class _Row extends ConsumerWidget {
  const _Row({
    required this.install,
    required this.onTap,
    required this.onToggle,
    required this.onUninstall,
  });

  final Installation install;
  final VoidCallback onTap;
  final ValueChanged<bool> onToggle;
  final VoidCallback onUninstall;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    // 懒加载 manifest 看 app 是否有 view —— 只有 hybrid / view / webview
    // 才显示快捷"打开"按钮。manifestProvider 是 family 级缓存, AppDetailPage
    // 已经访问过的 identifier 在这里命中缓存不会再发请求。
    final manifest = ref.watch(manifestProvider(install.identifier)).valueOrNull;
    final homeViewId = manifest == null ? null : _firstViewId(manifest);
    // Material 包裹：ListTile 的 ink splash / 背景画在最近 Material 祖先
    // 上；PageScaffold 的 ColoredBox 会挡住（debug 断言 + ripple 不可见）。
    return Material(
      color: BiuTokens.bg,
      child: ListTile(
        onTap: onTap,
        leading: CircleAvatar(
          child: Text(install.identifier.isEmpty ? '?'
              : install.identifier.characters.first.toUpperCase()),
        ),
        title: Text(install.identifier),
        subtitle: Text('v${install.version} · ${install.scope}'),
        trailing: Wrap(
          spacing: BiuTokens.space2,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            if (homeViewId != null)
              IconButton(
                tooltip: l10n.appsOpen,
                onPressed: install.enabled
                    ? () => context.push('/apps/host/${install.id}/$homeViewId')
                    : null,
                icon: const Icon(Icons.open_in_new),
              ),
            Switch(value: install.enabled, onChanged: install.forced ? null : onToggle),
            IconButton(
              tooltip: l10n.appsUninstall,
              onPressed: install.forced ? null : onUninstall,
              icon: const Icon(Icons.delete_outline),
            ),
          ],
        ),
      ),
    );
  }
}

/// 取 manifest 第一个 view 的 id (优先 'home'); backend-only app 返回 null。
String? _firstViewId(Map<String, dynamic> manifest) {
  final views = (manifest['views'] as List?)
          ?.whereType<Map<String, dynamic>>()
          .toList(growable: false) ??
      const <Map<String, dynamic>>[];
  if (views.isEmpty) return null;
  for (final v in views) {
    if (v['id'] == 'home') return 'home';
  }
  final first = views.first['id'];
  return first is String && first.isNotEmpty ? first : null;
}
