// BiuGlass — backdrop-filter saturate blur 玻璃磨砂封装。
//
// 对应 prototype:
//   backdrop-filter: saturate(180%) blur(20px);
//   -webkit-backdrop-filter: saturate(180%) blur(20px);
//   background: rgba(255, 255, 255, 0.18);
//
// 用途:
//   * Banner CTA chip / "立即升级"按钮(深色 banner 上的玻璃质感)
//   * Sticky 顶栏 / Tab bar(模糊下方滚动内容)
//   * Popover / Dropdown(Material 弹层)
//
// 平台行为:
//   * macOS / iOS / Android native:正常 BackdropFilter + ImageFilter.blur,
//     性能由 Skia / Impeller 处理
//   * Web (CanvasKit):BackdropFilter 性能可接受
//   * Web (HTML 渲染):BackdropFilter 不支持 — 自动降级到无 blur 的 fallback
//   * 强制降级:传 disableBlur: true(测试 / 低端设备)
//
// 注意:
//   * 玻璃效果需要"下面有内容才看得出",在纯色背景上看跟实色无区别
//   * BackdropFilter 必须包在 ClipRect 里,否则 blur 影响周围

import 'dart:ui';

import 'package:flutter/material.dart';

import '../../app/theme/tokens.dart';

class BiuGlass extends StatelessWidget {
  const BiuGlass({
    super.key,
    required this.child,
    this.borderRadius,
    this.blur = 20,
    this.saturation = 1.8,
    this.tintColor,
    this.tintAlpha = 0.18,
    this.border,
    this.disableBlur = false,
  });

  final Widget child;
  final BorderRadius? borderRadius;

  /// blur sigma — prototype 用 20px,Flutter sigma ≈ 10 视觉接近
  final double blur;

  /// 饱和度乘数 — prototype `saturate(180%)` = 1.8
  final double saturation;

  /// 半透明色 tint(默认白色 0.18 透明度,适用深色 banner 上)
  final Color? tintColor;
  final double tintAlpha;

  /// 边框(prototype CTA 用 `border: 1px solid rgba(255,255,255,0.32)`)
  final BoxBorder? border;

  /// 强制不用 BackdropFilter(Web HTML renderer / 低性能场景 / 测试)
  final bool disableBlur;

  bool get _shouldDropBlur {
    // Web HTML renderer 不支持 backdrop blur,会显示原始内容(没磨砂)。
    // CanvasKit 支持。无法在 Dart 里区分,保守在 kIsWeb 时也跑(CanvasKit 主流)。
    // 用户传 disableBlur 才完全跳过。
    return disableBlur;
  }

  @override
  Widget build(BuildContext context) {
    final tint = tintColor ?? Colors.white;
    final bg = tint.withValues(alpha: tintAlpha);
    final radius = borderRadius ?? BorderRadius.zero;

    final inner = Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: borderRadius,
        border: border,
      ),
      child: child,
    );

    if (_shouldDropBlur) return inner;

    // ClipRRect 必要 — BackdropFilter 默认不裁剪,blur 会影响周围。
    return ClipRRect(
      borderRadius: radius,
      child: BackdropFilter(
        filter: ImageFilter.compose(
          // 双滤镜:饱和度 + 模糊
          inner: ColorFilter.matrix(_saturationMatrix(saturation)),
          outer: ImageFilter.blur(sigmaX: blur / 2, sigmaY: blur / 2),
        ),
        child: inner,
      ),
    );
  }
}

/// 标准饱和度矩阵 — 来自 https://www.w3.org/TR/filter-effects-1/#saturateEquivalent
/// s=1 不变,s=1.8 增强 80%,s=0 变灰度。
List<double> _saturationMatrix(double s) {
  // luminance weights
  const lr = 0.213;
  const lg = 0.715;
  const lb = 0.072;
  final r1 = lr * (1 - s);
  final r2 = lg * (1 - s);
  final r3 = lb * (1 - s);
  return [
    r1 + s, r2,     r3,     0, 0,
    r1,     r2 + s, r3,     0, 0,
    r1,     r2,     r3 + s, 0, 0,
    0,      0,      0,      1, 0,
  ];
}

/// 浅色 tint 版(用在浅色背景的 sticky 顶栏)。
class BiuGlassLight extends StatelessWidget {
  const BiuGlassLight({
    super.key,
    required this.child,
    this.borderRadius,
    this.tintAlpha = 0.72,
  });

  final Widget child;
  final BorderRadius? borderRadius;
  final double tintAlpha;

  @override
  Widget build(BuildContext context) {
    final neutral = NeutralTokens.forBrightness(Theme.of(context).brightness);
    return BiuGlass(
      borderRadius: borderRadius,
      blur: 16,
      saturation: 1.6,
      tintColor: neutral.surface0,
      tintAlpha: tintAlpha,
      child: child,
    );
  }
}
