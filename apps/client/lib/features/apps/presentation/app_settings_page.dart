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
    final freshAsync = await ref.read(upgradeStatusProvider(row.install.id).future);
    if (!context.mounted) return;
    if (freshAsync == null || !freshAsync.available) {
      // 仅失效这条 install 的状态; 之前 invalidate 整个 family 会让其他
      // pending 行的 status 缓存全部抛弃 → 升级 banner 抖动 + 多余请求。
      ref.invalidate(upgradeStatusProvider(row.install.id));
      return;
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
    return ListTile(
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
