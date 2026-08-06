// AppTile — single tile in the App Center grid.
//
// Visual: rounded card with icon (or first-letter fallback), display
// name, description (clamped 2 lines), small "已安装" badge when
// applicable. Single tap navigates to detail.

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import '../../../core/ui/biu_card.dart';
import '../../../data/api/apps_client.dart';
import '../../../l10n/app_localizations.dart';

class AppTile extends StatelessWidget {
  const AppTile({
    super.key,
    required this.entry,
    required this.installed,
    required this.onTap,
    this.onSecondaryTapDown,
    this.onLongPressStart,
    this.dragInstallId,
    this.iconUrl,
    this.iconHeaders,
  });

  final AppCatalogEntry entry;
  final bool installed;
  final VoidCallback onTap;

  /// 鼠标右键 / 触控板双指点击。坐标是 globalPosition，用来定位
  /// PopupMenu 的弹出位置。仅 installed=true 的 tile 给出该回调有意义
  /// (设计 §10A.3 右键 App tile 快捷菜单)。
  final void Function(TapDownDetails)? onSecondaryTapDown;

  /// 长按 (触屏的右键等价物, P1-11 方案 §4.2)。坐标同样取 globalPosition,
  /// 调用方弹与 onSecondaryTapDown 相同的菜单。
  final void Function(LongPressStartDetails)? onLongPressStart;

  /// 非空时 tile 可拖到 sidebar 触发固定 (设计 §10A.3 "直接拖拽")。
  /// 仅 installed 的 tile 调用方传 install_id; 未安装的 tile 传 null
  /// 让 tile 退回普通点击行为 (sidebar 装的是 install_id 引用, 没装
  /// 拖了也无意义)。
  final String? dragInstallId;

  /// 预解析的图标 URL — 由调用方根据 entry.icon (`cas:<sha>` / http
  /// URL) + creds 拼出。非空时走 Image.network 真渲染; 否则按既有
  /// emoji / 首字母 fallback。AppTile 自己保持 stateless 不 watch
  /// providers, 让 widget test 无需 ProviderScope。
  final String? iconUrl;
  final Map<String, String>? iconHeaders;

  @override
  Widget build(BuildContext context) {
    final tile = _buildTile(context, dragging: false);
    // 触屏无 hover / 拖拽与滚动冲突: 不挂 Draggable, 固定走长按菜单
    // (P1-11)。桌面 / Web 保留拖拽固定。
    if (dragInstallId == null || !platformHasHover(context)) return tile;
    return Draggable<String>(
      data: dragInstallId!,
      dragAnchorStrategy: pointerDragAnchorStrategy,
      feedback: _DragFeedback(entry: entry),
      childWhenDragging: Opacity(opacity: 0.4, child: tile),
      child: tile,
    );
  }

  Widget _buildTile(BuildContext context, {required bool dragging}) {
    final theme = Theme.of(context);
    final card = BiuCard(
      onTap: onTap,
      lift: 2,
      padding: const EdgeInsets.all(BiuTokens.space3),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _IconAvatar(
                name: entry.name,
                icon: entry.icon,
                imageUrl: iconUrl,
                imageHeaders: iconHeaders,
              ),
              const SizedBox(width: BiuTokens.space3),
              Expanded(
                child: Text(
                  entry.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: theme.textTheme.titleSmall?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (installed)
                _InstalledBadge(label: AppLocalizations.of(context)!.appsInstalled),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Expanded(
            child: Text(
              entry.description,
              maxLines: 3,
              overflow: TextOverflow.ellipsis,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            entry.identifier,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: theme.textTheme.labelSmall?.copyWith(
              color: theme.colorScheme.outline,
            ),
          ),
        ],
      ),
    );
    if (onSecondaryTapDown == null && onLongPressStart == null) return card;
    // BiuHoverable 内部 GestureDetector 仅声明 primary tap,secondary tap /
    // long press 在 gesture arena 不冲突 — 外层 GestureDetector 透传
    // onSecondaryTapDown / onLongPressStart (用 details 拿坐标弹菜单)。
    // HitTestBehavior.translucent 保证命中区跟 child 一致,但不影响内部
    // onTap 收事件。
    return GestureDetector(
      behavior: HitTestBehavior.translucent,
      onSecondaryTapDown: onSecondaryTapDown,
      onLongPressStart: onLongPressStart,
      child: card,
    );
  }
}

/// 拖动时跟随鼠标的小卡片, icon + 名字, 比 tile 简洁。
class _DragFeedback extends StatelessWidget {
  const _DragFeedback({required this.entry});
  final AppCatalogEntry entry;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: Colors.transparent,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: theme.colorScheme.primaryContainer.withValues(alpha: 0.95),
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withValues(alpha: 0.12),
              blurRadius: 8,
              offset: const Offset(0, 2),
            ),
          ],
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.widgets_outlined,
                size: 16, color: theme.colorScheme.onPrimaryContainer),
            const SizedBox(width: 8),
            Text(
              entry.name.isEmpty ? entry.identifier : entry.name,
              style: theme.textTheme.labelLarge?.copyWith(
                color: theme.colorScheme.onPrimaryContainer,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _IconAvatar extends StatelessWidget {
  const _IconAvatar({
    required this.name,
    required this.icon,
    this.imageUrl,
    this.imageHeaders,
  });
  final String name;
  final String icon;
  /// 调用方解析后的图标 URL (cas:/https URL → 实际下载地址) + 鉴权
  /// header。null 时按 emoji / letter fallback 渲染。
  final String? imageUrl;
  final Map<String, String>? imageHeaders;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final letter = name.isEmpty ? '?' : name.characters.first.toUpperCase();
    Widget letterFallback() => Text(
          letter,
          style: TextStyle(
            fontSize: 16,
            fontWeight: FontWeight.w700,
            color: scheme.onSurfaceVariant,
          ),
        );

    Widget child;
    if (imageUrl != null) {
      // 调用方解析过 cas:/http → 真渲染。NetworkImage 自带 disk cache,
      // server (brain by-sha) 设了 Cache-Control immutable, 第二次秒出。
      child = Image.network(
        imageUrl!,
        width: 28,
        height: 28,
        fit: BoxFit.cover,
        headers: imageHeaders,
        errorBuilder: (_, _, _) => letterFallback(),
        // loadingBuilder 不接管 — 用默认透明加载, frame 完成后 fade in;
        // 配合 errorBuilder 的 letterFallback 在 widget test (HTTP 拦
        // 截立即 error) 也能拿到 letter 文本节点。
      );
    } else if (icon.isNotEmpty && !icon.startsWith('http') && !icon.startsWith('cas:')) {
      // emoji
      child = Text(icon, style: const TextStyle(fontSize: 22));
    } else {
      // 空 / cas:/http 但调用方没给 imageUrl (例如缺 creds) → letter
      child = letterFallback();
    }
    return Container(
      width: 36,
      height: 36,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: child,
      ),
    );
  }
}

class _InstalledBadge extends StatelessWidget {
  const _InstalledBadge({required this.label});
  final String label;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: scheme.primaryContainer,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
      ),
      child: Text(
        label,
        style: Theme.of(context).textTheme.labelSmall?.copyWith(
          color: scheme.onPrimaryContainer,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
