// BiuMind 品牌 mark — CustomPainter 渲染。
// Path 数据 (biu_paths.dart) 与 web/site 完全同源。
// 5 段 fill path：头 + 手 (ink 黑) + 3 条电路 (purple 紫)。
// 每段透明度可独立控制，方便做进场动画 (splash 揭开效果)。

import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'package:path_drawing/path_drawing.dart';

import '../../app/theme/brand.dart';
import 'biu_paths.dart';

/// BiuMind logo 图标（仅 mark，无文字）。
///
/// 默认 ink 色取自 [DefaultTextStyle] 的 color，circuit 走 [BiuMarkColors.defaultCircuit]
/// （= 品牌紫 BiuBrand.primary）。需要单色样式（例如 dark mode 反白）传 [inkColor] / [circuitColor]
/// 显式覆盖。
///
/// [headOpacity] / [circuitOpacity] / [handOpacity] 给进场动画用 (0..1)。
class BiuMark extends StatelessWidget {
  const BiuMark({
    super.key,
    this.size = 32,
    this.inkColor,
    this.circuitColor,
    this.headOpacity = 1.0,
    this.circuitOpacity = 1.0,
    this.handOpacity = 1.0,
    this.semanticsLabel = 'BiuMind',
  });

  /// 边长（width = height）。Mark 自身是 viewBox 932×1554 (≈ 0.6 : 1)，
  /// 这个 size 是外部容器边长，mark 会居中 fit 进去。
  final double size;

  /// 头 + 手颜色，null = 取 DefaultTextStyle.color，再 fallback 到黑。
  final Color? inkColor;

  /// 电路颜色，null = 品牌紫。
  final Color? circuitColor;

  /// 头部透明度（0..1），默认 1。进场动画用。
  final double headOpacity;

  /// 电路（含 3 条线 + 圆点）整体透明度，默认 1。
  final double circuitOpacity;

  /// 手部透明度，默认 1。
  final double handOpacity;

  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) {
    final ink = inkColor ??
        DefaultTextStyle.of(context).style.color ??
        BiuMarkColors.defaultInk;
    final circuit = circuitColor ?? BiuMarkColors.defaultCircuit;

    final mark = SizedBox.square(
      dimension: size,
      child: CustomPaint(
        painter: _BiuMarkPainter(
          inkColor: ink,
          circuitColor: circuit,
          headOpacity: headOpacity.clamp(0.0, 1.0),
          circuitOpacity: circuitOpacity.clamp(0.0, 1.0),
          handOpacity: handOpacity.clamp(0.0, 1.0),
        ),
        isComplex: true,
        willChange: headOpacity != 1.0 || circuitOpacity != 1.0 || handOpacity != 1.0,
      ),
    );
    return semanticsLabel == null
        ? mark
        : Semantics(label: semanticsLabel, image: true, child: mark);
  }
}

class BiuMarkColors {
  /// 电路色 — 走永久品牌紫(跨色板恒定,因为 mark 出现在 splash / 登录页等
  /// 主题尚未加载或希望"永远是 BiuMind 紫"的场合)。
  static const Color defaultCircuit = BiuBrand.primary;

  /// ink 色 fallback — DefaultTextStyle.color 也无值时兜底。zinc-900 近似。
  static const Color defaultInk = Color(0xFF18181B); // theme-ignore: legacy 静态 ink fallback
}

// ─────────────────────────────────────────────────────
// Painter
// ─────────────────────────────────────────────────────

/// path_drawing 解析 SVG 字符串是有开销的，5 段 path 共 ~25K 字符。
/// 全 app 共用一份，所以解析一次后缓存到 module-level static。
class _BiuPaths {
  static final Path head = parseSvgPathData(biuHeadPath);
  static final Path hand = parseSvgPathData(biuHandPath);
  static final Path circuitL = parseSvgPathData(biuCircuitLeftPath);
  static final Path circuitM = parseSvgPathData(biuCircuitMiddlePath);
  static final Path circuitR = parseSvgPathData(biuCircuitRightPath);
}

class _BiuMarkPainter extends CustomPainter {
  _BiuMarkPainter({
    required this.inkColor,
    required this.circuitColor,
    required this.headOpacity,
    required this.circuitOpacity,
    required this.handOpacity,
  });

  final Color inkColor;
  final Color circuitColor;
  final double headOpacity;
  final double circuitOpacity;
  final double handOpacity;

  Paint _paint(Color base, double opacity) => Paint()
    ..color = base.withValues(alpha: base.a * opacity)
    ..style = PaintingStyle.fill
    ..isAntiAlias = true;

  @override
  void paint(Canvas canvas, Size size) {
    // viewBox fit：保持比例居中。
    final fit = math.min(size.width / biuViewBoxWidth, size.height / biuViewBoxHeight);
    final dx = (size.width - biuViewBoxWidth * fit) / 2;
    final dy = (size.height - biuViewBoxHeight * fit) / 2;

    canvas.save();
    canvas.translate(dx, dy);
    canvas.scale(fit);
    // potrace 输出的内部 transform：translate(-594.228457, 1763.5) scale(0.1, -0.1)
    // SVG transform 链从右往左作用 → Canvas 写出顺序与之相同。
    canvas.translate(biuTxTranslateX, biuTxTranslateY);
    canvas.scale(biuTxScaleX, biuTxScaleY);

    if (headOpacity > 0) canvas.drawPath(_BiuPaths.head, _paint(inkColor, headOpacity));
    if (circuitOpacity > 0) {
      final p = _paint(circuitColor, circuitOpacity);
      canvas.drawPath(_BiuPaths.circuitL, p);
      canvas.drawPath(_BiuPaths.circuitM, p);
      canvas.drawPath(_BiuPaths.circuitR, p);
    }
    if (handOpacity > 0) canvas.drawPath(_BiuPaths.hand, _paint(inkColor, handOpacity));

    canvas.restore();
  }

  @override
  bool shouldRepaint(covariant _BiuMarkPainter old) =>
      old.inkColor != inkColor ||
      old.circuitColor != circuitColor ||
      old.headOpacity != headOpacity ||
      old.circuitOpacity != circuitOpacity ||
      old.handOpacity != handOpacity;
}
