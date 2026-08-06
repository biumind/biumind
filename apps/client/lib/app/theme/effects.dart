// Effects — 复合视觉效果工厂(渐变叠层 / scrim / brand-tint mix)。
//
// 这里是 prototype CSS 那些"几层叠在一起"的 Flutter 等价实现:
//   * color-mix(in srgb, A 3%, B)        — colorMix(a, b, 0.03)
//   * card 3% brand 渐变层               — cardGradTint(brand)
//   * Hero radial(brand 80%) + linear   — heroDecoration(spec, brightness)
//   * Banner 文字底暗化 scrim            — bannerScrimGradient(spec)
//
// 设计原则:这层不暴露 widget,只提供 Decoration / Gradient 工厂。组件
// 拼装(Stack scrim 在 gradient 上,等等)在调用方处理。
//
// 跟 BiuColors 关系:本文件**不持有**主题色,只接受 PaletteSpec 和 Brightness
// 入参,纯函数。

import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';

import 'palettes.dart';

// ═════════════════════════════════════════════════════════════════════════
// colorMix — CSS color-mix(in srgb, a percent, b) 的 Flutter 等价
// ═════════════════════════════════════════════════════════════════════════
//
// CSS:`color-mix(in srgb, brand 3%, transparent)` 输出"3% brand + 97% 透明"。
// Flutter:Color.lerp(brand, transparent, 0.97) 不对(alpha 是另一回事)。
//
// 正解:逐通道 sRGB 加权平均(percent = a 的占比)。
// 注:srgb 加权技术上应在线性空间做,但 CSS 也是直接 sRGB 加权,这里跟 CSS
// 一致即可(避免色相偏移)。

Color colorMix(Color a, Color b, double percent) {
  final t = percent.clamp(0.0, 1.0);
  // ignore: deprecated_member_use - .a/.r/.g/.b 是 Color 4.0 字段,Flutter
  // 仍保留 .opacity / .red / .green / .blue 兼容,但新代码用 channel getter。
  return Color.from(
    alpha: a.a * t + b.a * (1 - t),
    red:   a.r * t + b.r * (1 - t),
    green: a.g * t + b.g * (1 - t),
    blue:  a.b * t + b.b * (1 - t),
  );
}

// ═════════════════════════════════════════════════════════════════════════
// Card — 3% brand 微染渐变叠层
// ═════════════════════════════════════════════════════════════════════════
//
// Prototype:
//   --card-grad: linear-gradient(135deg,
//     color-mix(in srgb, brand 3%, transparent),
//     transparent 50%);
//
// 用法:Container(decoration: BoxDecoration(color: surface0, gradient: cardGrad(brand)))
// 视觉:从左上角开始一抹极淡的 brand 色,半透明,扩散到 50% 后完全透明。

LinearGradient cardGradTint(Color brand, {double percent = 0.03}) {
  return LinearGradient(
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
    colors: [
      brand.withValues(alpha: percent),
      brand.withValues(alpha: 0.0),
    ],
    stops: const [0.0, 0.5],
  );
}

// ═════════════════════════════════════════════════════════════════════════
// Hero — radial(brand 80%) + linear(via, to) 双层 mesh
// ═════════════════════════════════════════════════════════════════════════
//
// Prototype Hybrid:
//   --hero-grad:
//     radial-gradient(at 0% 0%, color-mix(brand 80%, transparent), transparent 50%),
//     linear-gradient(135deg, grad-via, grad-to);
//
// Flutter 没有原生"两个 gradient 叠加"的 BoxDecoration,需要用 Stack 或
// foregroundDecoration 实现。这个工厂返回主 linear,radial 由调用方用
// foregroundDecoration 叠加 (radialOverlay 函数返回那一层)。

LinearGradient heroBaseLinear(PaletteSpec spec, Brightness mode) {
  final colors = spec.brandGradientFor(mode);
  // brandGradientFor 是 2-3 色,Hero 用 via→to (取后两个)。
  final via = colors.length >= 2 ? colors[colors.length - 2] : colors.first;
  final to = colors.last;
  return LinearGradient(
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
    colors: [via, to],
  );
}

/// Hero 顶角 radial overlay — 调用方放在 foregroundDecoration 或 Stack 上层。
RadialGradient heroRadialOverlay(PaletteSpec spec, Brightness mode) {
  final brand = spec.brandGradientFor(mode).first;
  return RadialGradient(
    center: const Alignment(-1, -1), // 0% 0% = top-left
    radius: 1.4,
    colors: [
      colorMix(brand, const Color(0x00000000), 0.8),
      const Color(0x00000000),
    ],
    stops: const [0.0, 0.5],
  );
}

/// Hero 完整 BoxDecoration — base linear + radial overlay 一次性给出。
/// 用法:Container(decoration: heroDecoration(spec, brightness, radius: 16))
BoxDecoration heroDecoration(
  PaletteSpec spec,
  Brightness mode, {
  BorderRadius? borderRadius,
}) {
  return BoxDecoration(
    gradient: heroBaseLinear(spec, mode),
    borderRadius: borderRadius,
  );
}

