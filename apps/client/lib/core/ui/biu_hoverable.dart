// BiuHoverable — hover lift + shadow swap 微动效封装。
//
// 对应 prototype:
//   .credit-chip {
//     transition: transform 200ms ease, box-shadow 200ms ease;
//   }
//   .credit-chip:hover {
//     transform: translateY(-1px);
//     box-shadow: var(--shadow-md);
//   }
//
// 设计:
//   * 仅 hasHover platform 启用 (macOS / Linux / Windows / Web 桌面),触屏端
//     pointer 没 hover 概念,自动 fallback 到无动效
//   * 用 builder 接口,调用方决定哪个属性跟着 hover 变 (transform / shadow /
//     border / scale 等都可以),不锁定具体视觉
//   * 动画走 BiuMotion.normal (200ms) + standard curve (easeOutCubic)
//
// 用法:
//   BiuHoverable(
//     onTap: ...,
//     liftPx: 1,            // hover 时上抬 1px (prototype credit-chip 标准)
//     liftPxStrong: 3,      // hover 时上抬 3px (prototype starter-card 标准)
//     builder: (ctx, hovered, pressed) => Container(...),
//   )

import 'package:flutter/material.dart';

import '../../app/theme/tokens.dart';

/// 检查当前 platform 是否有 hover 概念。
/// 触屏(iOS / Android)无 hover,桌面 + Web 有。
bool _platformHasHover(BuildContext context) {
  final p = Theme.of(context).platform;
  switch (p) {
    case TargetPlatform.iOS:
    case TargetPlatform.android:
      return false;
    case TargetPlatform.macOS:
    case TargetPlatform.linux:
    case TargetPlatform.windows:
    case TargetPlatform.fuchsia:
      return true;
  }
}

class BiuHoverable extends StatefulWidget {
  const BiuHoverable({
    super.key,
    required this.builder,
    this.onTap,
    this.onSecondaryTap,
    this.cursor = SystemMouseCursors.click,
    this.duration,
    this.curve,
  });

  /// (context, hovered, pressed) → Widget
  final Widget Function(BuildContext, bool hovered, bool pressed) builder;

  final VoidCallback? onTap;
  final VoidCallback? onSecondaryTap;
  final MouseCursor cursor;

  /// 动画时长,默认走 BiuMotion.normal (200ms)
  final Duration? duration;
  final Curve? curve;

  @override
  State<BiuHoverable> createState() => _BiuHoverableState();
}

class _BiuHoverableState extends State<BiuHoverable> {
  bool _hovered = false;
  bool _pressed = false;

  @override
  Widget build(BuildContext context) {
    final hasHover = _platformHasHover(context);
    // 触屏端永远 hovered=false,让动效完全消失
    final hovered = hasHover && _hovered;

    final child = widget.builder(context, hovered, _pressed);

    Widget wrapped = child;
    if (widget.onTap != null || widget.onSecondaryTap != null) {
      wrapped = GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        onSecondaryTap: widget.onSecondaryTap,
        onTapDown: (_) => setState(() => _pressed = true),
        onTapUp: (_) => setState(() => _pressed = false),
        onTapCancel: () => setState(() => _pressed = false),
        child: wrapped,
      );
    }

    if (!hasHover) return wrapped;

    return MouseRegion(
      cursor: widget.cursor,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: wrapped,
    );
  }
}

// ═════════════════════════════════════════════════════════════════════════
// 辅助:常见 hover 视觉模式封装(避免每个调用点都写 builder)
// ═════════════════════════════════════════════════════════════════════════

/// 标准 lift hover — 上抬 + 阴影加深。最常用形式。
///
/// 用法:
///   BiuLift(
///     liftPx: 1,
///     onTap: () {},
///     child: Container(...),  // 你的内容,decoration 不要加 shadow,这里管
///   )
class BiuLift extends StatelessWidget {
  const BiuLift({
    super.key,
    required this.child,
    this.onTap,
    this.liftPx = 1,
    this.borderRadius,
    this.restingShadow,
    this.hoverShadow,
  });

  final Widget child;
  final VoidCallback? onTap;

  /// hover 时上抬 px (prototype:credit-chip=1, starter-card=3)
  final double liftPx;

  /// 圆角 — 给 InkWell 的水波纹用
  final BorderRadius? borderRadius;

  /// 默认状态阴影 — 不传走 ShadowTokens.sm
  final List<BoxShadow>? restingShadow;

  /// hover 状态阴影 — 不传走 ShadowTokens.md
  final List<BoxShadow>? hoverShadow;

  @override
  Widget build(BuildContext context) {
    final brightness = Theme.of(context).brightness;
    final st = ShadowTokens.forBrightness(brightness);
    final resting = restingShadow ?? st.sm;
    final hover = hoverShadow ?? st.md;
    return BiuHoverable(
      onTap: onTap,
      builder: (ctx, hovered, pressed) {
        return AnimatedContainer(
          duration: MotionTokens.normal,
          curve: MotionTokens.standard,
          transform: Matrix4.translationValues(0, hovered ? -liftPx : 0, 0),
          decoration: BoxDecoration(
            borderRadius: borderRadius,
            boxShadow: hovered ? hover : resting,
          ),
          child: child,
        );
      },
    );
  }
}
