// AppsPage — top-level 「应用中心」 tab.
//
// Layout (matches Skills/Wiki visual rhythm):
//   AppBar:   title + 搜索 + ⚙ 管理（→ AppSettingsPage）
//   Filter:   segmented chips (全部 / 已安装 / Productivity / Content / Data / Comm / Dev / Utility)
//   Body:     responsive grid — 5 col desktop / 3 col tablet / 2 col mobile
//             tile = AppTile（icon + name + description + 已安装徽章）
//
// Tap a tile → AppDetailPage. The detail page handles install /
// uninstall flows; this page is purely catalogue navigation.
//
// Cloud calls go through appsClientProvider; the page shows a
// "configure Settings first" placeholder when model-relay credentials
// aren't set, mirroring SkillsPage.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/ui/popup_position.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/api/sidebar_client.dart';
import '../../../data/apps_providers.dart';
import '../../../data/dev_apps_provider.dart';
import '../../../data/sidebar_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../services/auth_service.dart';
import '../../../shared/page_scaffold.dart';
import 'add_webview_dialog.dart';
import 'app_icon_resolver.dart';
import 'app_tile.dart';
import 'apps_error.dart';

class AppsPage extends ConsumerStatefulWidget {
  const AppsPage({super.key});

  @override
  ConsumerState<AppsPage> createState() => _AppsPageState();
}

class _AppsPageState extends ConsumerState<AppsPage> {
  String _category = ''; // '' = 全部; 'installed' = 仅已安装; 否则按 category 过滤
  String _query = '';

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    // select(null-bool): 仅 configure↔ready 翻转重建; token 轮换不闪.
    ref.watch(appsClientProvider.select((c) => c == null));
    final client = ref.read(appsClientProvider);
    if (client == null) {
      return PageScaffold(
        title: l10n.appsTitle,
        child: _ConfigurePlaceholder(message: l10n.appsConfigureFirst),
      );
    }

    final catalogAsync = ref.watch(appsCatalogProvider);
    final installs = ref.watch(installationsProvider('user')).valueOrNull ?? const [];
    final devApps = ref.watch(devAppsProvider).valueOrNull ?? const [];

    // Dev events feed — when the SSE stream emits any event, refresh the
    // dev apps list so manifest hot-reloads land in the UI within ~1s.
    ref.listen(devAppsEventsProvider, (_, _) {
      ref.invalidate(devAppsProvider);
    });

    return PageScaffold(
      title: l10n.appsTitle,
      actions: [
        IconButton(
          tooltip: '添加 WebView 应用',
          onPressed: () async {
            final install = await showAddWebViewDialog(context);
            if (install != null && context.mounted) {
              // 跳转到刚创建的 webview 详情页
              context.push('/apps/detail/${install.identifier}');
            }
          },
          icon: const Icon(Icons.add_link),
        ),
        IconButton(
          tooltip: l10n.appsRefresh,
          onPressed: () {
            // 用户主动点 refresh —— 当前页只 watch scope='user' 的列表,
            // 没必要清掉 'org'/全 family 的缓存。
            ref.invalidate(appsCatalogProvider);
            ref.invalidateInstallScope('user');
          },
          icon: const Icon(Icons.refresh),
        ),
        IconButton(
          tooltip: l10n.appsManage,
          onPressed: () => context.push('/apps/installed'),
          icon: const Icon(Icons.settings_outlined),
        ),
      ],
      child: catalogAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: SelectableText('Error: $e')),
        data: (catalog) => Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _SearchAndFilters(
              query: _query,
              category: _category,
              installedCount: installs.length,
              onQueryChanged: (v) => setState(() => _query = v),
              onCategoryChanged: (v) => setState(() => _category = v),
            ),
            const SizedBox(height: BiuTokens.space4),
            if (devApps.isNotEmpty) ...[
              _DevAppsSection(apps: devApps),
              const SizedBox(height: BiuTokens.space4),
            ],
            Expanded(
              child: _AppGrid(
                entries: _filter(catalog, installs),
                installs: installs,
                onTap: (slug) => context.push('/apps/detail/$slug'),
                onSecondaryTap: (entry, position) =>
                    _showContextMenu(context, ref, entry, installs, position),
              ),
            ),
          ],
        ),
      ),
    );
  }

  List<AppCatalogEntry> _filter(List<AppCatalogEntry> all, List<Installation> installs) {
    final installedSet = installs.map((i) => i.identifier).toSet();
    final q = _query.trim().toLowerCase();
    return all.where((e) {
      if (_category == 'installed') {
        if (!installedSet.contains(e.identifier)) return false;
      } else if (_category.isNotEmpty) {
        if (e.category != _category) return false;
      }
      if (q.isNotEmpty) {
        if (!e.name.toLowerCase().contains(q) &&
            !e.identifier.toLowerCase().contains(q) &&
            !e.description.toLowerCase().contains(q)) {
          return false;
        }
      }
      return true;
    }).toList(growable: false);
  }
}

