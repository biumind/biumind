// BiuTile — 高密度列表行 widget(thread tile / nav item / 列表条目)。
//
// 跟 BiuCard 区别:
//   * BiuCard:卡片场景,有阴影 + cardGradTint + hover lift + 1.5px border 选中
//   * BiuTile:列表行场景,无阴影 + 无 tint + hover bg 变 surface2 + 选中
//                3px brand 左竖条 + brandSoft bg
//
// 对应 prototype:
//   .tile {
//     padding: var(--pad-tile-y) var(--pad-tile-x);
//     transition: background 160ms ease;
//     border-left: 3px solid transparent;
//   }
//   .tile:hover { background: var(--surf-2); }
//   .tile.selected {
//     background: var(--brand-soft);
//     border-left-color: var(--brand);
//   }
//
// 用法:
//   BiuTile(
//     onTap: () {},
//     selected: false,
//     padding: EdgeInsets.symmetric(horizontal: m.tilePadH, vertical: m.tilePadV),
//     child: Row(...),
//   )

import 'package:flutter/material.dart';

import '../../app/theme/extensions.dart';
import '../../app/theme/tokens.dart';
import 'biu_hoverable.dart';

class BiuTile extends StatelessWidget {
  const BiuTile({
    super.key,
    required this.child,
    this.onTap,
    this.onSecondaryTap,
    this.selected = false,
    this.padding,
    this.borderRadius,
    this.indicatorWidth = 3,
  });

  final Widget child;
  final VoidCallback? onTap;
  final VoidCallback? onSecondaryTap;
  final bool selected;

  final EdgeInsetsGeometry? padding;
  final BorderRadius? borderRadius;

  /// 选中态左侧竖条宽度(默认 3px,prototype 标准)
  final double indicatorWidth;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final cs = theme.colorScheme;
    final neutral = NeutralTokens.forBrightness(theme.brightness);
    final brand = c?.brand ?? cs.primary;
    final brandSoft = c?.brandSoft ?? cs.primaryContainer;
    final hoverBg = c?.surface2 ?? neutral.surface2;

    return BiuHoverable(
      onTap: onTap,
      onSecondaryTap: onSecondaryTap,
      builder: (ctx, hovered, pressed) {
        final bg = selected
            ? brandSoft
            : (hovered ? hoverBg : Colors.transparent);
        // hover bg 改用普通 Container 即时切换 — AnimatedContainer 160ms
        // 在快速划过列表时多 tile 同时淡出会留残影。selected 状态本组件
        // 仅 list selection 切换(低频),即时切换也可以接受。
        return Container(
          decoration: BoxDecoration(
            color: bg,
            borderRadius: borderRadius,
            border: Border(
              left: BorderSide(
                color: selected ? brand : Colors.transparent,
                width: indicatorWidth,
              ),
            ),
          ),
          padding: padding,
          child: child,
        );
      },
    );
  }
}
