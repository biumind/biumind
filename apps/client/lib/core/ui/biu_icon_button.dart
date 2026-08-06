// BiuIconButton — prototype `.icon-btn` 等价的小尺寸图标按钮。
//
// 对应 prototype CSS:
//   .icon-btn {
//     width: 28px; height: 28px;
//     display: grid; place-items: center;
//     border-radius: var(--radius-sm);
//     cursor: pointer;
//     color: var(--text-3);
//     transition: background 160ms ease, color 160ms ease;
//   }
//   .icon-btn:hover { background: var(--hover-bg); color: var(--text-1); }
//
// Material IconButton 默认 48x48 tap area + 较强的 hover splash,跟 prototype
// 紧凑型的 thread list head 不匹配。这里给一个 28x28 + 软 hover bg 的版本。
//
// 用法:
//   BiuIconButton(
//     icon: Icons.search,
//     tooltip: '跨会话搜索',
//     onTap: () => ...,
//   )

import 'package:flutter/material.dart';

import '../../app/theme/extensions.dart';
import 'biu_hoverable.dart';

class BiuIconButton extends StatelessWidget {
  const BiuIconButton({
    super.key,
    required this.icon,
    this.onTap,
    this.tooltip,
    this.size = 28,
    this.iconSize = 16,
    this.color,
  });

  final IconData icon;
  final VoidCallback? onTap;
  final String? tooltip;
  final double size;
  final double iconSize;

  /// 强制指定颜色 — 默认 hover 切换 text3 → text1。
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final hoverBg = c?.surface2 ?? theme.colorScheme.surfaceContainer;

    final btn = BiuHoverable(
      onTap: onTap,
      builder: (ctx, hovered, _) {
        final fg = color ??
            (hovered
                ? (c?.text1 ?? theme.colorScheme.onSurface)
                : (c?.text3 ?? theme.colorScheme.onSurfaceVariant));
        return AnimatedContainer(
          duration: const Duration(milliseconds: 160),
          width: size,
          height: size,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: hovered ? hoverBg.withValues(alpha: 0.8) : Colors.transparent,
            borderRadius: BorderRadius.circular(6),
          ),
          child: Icon(icon, size: iconSize, color: fg),
        );
      },
    );

    if (tooltip == null) return btn;
    return Tooltip(
      message: tooltip!,
      waitDuration: const Duration(milliseconds: 400),
      child: btn,
    );
  }
}
