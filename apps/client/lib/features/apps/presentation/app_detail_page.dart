// AppDetailPage — single-app drill-down.
//
// Shows full manifest fields the catalog tile didn't fit:
//   - Display name / version / author / category / kind
//   - Description (full)
//   - Permissions list (with risk highlighting)
//   - Bundled skills (chips)
//   - View routes declared
//   - Triggers declared (cron / webhook / inbox)
// Bottom: 安装 / 卸载 / 启用 toggle button row depending on
// whether the app is currently installed for the caller.
//
// All install + toggle calls go through appsClientProvider; the page
// invalidates installationsProvider after success so the catalog
// page's "已安装" badge re-renders.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/api/sidebar_client.dart';
import '../../../data/apps_providers.dart';
import '../../../data/pin_suggestion_repo.dart';
import '../../../data/sidebar_providers.dart';
import '../../../l10n/app_localizations.dart';
import '../../../services/auth_service.dart';
import '../../../shared/page_scaffold.dart';
import '../host/webview_panel.dart';
import 'app_icon_resolver.dart';
import 'apps_error.dart';
import 'install_dialog.dart';

/// 解析 App 的 home view id：优先取 manifest.views 里 id=='home' 的 view，
/// 否则取首个 view 的 id；manifest 没声明任何 view（backend-only kind apps，
/// 走 chat skill 路径，没有可宿主的 UI）则返回 null。
///
/// sidebar pinned 项「直接进应用本体」(`/apps/host/<installId>/<viewId>`)
/// 与 AppDetailPage 的「打开」按钮共用同一解析，避免两处口径漂移。
String? resolveAppHomeViewId(Map<String, dynamic> manifest) {
  final views = (manifest['views'] as List?)
          ?.whereType<Map<String, dynamic>>()
          .toList(growable: false) ??
      const <Map<String, dynamic>>[];
  if (views.isEmpty) return null;
  for (final v in views) {
    if (v['id'] == 'home') return 'home';
  }
  final firstId = views.first['id'];
  return firstId is String && firstId.isNotEmpty ? firstId : null;
}

/// 防御性判定 repo app（M1.14）：manifest 带 `repo_meta` 对象或
/// `tier == 'repo'`。老服务端没有这两个字段 → false，走原 view 逻辑。
bool _isRepoApp(Map<String, dynamic> manifest) {
  if (RepoMeta.tryParse(manifest['repo_meta']) != null) return true;
  return (manifest['tier'] as String?) == 'repo';
}