class _SearchAndFilters extends StatelessWidget {
  const _SearchAndFilters({
    required this.query,
    required this.category,
    required this.installedCount,
    required this.onQueryChanged,
    required this.onCategoryChanged,
  });

  final String query;
  final String category;
  final int installedCount;
  final ValueChanged<String> onQueryChanged;
  final ValueChanged<String> onCategoryChanged;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final cats = <(String key, String label)>[
      ('', l10n.appsCategoryAll),
      ('installed', l10n.appsCategoryInstalled(installedCount)),
      ('productivity', l10n.appsCategoryProductivity),
      ('content', l10n.appsCategoryContent),
      ('data', l10n.appsCategoryData),
      ('comm', l10n.appsCategoryComm),
      ('dev', l10n.appsCategoryDev),
      ('utility', l10n.appsCategoryUtility),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          height: 40,
          child: TextField(
            decoration: InputDecoration(
              hintText: l10n.appsSearchHint,
              prefixIcon: const Icon(Icons.search, size: 20),
              isDense: true,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(20),
              ),
            ),
            onChanged: onQueryChanged,
          ),
        ),
        const SizedBox(height: BiuTokens.space3),
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: Wrap(
            spacing: BiuTokens.space2,
            children: cats.map((c) {
              final selected = c.$1 == category;
              return ChoiceChip(
                label: Text(c.$2),
                selected: selected,
                onSelected: (_) => onCategoryChanged(c.$1),
              );
            }).toList(),
          ),
        ),
      ],
    );
  }
}

class _AppGrid extends ConsumerWidget {
  const _AppGrid({
    required this.entries,
    required this.installs,
    required this.onTap,
    required this.onSecondaryTap,
  });

  final List<AppCatalogEntry> entries;
  final List<Installation> installs;
  final ValueChanged<String> onTap;

  /// 右键菜单回调；entry + 鼠标 globalPosition。
  final void Function(AppCatalogEntry entry, Offset globalPosition) onSecondaryTap;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final creds = ref.watch(hubCredentialsProvider);
    if (entries.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(BiuTokens.space5),
          child: Text(
            AppLocalizations.of(context)!.appsEmpty,
            style: Theme.of(context).textTheme.bodyMedium,
          ),
        ),
      );
    }
    final installedSlugs = installs.map((i) => i.identifier).toSet();
    final installIdBySlug = {for (final i in installs) i.identifier: i.id};
    return LayoutBuilder(
      builder: (context, constraints) {
        // Responsive column count — matches the visual density of
        // SkillsPage tiles.
        final w = constraints.maxWidth;
        int cols = 5;
        if (w < 480) {
          cols = 2;
        } else if (w < 720) {
          cols = 3;
        } else if (w < 1024) {
          cols = 4;
        }
        return GridView.builder(
          padding: const EdgeInsets.only(bottom: BiuTokens.space5),
          gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: cols,
            crossAxisSpacing: BiuTokens.space3,
            mainAxisSpacing: BiuTokens.space3,
            childAspectRatio: 0.95,
          ),
          itemCount: entries.length,
          itemBuilder: (context, i) {
            final e = entries[i];
            final (iconUrl, iconHeaders) = resolveAppIcon(e.icon, creds);
            return AppTile(
              entry: e,
              installed: installedSlugs.contains(e.identifier),
              onTap: () => onTap(e.identifier),
              onSecondaryTapDown: (d) => onSecondaryTap(e, d.globalPosition),
              // 触屏等价物: 长按弹同一个 pin 菜单 (P1-11)。
              onLongPressStart: (d) => onSecondaryTap(e, d.globalPosition),
              dragInstallId: installIdBySlug[e.identifier],
              iconUrl: iconUrl,
              iconHeaders: iconHeaders,
            );
          },
        );
      },
    );
  }
}