// ═════════════════════════════════════════════════════════════════════════
// Banner — gradient + scrim 暗化叠层 + text-shadow
// ═════════════════════════════════════════════════════════════════════════
//
// Prototype:banner 是 3 层(用户视角):
//   1. linear-gradient(135deg, from, via 60%, to)         — 主渐变
//   2. linear-gradient(180deg, 2% black → 16% black)      — 底部 scrim
//   3. text-shadow: 0 1px 2px rgba(0,0,0,0.16)           — 文字阴影
//
// Flutter:把 1 + 2 用 Stack 堆,文字 style 加 shadows。

/// Banner 主渐变(135deg, 3 stop)。直接消费 PaletteSpec.bannerFor(mode).gradient。
LinearGradient bannerMainGradient(BannerSpec banner) {
  return LinearGradient(
    begin: Alignment.topLeft,
    end: Alignment.bottomRight,
    colors: banner.gradient,
    stops: banner.gradient.length == 3 ? const [0.0, 0.6, 1.0] : null,
  );
}

/// Banner scrim 暗化层(180deg top→btm)。返回 LinearGradient,调用方
/// 包成 Container(decoration: BoxDecoration(gradient: scrim)) 叠在主渐变上。
LinearGradient bannerScrimGradient(BannerSpec banner) {
  return LinearGradient(
    begin: Alignment.topCenter,
    end: Alignment.bottomCenter,
    colors: banner.scrim,
  );
}

/// Banner 文字 text-shadow — 给 TextStyle.shadows 用。
const List<Shadow> bannerTextShadow = [
  Shadow(
    color: Color(0x29000000), // rgba(0,0,0,0.16)
    offset: Offset(0, 1),
    blurRadius: 2,
  ),
];

// ═════════════════════════════════════════════════════════════════════════
// Banner 完整 widget-friendly Decoration 组合(给 Stack 用)
// ═════════════════════════════════════════════════════════════════════════

/// 一次性返回 banner 的 (主渐变 decoration, scrim decoration)。
/// 用法:
///   Stack(
///     children: [
///       Positioned.fill(child: DecoratedBox(decoration: main)),
///       Positioned.fill(child: DecoratedBox(decoration: scrim)),
///       `<content>`
///     ],
///   )
({BoxDecoration main, BoxDecoration scrim}) bannerLayers(
  BannerSpec banner, {
  BorderRadius? borderRadius,
}) {
  return (
    main: BoxDecoration(
      gradient: bannerMainGradient(banner),
      borderRadius: borderRadius,
    ),
    scrim: BoxDecoration(
      gradient: bannerScrimGradient(banner),
      borderRadius: borderRadius,
    ),
  );
}

/// Banner 右上角微光高光 — prototype `::after { background:
/// radial-gradient(circle at 100% 0%, rgba(255,255,255,0.16), transparent 55%);
/// mix-blend-mode: soft-light }`。
///
/// Flutter BoxDecoration 不支持 mix-blend-mode,这里直接用低 alpha (8% 白)的
/// RadialGradient 叠层 — 视觉上接近 soft-light 在深色 banner 上的效果(亮一点点
/// 不刺眼)。叠在 main + scrim 上方。
BoxDecoration bannerHighlightOverlay({BorderRadius? borderRadius}) {
  return BoxDecoration(
    borderRadius: borderRadius,
    gradient: const RadialGradient(
      center: Alignment(1, -1), // 100% 0% = 右上角
      radius: 0.9,
      colors: [
        Color(0x14FFFFFF), // ~8% white
        Color(0x00FFFFFF),
      ],
      stops: [0.0, 0.55],
    ),
  );
}

/// Banner 顶部 1px 白色 inset 高光 — prototype `box-shadow: shadow-md,
/// inset 0 1px 0 rgba(255,255,255,0.12)`。
///
/// 早期实现用 LinearGradient stops `[0, 0.015]` 模拟,问题是 stop 是相对高度
/// 百分比,在 60px banner 上只有 ~1px 但在 80px banner 上就 1.2px,且渐变本身
/// 软边过渡看不到清晰的 1px 边线。改用 Border.only(top: 1px) 画**固定 1px**
/// 顶部边线,跟 prototype `inset 0 1px 0` 像素级一致,banner 多高都是 1px。
BoxDecoration bannerTopEdgeHighlight({BorderRadius? borderRadius}) {
  return BoxDecoration(
    borderRadius: borderRadius,
    border: const Border(
      top: BorderSide(
        color: Color(0x1FFFFFFF), // ~12% white,跟 prototype rgba(255,255,255,0.12) 一致
        width: 1,
      ),
    ),
  );
}

// ═════════════════════════════════════════════════════════════════════════
// Misc — alpha 渐隐 / 焦点环
// ═════════════════════════════════════════════════════════════════════════

/// Focus halo — 输入框 / 按钮 focused 时外圈光晕(brand-soft 3px)。
/// 调用方:`boxShadow: focusHalo(c.brand)`
List<BoxShadow> focusHalo(Color brand) {
  return [
    BoxShadow(
      color: brand.withValues(alpha: 0.18),
      blurRadius: 0,
      spreadRadius: 3,
    ),
  ];
}

/// 给 lerpDouble 一个有意义的别名,文档清晰。
double lerpD(double a, double b, double t) => lerpDouble(a, b, t)!;