class AppDetailPage extends ConsumerWidget {
  const AppDetailPage({super.key, required this.identifier});
  final String identifier;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final manifestAsync = ref.watch(manifestProvider(identifier));
    final install = ref.watch(installationByIdentifierProvider(identifier));
    // l10n is referenced inside _Body / dialogs through
    // Localizations.of; keep the explicit lookup here for parity
    // with sibling pages.
    // ignore: unused_local_variable
    final _ = l10n;
    return PageScaffold(
      title: identifier,
      // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
      leading: const PhoneBackButton(),
      child: manifestAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: SelectableText('Error: $e')),
        data: (m) {
          final creds = ref.watch(hubCredentialsProvider);
          final iconRaw = (m['icon'] as String?) ?? '';
          final (iconUrl, iconHeaders) = resolveAppIcon(iconRaw, creds);
          // Repo App（M1.14）：manifest 带 repo_meta / tier=='repo' 时
          // "打开"跳进伪独立窗口而非 view host；平台无 runner（Windows
          // / 移动端）不给入口。
          final isRepo = _isRepoApp(m);
          final canOpenRepo = isRepo &&
              install != null &&
              ref.watch(platformCapsProvider).hasRepoAppRunner;
          return _Body(
            manifest: m,
            install: install,
            iconUrl: iconUrl,
            iconHeaders: iconHeaders,
            iconRaw: iconRaw,
            onInstall: () => _install(context, ref, m),
            onUninstall: () => _uninstall(context, ref, install),
            onToggle: (enabled) => _toggle(context, ref, install, enabled),
            onOpen: install == null
                ? null
                : canOpenRepo
                    ? () => context.push('/apps/repo-window/${install.id}')
                    : isRepo
                        ? null
                        : () => _open(context, install, m),
          );
        },
      ),
    );
  }

  /// 跳到 App 的 home view (manifest.views[0].id; fallback "home")。
  /// 仅当 manifest 声明了至少一个 view 时启用 —— backend kind apps 没有
  /// view, 走 chat skill 路径, 这里返回空让"打开"按钮不显示。
  void _open(BuildContext context, Installation install, Map<String, dynamic> manifest) {
    final viewId = _resolveHomeViewId(manifest);
    if (viewId == null) return;
    context.push('/apps/host/${install.id}/$viewId');
  }

  Future<void> _install(BuildContext context, WidgetRef ref, Map<String, dynamic> manifest) async {
    final perms = (manifest['permissions'] as List?)?.whereType<String>().toList()
        ?? const <String>[];
    final name = (manifest['title'] as String?) ?? (manifest['name'] as String? ?? identifier);
    final version = manifest['version'] as String? ?? '0.0.0';
    final iconRaw = (manifest['icon'] as String?) ?? '';
    final (iconUrl, iconHeaders) =
        resolveAppIcon(iconRaw, ref.read(hubCredentialsProvider));

    final choice = await InstallDialog.show(
      context,
      appName: name,
      version: version,
      permissions: perms,
      iconUrl: iconUrl,
      iconHeaders: iconHeaders,
      iconRaw: iconRaw,
    );
    if (choice == null) return;

    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      await client.install(
        identifier: identifier,
        scope: 'user',
        grantedPermissions: choice.grantedPermissions,
        token: token,
      );
      // 装上后只失效相关 key —— scope='user' 的列表 + catalog（"已安装"
      // 徽章依赖列表 derived 状态, 实际不需要拉 catalog；但这里保留以
      // 防服务端 catalog 受用户 install 影响（如 personal apps 可见性）。
      ref.invalidateInstallScope('user');
      ref.invalidate(appsCatalogProvider);
      if (!context.mounted) return;
      // 等 installationsProvider 失效 + 重新拉取后, 通过 identifier 反查
      // installId。给 1 个 frame 缓冲让 provider 拿到新行 —— pin
      // SnackBar 需要用 fresh.id, 跳 home view 也要 fresh.id。
      await Future<void>.delayed(const Duration(milliseconds: 50));
      if (!context.mounted) return;
      final fresh = ref.read(installationByIdentifierProvider(identifier));
      await _showInstalledSnack(
        context,
        ref,
        identifier: identifier,
        name: name,
        installId: fresh?.id,
      );
      // 装完后自动跳进 home view —— 解决"装完了但找不到入口"的痛点。
      // 前提: manifest 声明了至少一个 view (hybrid / view / webview kinds);
      // backend-only app 无 view, 留在原页让用户读 description。
      final viewId = _resolveHomeViewId(manifest);
      if (viewId != null && fresh != null && context.mounted) {
        context.push('/apps/host/${fresh.id}/$viewId');
      }
    } catch (e) {
      if (context.mounted) {
        _showError(context, e);
      }
    }
  }

  Future<void> _uninstall(BuildContext context, WidgetRef ref, Installation? install) async {
    if (install == null) return;
    final l10n = AppLocalizations.of(context)!;
    final ok = await showDialog<bool>(
      context: context,
      // dialogCtx(不是外层 page context):showDialog 默认 useRootNavigator
      // 把对话框 push 到根 Navigator,而 page context 解析到的是 ShellRoute
      // 子 Navigator。用 page context pop 会弹掉详情页本身(露出应用中心)
      // 而把对话框留在根 Navigator 上 → showDialog future 永不完成,卸载卡死。
      // 必须用对话框自己的 context 才能弹对话框这条路由。
      builder: (dialogCtx) => AlertDialog(
        title: Text(l10n.appsUninstallTitle),
        content: Text(l10n.appsUninstallConfirm(install.identifier)),
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
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      // M12: webview apps own a chunk of WKWebsiteDataStore / Android
      // CookieManager state. Wipe it before the install row goes away
      // so the user doesn't carry stale cookies if they reinstall the
      // same site later. Best-effort — failure logs but doesn't block
      // the uninstall.
      await _clearWebViewStorageIfApplicable(ref, install);

      await client.uninstall(installId: install.id, token: token);
      ref.invalidateInstall(install.id);
      ref.invalidateInstallScope('user');
      ref.invalidate(appsCatalogProvider);
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.appsUninstalledToast(install.identifier))),
        );
      }
    } catch (e) {
      if (context.mounted) _showError(context, e);
    }
  }

  Future<void> _toggle(BuildContext context, WidgetRef ref, Installation? install, bool enabled) async {
    if (install == null) return;
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      await client.toggle(installId: install.id, enabled: enabled, token: token);
      ref.invalidateInstall(install.id);
      ref.invalidateInstallScope('user');
    } catch (e) {
      if (context.mounted) _showError(context, e);
    }
  }

  /// 找出 manifest 的 "home" view id —— 用于安装后跳转 / "打开" 按钮:
  ///   1. 优先 id == 'home'
  ///   2. 否则 manifest.views[0].id
  ///   3. 都不存在 (backend-only app) → null
  static String? _resolveHomeViewId(Map<String, dynamic> manifest) =>
      resolveAppHomeViewId(manifest);

  void _showError(BuildContext context, Object e) {
    final scheme = Theme.of(context).colorScheme;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(humanizeAppsError(context, e)),
        backgroundColor: scheme.errorContainer,
      ),
    );
  }

  /// 装完成功 toast。如 PinSuggestionRepo 决定可以提示且 install 已落库,
  /// 在 SnackBar 上挂 "添加到侧边栏" action; show 同时立即写 dismiss 时间
  /// 戳——同一 identifier 7 天内不重复打扰 (设计 §10A.3)。
  Future<void> _showInstalledSnack(
    BuildContext context,
    WidgetRef ref, {
    required String identifier,
    required String name,
    required String? installId,
  }) async {
    final l10n = AppLocalizations.of(context)!;
    final pinRepo = ref.read(pinSuggestionRepoProvider);
    // 已 pin 不再询问 (e.g. default_pin=true 的 bundled app 装完已自动入栏);
    // 用 *Now 变体避免在 callback 上下文订阅 layout provider。
    final pinned = installId != null && isAppPinnedNow(ref, installId);
    final canOffer = installId != null && !pinned;
    final shouldOffer = canOffer && await pinRepo.shouldShow(identifier);
    if (shouldOffer) {
      // 看到提示就算"已表态" —— 让 SnackBar 自然消失也算 dismiss,
      // 否则同一 app 反复装/卸会一直弹。7 天后再次重装会再提示。
      await pinRepo.dismiss(identifier);
    }
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(l10n.appsInstalledToast(name)),
        duration: const Duration(seconds: 4),
        action: shouldOffer
            ? SnackBarAction(
                label: l10n.sidebarPinSuggestionAction,
                onPressed: () => _pinFromInstallToast(context, ref, installId),
              )
            : null,
      ),
    );
  }

  Future<void> _pinFromInstallToast(
      BuildContext context, WidgetRef ref, String installId) async {
    final l10n = AppLocalizations.of(context)!;
    try {
      final nowPinned = await togglePinnedApp(ref, installId: installId);
      if (nowPinned == null || !context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.sidebarPinnedToast),
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
}

class _Body extends StatelessWidget {
  const _Body({
    required this.manifest,
    required this.install,
    required this.iconUrl,
    required this.iconHeaders,
    required this.iconRaw,
    required this.onInstall,
    required this.onUninstall,
    required this.onToggle,
    required this.onOpen,
  });

  final Map<String, dynamic> manifest;
  final Installation? install;
  /// 来自 resolveAppIcon — 解析 manifest.icon 后拼好的图片 URL + auth
  /// header (cas: 路径需要 Bearer)。null = 没图 / emoji / 加载未就绪。
  final String? iconUrl;
  final Map<String, String>? iconHeaders;
  /// 原始 icon 字段, 用来 fallback 显示 emoji / 短文字 (URL 路径用上面
  /// 的 iconUrl, 其它情况渲 iconRaw)。
  final String iconRaw;
  final VoidCallback onInstall;
  final VoidCallback onUninstall;
  final ValueChanged<bool> onToggle;

  /// null 表示该 app 没有 view (backend-only) 或未安装 —— UI 不显示"打开"按钮。
  final VoidCallback? onOpen;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;

    final title = (manifest['title'] as String?) ?? (manifest['name'] as String? ?? '');
    final version = manifest['version'] as String? ?? '';
    final desc = manifest['description'] as String? ?? '';
    final author = manifest['author'] as String? ?? '';
    final permissions = (manifest['permissions'] as List?)?.whereType<String>().toList()
        ?? const <String>[];
    final views = (manifest['views'] as List?)?.whereType<Map<String, dynamic>>().toList()
        ?? const <Map<String, dynamic>>[];
    final triggers = (manifest['triggers'] as List?)?.whereType<Map<String, dynamic>>().toList()
        ?? const <Map<String, dynamic>>[];
    final skills = (manifest['skills'] as List?)?.whereType<Map<String, dynamic>>().toList()
        ?? const <Map<String, dynamic>>[];

    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _AppHeroIcon(
                title: title,
                iconUrl: iconUrl,
                iconHeaders: iconHeaders,
                iconRaw: iconRaw,
              ),
              const SizedBox(width: BiuTokens.space3),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(title, style: theme.textTheme.headlineSmall?.copyWith(
                        fontWeight: FontWeight.w700)),
                    if (version.isNotEmpty || author.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(top: 4),
                        child: Text(
                          [if (author.isNotEmpty) 'by $author', if (version.isNotEmpty) 'v$version'].join(' · '),
                          style: theme.textTheme.bodySmall?.copyWith(
                              color: theme.colorScheme.onSurfaceVariant),
                        ),
                      ),
                  ],
                ),
              ),
              const SizedBox(width: BiuTokens.space3),
              _ActionButtons(
                installed: install != null,
                enabled: install?.enabled ?? false,
                onInstall: onInstall,
                onUninstall: onUninstall,
                onToggle: onToggle,
                onOpen: onOpen,
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space3),
          if (desc.isNotEmpty) Text(desc),
          // Repo App（M1.14）：GitHub 信息区 —— 有 repo_meta 才显示。
          if (RepoMeta.tryParse(manifest['repo_meta'])
              case final repoMeta?) ...[
            const SizedBox(height: BiuTokens.space5),
            _Section(
              title: 'GitHub',
              child: _RepoInfoList(repoMeta: repoMeta, version: version),
            ),
          ],
          const SizedBox(height: BiuTokens.space5),
          _Section(title: l10n.appsSectionPermissions, child: _PermsList(permissions)),
          if (views.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space5),
            _Section(title: l10n.appsSectionViews, child: _ViewsList(views)),
          ],
          if (triggers.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space5),
            _Section(title: l10n.appsSectionTriggers, child: _TriggersList(triggers)),
          ],
          if (skills.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space5),
            _Section(title: l10n.appsSectionSkills, child: _SkillsList(skills)),
          ],
        ],
      ),
    );
  }
}