/// 右键菜单：固定 / 取消固定 / 自定义侧边栏。
/// 未安装时菜单仍显示但 pin 项 disabled —— 引导用户先安装。
Future<void> _showContextMenu(
  BuildContext context,
  WidgetRef ref,
  AppCatalogEntry entry,
  List<Installation> installs,
  Offset globalPosition,
) async {
  final l10n = AppLocalizations.of(context)!;
  Installation? install;
  for (final i in installs) {
    if (i.identifier == entry.identifier) {
      install = i;
      break;
    }
  }
  final installed = install != null;
  final pinned = installed && isAppPinnedNow(ref, install.id);

  final selected = await showMenu<String>(
    context: context,
    position: popupPositionAt(context, globalPosition),
    items: [
      if (!pinned)
        PopupMenuItem<String>(
          value: 'pin',
          enabled: installed,
          child: ListTile(
            leading: const Icon(Icons.push_pin_outlined, size: 18),
            title: Text(l10n.sidebarPinAction),
            subtitle: installed ? null : Text(l10n.sidebarPinNeedsInstall),
            dense: true,
            contentPadding: EdgeInsets.zero,
          ),
        ),
      if (pinned)
        PopupMenuItem<String>(
          value: 'unpin',
          child: ListTile(
            leading: const Icon(Icons.push_pin, size: 18),
            title: Text(l10n.sidebarUnpinAction),
            dense: true,
            contentPadding: EdgeInsets.zero,
          ),
        ),
      const PopupMenuDivider(),
      PopupMenuItem<String>(
        value: 'customize',
        child: ListTile(
          leading: const Icon(Icons.tune, size: 18),
          title: Text(l10n.sidebarCustomizeAction),
          dense: true,
          contentPadding: EdgeInsets.zero,
        ),
      ),
    ],
  );

  if (selected == null || !context.mounted) return;
  switch (selected) {
    case 'pin':
    case 'unpin':
      if (install != null) {
        await _togglePinFromMenu(context, ref, install.id);
      }
    case 'customize':
      context.push('/apps/customize');
  }
}

Future<void> _togglePinFromMenu(
    BuildContext context, WidgetRef ref, String installId) async {
  final l10n = AppLocalizations.of(context)!;
  try {
    final nowPinned = await togglePinnedApp(ref, installId: installId);
    if (nowPinned == null || !context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
            nowPinned ? l10n.sidebarPinnedToast : l10n.sidebarUnpinnedToast),
        duration: const Duration(seconds: 2),
      ),
    );
  } on SidebarConflict {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(l10n.sidebarConflict)),
    );
  } catch (e) {
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(humanizeAppsError(context, e))),
    );
  }
}

class _ConfigurePlaceholder extends StatelessWidget {
  const _ConfigurePlaceholder({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Text(
          message,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ),
    );
  }
}

// ─── 开发中（dev-loaded apps）─────────────────────────────
//
// Shown above the regular catalog when the local CLI is running
// `biu app run --dev`. Each tile carries an "DEV" badge + the source
// directory tooltip so it's clearly distinct from a real installation.
// Tap → opens the dev manifest's first view via AppViewHost using
// install_id = "dev:<slug>"; the AppViewHost layer recognises the
// dev: prefix and skips the real installation lookup.

class _DevAppsSection extends StatelessWidget {
  const _DevAppsSection({required this.apps});
  final List<DevApp> apps;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Icon(Icons.code, size: 16, color: theme.colorScheme.primary),
            const SizedBox(width: 6),
            Text('开发中',
                style: theme.textTheme.titleSmall?.copyWith(
                  color: theme.colorScheme.primary,
                  fontWeight: FontWeight.w600,
                )),
            const SizedBox(width: BiuTokens.space2),
            Text('${apps.length} 个本地加载',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                )),
          ],
        ),
        const SizedBox(height: BiuTokens.space2),
        Wrap(
          spacing: BiuTokens.space3,
          runSpacing: BiuTokens.space3,
          children: apps.map((a) => _DevAppTile(app: a)).toList(),
        ),
      ],
    );
  }
}

class _DevAppTile extends StatelessWidget {
  const _DevAppTile({required this.app});
  final DevApp app;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final firstView = (app.manifest['views'] as List?)
        ?.whereType<Map<String, dynamic>>()
        .firstOrNull;
    return SizedBox(
      width: 240,
      child: Card(
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: firstView == null
              ? null
              : () => context.push(
                    '/apps/host/dev:${app.slug}/${firstView['id'] as String? ?? 'home'}',
                  ),
          child: Padding(
            padding: const EdgeInsets.all(BiuTokens.space3),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        app.title.isEmpty ? app.identifier : app.title,
                        style: theme.textTheme.titleSmall,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(
                        color: theme.colorScheme.primary.withValues(alpha: 0.12),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        app.mock ? 'DEV·MOCK' : 'DEV',
                        style: theme.textTheme.labelSmall?.copyWith(
                          color: theme.colorScheme.primary,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 0.5,
                        ),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  'v${app.version}  ·  ${app.identifier}',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: BiuTokens.space2),
                Tooltip(
                  message: app.sourcePath,
                  child: Text(
                    _shortenPath(app.sourcePath),
                    style: theme.textTheme.bodySmall?.copyWith(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  String _shortenPath(String p) {
    final segs = p.split('/');
    if (segs.length <= 3) return p;
    return '…/${segs.sublist(segs.length - 3).join('/')}';
  }
}
