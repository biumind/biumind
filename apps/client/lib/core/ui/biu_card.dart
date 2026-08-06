// BiuCard — 通用卡片 widget。
//
// 对应 prototype:
//   .card {
//     background: var(--surf-0);
//     border: 1px solid var(--border-hairline);
//     box-shadow: var(--shadow-sm);
//     border-radius: var(--radius-lg);
//     transition: transform 200ms, box-shadow 200ms;
//   }
//   .card::before {
//     content: '';
//     position: absolute; inset: 0;
//     background: var(--card-grad);   /* 3% brand 微染 135deg */
//     pointer-events: none;
//   }
//   .card:hover {
//     transform: translateY(-1px);
//     box-shadow: var(--shadow-md);
//   }
//   .card.selected {
//     border-color: var(--brand);
//     border-width: 1.5px;
//     background: var(--brand-soft);
//   }
//
// 关键视觉特征:
//   1. 微染叠层 — 实色 surface 上叠 3% brand 渐变,有"温度"
//   2. 1px hairline 边框 — 极淡分隔(不是黑边)
//   3. shadow-sm 默认,hover swap 到 shadow-md + 上抬 1px
//   4. selected 1.5px brand border + brand-soft bg
//
// 用法:
//   BiuCard(
//     onTap: () {},
//     selected: false,
//     lift: 1,         // hover 上抬 px (chip=0, card=1, hero-card=3)
//     padding: ...,
//     child: ...,
//   )
//
// 设计原则:
//   * 默认带 hairline border + shadow-sm + cardGradTint
//   * 不带 hover lift 时(`lift: 0`),保留 padding + decoration 但去 hover 动效
//   * 内部用 BiuLift 走动效;非 hasHover platform 自动 fallback 到静态

import 'package:flutter/material.dart';

import '../../app/theme/effects.dart';
import '../../app/theme/extensions.dart';
import '../../app/theme/tokens.dart';
import 'biu_hoverable.dart';

class BiuCard extends StatelessWidget {
  const BiuCard({
    super.key,
    required this.child,
    this.onTap,
    this.onSecondaryTap,
    this.selected = false,
    this.lift = 1,
    this.padding,
    this.borderRadius,
    this.tintPercent = 0.03,
    this.disableTint = false,
    this.disableShadow = false,
  });

  final Widget child;
  final VoidCallback? onTap;
  final VoidCallback? onSecondaryTap;
  final bool selected;

  /// hover 上抬 px。0 = 无 hover 动效(static card)
  final double lift;

  final EdgeInsetsGeometry? padding;
  final BorderRadius? borderRadius;

  /// brand 微染百分比(prototype 3%)。0 = 不要微染叠层
  final double tintPercent;
  final bool disableTint;
  final bool disableShadow;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>(); // 可能为 null (旧 widget tests)
    final cs = theme.colorScheme;
    final brightness = theme.brightness;
    final st = ShadowTokens.forBrightness(brightness);
    final neutral = NeutralTokens.forBrightness(brightness);

    final radius = borderRadius ?? BorderRadius.circular(RadiusTokens.lg);
    final pad = padding ?? const EdgeInsets.all(SpacingTokens.s4);

    // Fallback chain:c (BiuColors) > Material ColorScheme > NeutralTokens 静态
    final brand = c?.brand ?? cs.primary;
    final brandSoft = c?.brandSoft ?? cs.primaryContainer;
    final surface0 = c?.surface0 ?? cs.surface;
    final borderHairline = c?.borderHairline ?? neutral.borderHairline;
    // prototype `.qa:hover { border-color: var(--border-soft) }` — hover 时
    // 边框从 hairline 切到稍深的 soft,跟 shadow 升级配合让 hover 反馈更立体。
    final borderSoft = c?.borderSoft ?? neutral.borderSoft;

    final bgColor = selected ? brandSoft : surface0;
    final borderWidth = selected ? 1.5 : 1.0;

    final restingShadow = disableShadow ? <BoxShadow>[] : st.sm;
    final hoverShadow = disableShadow ? <BoxShadow>[] : st.md;
    final selectedShadow = disableShadow ? <BoxShadow>[] : st.md;

    Widget content = Padding(padding: pad, child: child);

    // cardGradTint 用 foregroundDecoration 叠在 child 之上(透明渐变,
    // 只染色不阻断点击)。selected 状态下不叠 tint(brandSoft 已经够亮)。
    final showTint = !disableTint && !selected && tintPercent > 0;

    Widget buildContainer({
      required bool hovered,
      required List<BoxShadow> shadow,
      required double translateY,
    }) {
      // selected 永远 brand;非 selected 时 hover 切到 borderSoft,resting 用
      // hairline。跟 prototype `.qa:hover { border-color: border-soft }` 对齐。
      final borderColor = selected
          ? brand
          : (hovered ? borderSoft : borderHairline);
      return AnimatedContainer(
        duration: MotionTokens.normal,
        curve: MotionTokens.standard,
        transform: Matrix4.translationValues(0, translateY, 0),
        decoration: BoxDecoration(
          color: bgColor,
          borderRadius: radius,
          border: Border.all(color: borderColor, width: borderWidth),
          boxShadow: shadow,
        ),
        foregroundDecoration: showTint
            ? BoxDecoration(
                borderRadius: radius,
                gradient: cardGradTint(brand, percent: tintPercent),
              )
            : null,
        child: content,
      );
    }

    if (lift <= 0 && onTap == null && onSecondaryTap == null) {
      return buildContainer(
        hovered: false,
        shadow: selected ? selectedShadow : restingShadow,
        translateY: 0,
      );
    }

    return BiuHoverable(
      onTap: onTap,
      onSecondaryTap: onSecondaryTap,
      builder: (ctx, hovered, pressed) {
        final translateY = (lift > 0 && hovered) ? -lift : 0.0;
        final shadow = selected
            ? selectedShadow
            : (hovered ? hoverShadow : restingShadow);
        return buildContainer(
          hovered: hovered,
          shadow: shadow,
          translateY: translateY,
        );
      },
    );
  }
}