/// 详情页头部 56px 大图标 — 跟 catalog tile 一致的 hero, 让用户从入口
/// 到细节都看到同一个 manifest.icon。
class _AppHeroIcon extends StatelessWidget {
  const _AppHeroIcon({
    required this.title,
    required this.iconUrl,
    required this.iconHeaders,
    required this.iconRaw,
  });
  final String title;
  final String? iconUrl;
  final Map<String, String>? iconHeaders;
  final String iconRaw;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final letter = title.isEmpty ? '?' : title.characters.first.toUpperCase();
    Widget letterFallback() => Text(
          letter,
          style: TextStyle(
            fontSize: 24,
            fontWeight: FontWeight.w700,
            color: scheme.onSurfaceVariant,
          ),
        );
    Widget child;
    if (iconUrl != null) {
      child = Image.network(
        iconUrl!,
        width: 44,
        height: 44,
        fit: BoxFit.cover,
        headers: iconHeaders,
        errorBuilder: (_, _, _) => letterFallback(),
      );
    } else if (iconRaw.isNotEmpty &&
        !iconRaw.startsWith('http') &&
        !iconRaw.startsWith('cas:')) {
      child = Text(iconRaw, style: const TextStyle(fontSize: 32));
    } else {
      child = letterFallback();
    }
    return Container(
      width: 56,
      height: 56,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        child: child,
      ),
    );
  }
}

