// BiuChip — pill / chip 通用组件。
//
// 对应 prototype 的三态:
//   .chip {                               // 默认
//     background: var(--surf-2);
//     border: 1px solid var(--border-hairline);
//     color: var(--text-2);
//   }
//   .chip.brand {                         // 强调态(技能 chips)
//     background: var(--brand-soft);
//     color: var(--brand);
//     border-color: transparent;
//   }
//   .chip.active {                        // 选中态(filled,模型选中)
//     background: var(--brand);
//     color: var(--brand-fg);             // 通常 white
//     border-color: transparent;
//   }
//
// Flutter 三参数映射:
//   BiuChip()                   → 默认 surface-2
//   BiuChip(brand: true)        → brand-soft + brand 文字(无边框)
//   BiuChip(active: true)       → brand 实色 + 白字(filled)
//   BiuChip(selected: true)     → 旧版"边框选中"状态(legacy 兼容,不推荐
//                                  新代码用,优先用 active)
//
// 跟 BiuCard 区别:
//   * BiuChip:pill 形状(圆角 999),小尺寸(padding 4×10),hover lift 1px
//   * BiuCard:大尺寸卡片(圆角 lg=14),hover lift 1-3px
//
// 用法:
//   BiuChip(
//     onTap: () {},
//     active: true,                       // 模型 chip 当前选中
//     leading: Icon(Icons.check),
//     label: Text('glm-5.1'),
//   )

import 'package:flutter/material.dart';

import '../../app/theme/extensions.dart';
import '../../app/theme/tokens.dart';
import 'biu_hoverable.dart';

class BiuChip extends StatelessWidget {
  const BiuChip({
    super.key,
    this.onTap,
    this.label,
    this.leading,
    this.trailing,
    this.selected = false,
    this.brand = false,
    this.active = false,
    this.lift = 1,
    this.padding = const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
    this.foregroundColor,
    this.backgroundColor,
    this.disableBorder = false,
  });

  final VoidCallback? onTap;
  final Widget? label;
  final Widget? leading;
  final Widget? trailing;

  /// legacy "边框选中" 状态:1.5px brand border + brand-soft bg + brand 文字。
  /// 新代码优先考虑 [active] (filled) — 视觉对比更强,跟 prototype 一致。
  final bool selected;

  /// 强调态(技能 chips):brand-soft 背景 + brand 文字,无边框。
  final bool brand;

  /// 选中实色态(filled, 模型 chip):brand 背景 + 白字,无边框。
  final bool active;

  final double lift;
  final EdgeInsetsGeometry padding;

  /// 强制覆盖前景(文字 / icon)色,默认走 selected/brand/active 推导
  final Color? foregroundColor;

  /// 强制覆盖背景色,默认走 selected/brand/active 推导
  final Color? backgroundColor;

  /// 强制无边框 — prototype 中 `.stat`(stat chip)是 surf-2 + 无边框,
  /// 默认 BiuChip 会带 hairline border 偏老风格,显式打开这个让 chip 跟 stat
  /// 视觉一致。brand/active 已经隐式无边框,本 flag 只对默认/selected 生效。
  final bool disableBorder;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final cs = theme.colorScheme;
    final brightness = theme.brightness;
    final neutral = NeutralTokens.forBrightness(brightness);
    final st = ShadowTokens.forBrightness(brightness);

    final brand = c?.brand ?? cs.primary;
    final brandSoft = c?.brandSoft ?? cs.primaryContainer;
    final surface2 = c?.surface2 ?? neutral.surface2;
    final surface3 = c?.surface3 ?? neutral.surface3;
    final borderHairline = c?.borderHairline ?? neutral.borderHairline;
    final text1 = c?.text1 ?? cs.onSurface;

    // 状态优先级:active(filled)> brand(soft)> selected(legacy 边框)> 默认
    final Color fg;
    final Color bg;
    Color borderColor;
    double borderWidth;
    if (active) {
      fg = foregroundColor ?? Colors.white;
      bg = backgroundColor ?? brand;
      borderColor = Colors.transparent;
      borderWidth = 0;
    } else if (this.brand) {
      fg = foregroundColor ?? brand;
      bg = backgroundColor ?? brandSoft;
      borderColor = Colors.transparent;
      borderWidth = 0;
    } else if (selected) {
      fg = foregroundColor ?? brand;
      bg = backgroundColor ?? brandSoft;
      borderColor = brand;
      borderWidth = 1.5;
    } else {
      fg = foregroundColor ?? text1;
      bg = backgroundColor ?? surface2;
      borderColor = borderHairline;
      borderWidth = 1;
    }
    if (disableBorder) {
      borderColor = Colors.transparent;
      borderWidth = 0;
    }
    final radius = BorderRadius.circular(999);

    Widget content = DefaultTextStyle.merge(
      style: TextStyle(color: fg, fontSize: 12, fontWeight: FontWeight.w500),
      child: IconTheme.merge(
        data: IconThemeData(color: fg, size: 14),
        child: Padding(
          padding: padding,
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (leading != null) ...[leading!, const SizedBox(width: 4)],
              if (label != null) Flexible(child: label!),
              if (trailing != null) ...[const SizedBox(width: 4), trailing!],
            ],
          ),
        ),
      ),
    );

    // 默认/selected 状态下 hover 切 bg 到 surf-3 — 跟 prototype
    // `.chip:hover { background: surf-3; color: text-1 }` 一致;active/brand
    // 已是高对比 filled,hover 不切 bg 只升 shadow + translateY 即可。
    final isPlainOrSelected = !active && !this.brand;
    Widget buildContainer({
      required List<BoxShadow> shadow,
      required double translateY,
      required Color bgColor,
    }) {
      return AnimatedContainer(
        duration: const Duration(milliseconds: 160),
        curve: MotionTokens.standard,
        transform: Matrix4.translationValues(0, translateY, 0),
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: radius,
          border: borderWidth > 0
              ? Border.all(color: borderColor, width: borderWidth)
              : null,
          boxShadow: shadow,
        ),
        child: content,
      );
    }

    if (lift <= 0 && onTap == null) {
      return buildContainer(shadow: st.sm, translateY: 0, bgColor: bg);
    }

    return BiuHoverable(
      onTap: onTap,
      builder: (ctx, hovered, pressed) {
        final shadow = hovered ? st.md : st.sm;
        final translateY = (lift > 0 && hovered) ? -lift : 0.0;
        final bgColor = (hovered && isPlainOrSelected && backgroundColor == null)
            ? surface3
            : bg;
        return buildContainer(
          shadow: shadow,
          translateY: translateY,
          bgColor: bgColor,
        );
      },
    );
  }
}