class _ActionButtons extends StatelessWidget {
  const _ActionButtons({
    required this.installed,
    required this.enabled,
    required this.onInstall,
    required this.onUninstall,
    required this.onToggle,
    required this.onOpen,
  });
  final bool installed;
  final bool enabled;
  final VoidCallback onInstall;
  final VoidCallback onUninstall;
  final ValueChanged<bool> onToggle;

  /// null = 该 app 无 view (backend-only) 或未安装 —— 不渲染 Open 按钮。
  final VoidCallback? onOpen;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    if (!installed) {
      return FilledButton.icon(
        onPressed: onInstall,
        icon: const Icon(Icons.download_outlined, size: 18),
        label: Text(l10n.appsInstall),
      );
    }
    return Wrap(spacing: BiuTokens.space2, crossAxisAlignment: WrapCrossAlignment.center, children: [
      // "打开" 是装完后的最高频操作 —— 放最左做主按钮; 仅当 app 有
      // view 时显示 (backend-only app 没 onOpen)。
      if (onOpen != null)
        FilledButton.icon(
          onPressed: enabled ? onOpen : null,
          icon: const Icon(Icons.open_in_new, size: 18),
          label: Text(l10n.appsOpen),
        ),
      Switch(value: enabled, onChanged: onToggle),
      FilledButton.tonal(
        onPressed: onUninstall,
        child: Text(l10n.appsUninstall),
      ),
    ]);
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});
  final String title;
  final Widget child;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.titleSmall?.copyWith(
            fontWeight: FontWeight.w600)),
        const SizedBox(height: BiuTokens.space2),
        child,
      ],
    );
  }
}

/// Repo App 的 GitHub 信息区（M1.14）：stars / license / 当前版本 /
/// 最新版本 / 仓库地址。所有字段防御性渲染（空则跳过该行）。
class _RepoInfoList extends StatelessWidget {
  const _RepoInfoList({required this.repoMeta, required this.version});

  final RepoMeta repoMeta;
  final String version;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    Widget row(String label, String value) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 72,
                child: Text(label,
                    style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant)),
              ),
              Expanded(
                child: SelectableText(value,
                    style: theme.textTheme.bodySmall),
              ),
            ],
          ),
        );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (repoMeta.url.isNotEmpty) row('仓库', repoMeta.url),
        if (repoMeta.stars > 0) row('Stars', '★ ${repoMeta.stars}'),
        if (repoMeta.license.isNotEmpty) row('许可证', repoMeta.license),
        if (version.isNotEmpty) row('当前版本', 'v$version'),
        if (repoMeta.latestRef.isNotEmpty) row('最新版本', repoMeta.latestRef),
      ],
    );
  }
}

class _PermsList extends StatelessWidget {
  const _PermsList(this.perms);
  final List<String> perms;
  @override
  Widget build(BuildContext context) {
    if (perms.isEmpty) {
      return Text(AppLocalizations.of(context)!.appsNoPermissionRequested,
          style: Theme.of(context).textTheme.bodySmall);
    }
    final scheme = Theme.of(context).colorScheme;
    return Wrap(
      spacing: BiuTokens.space2,
      runSpacing: BiuTokens.space2,
      children: perms.map((p) {
        final risky = p.startsWith('net.outbound')
            || p == 'sandbox.exec'
            || p.startsWith('secrets.read');
        return Chip(
          label: Text(p, style: const TextStyle(fontFamily: 'monospace')),
          backgroundColor: risky ? scheme.errorContainer : scheme.surfaceContainerHigh,
          labelStyle: risky ? TextStyle(color: scheme.onErrorContainer) : null,
        );
      }).toList(),
    );
  }
}

class _ViewsList extends StatelessWidget {
  const _ViewsList(this.views);
  final List<Map<String, dynamic>> views;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: views.map((v) {
        final id = v['id'] as String? ?? '';
        final layout = v['layout'] as String? ?? '';
        final route = v['route'] as String? ?? '';
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Text('· $id ($layout) — $route',
              style: Theme.of(context).textTheme.bodySmall),
        );
      }).toList(),
    );
  }
}

class _TriggersList extends StatelessWidget {
  const _TriggersList(this.triggers);
  final List<Map<String, dynamic>> triggers;
  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: triggers.map((t) {
        final kind = t['kind'] as String? ?? '';
        final name = t['name'] as String? ?? '';
        final action = t['action'] as String? ?? '';
        final detail = (t['expr'] as String?) ?? (t['path'] as String?) ?? (t['pattern'] as String?) ?? '';
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Text('· [$kind] $name → $action${detail.isEmpty ? '' : ' ($detail)'}',
              style: Theme.of(context).textTheme.bodySmall),
        );
      }).toList(),
    );
  }
}

/// Wipes the webview cookie/storage scoped to the install's URL. Only
/// touches storage when the manifest declares kind='webview'. Failures
/// are logged via debugPrint and swallowed — losing the wipe is less
/// bad than blocking the uninstall.
Future<void> _clearWebViewStorageIfApplicable(WidgetRef ref, Installation install) async {
  try {
    final manifest =
        await ref.read(manifestProvider(install.identifier).future);
    if (manifest['kind'] != 'webview') return;
    final views = manifest['views'] as List?;
    final firstUrl = views
        ?.whereType<Map>()
        .map((v) => v['url'] as String?)
        .firstWhere((u) => u != null && u.isNotEmpty, orElse: () => null);
    if (firstUrl == null) return;
    final origin = Uri.parse(firstUrl);
    await WebViewPanel.clearForOrigin(
      origin,
      caps: ref.read(platformCapsProvider),
    );
  } catch (e) {
    debugPrint('webview storage cleanup skipped: $e');
  }
}

class _SkillsList extends StatelessWidget {
  const _SkillsList(this.skills);
  final List<Map<String, dynamic>> skills;
  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: BiuTokens.space2,
      runSpacing: BiuTokens.space2,
      children: skills.map((s) {
        return Chip(label: Text(s['identifier'] as String? ?? '?'));
      }).toList(),
    );
  }
}
