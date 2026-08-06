// ThemeExtension 三件套 — 业务 token 的入口,新模块**唯一**应该读的地方。
//
//   BiuColors  — 颜色 (brand / accent / mode / banner / surface / text / border)
//   BiuMetrics — 字号 / 列表宽度 / padding / 元素尺寸 (跟 FontSize 联动)
//   BiuMotion  — 动画时长 / 曲线
//
// 用法:
//   final c = Theme.of(context).extension<BiuColors>()!;
//   final m = Theme.of(context).extension<BiuMetrics>()!;
//   final mo = Theme.of(context).extension<BiuMotion>()!;
//
// 加新业务 token:
//   1. 在对应 Extension 加字段 + copyWith / lerp 也加
//   2. theme_builder.dart 里构造 ThemeData 时填值
//   3. 不要为了"图省事"硬编码 hex / 数值,token 从此处开始才能跨色板复用
//
// 反模式:
//   ❌ Color(0xFF7C3AED) 写死 → 色板切换不跟 (T1 不变量)
//   ❌ EdgeInsets.all(12) 写死 → 字号档位不跟 (用 m.cardPad)
//   ❌ Duration(milliseconds: 200) 写死 → 用 mo.normal

import 'package:flutter/material.dart';

import 'font_size.dart';
import 'palettes.dart';
import 'tokens.dart';

// ═════════════════════════════════════════════════════════════════════════
// BiuColors
// ═════════════════════════════════════════════════════════════════════════

@immutable
class BiuColors extends ThemeExtension<BiuColors> {
  const BiuColors({
    // ── 品牌 / 强调 ─────────────────────────────────────
    required this.brand,
    required this.brandHover,
    required this.brandSoft,
    required this.accent,
    required this.accentHover,
    required this.accentSoft,
    required this.brandGradient,
    // ── Mode 三色 ───────────────────────────────────────
    required this.modeChat,
    required this.modeAgent,
    required this.modeTask,
    // ── Banner 全套 ─────────────────────────────────────
    required this.bannerGradient,
    required this.bannerScrim,
    required this.bannerFg,
    required this.bannerFgDim,
    required this.bannerCtaBg,
    required this.bannerCtaFg,
    required this.bannerCtaBorder,
    // ── 中性色 ──────────────────────────────────────────
    required this.bgApp,
    required this.surface0,
    required this.surface1,
    required this.surface2,
    required this.surface3,
    required this.text1,
    required this.text2,
    required this.text3,
    required this.textMuted,
    required this.borderHairline,
    required this.borderSoft,
    required this.borderStrong,
    // ── Semantic ────────────────────────────────────────
    required this.success,
    required this.successSoft,
    required this.warning,
    required this.warningSoft,
    required this.error,
    required this.errorSoft,
    required this.info,
    required this.infoSoft,
  });

  // 品牌 / 强调
  final Color brand;
  final Color brandHover;
  final Color brandSoft;
  final Color accent;
  final Color accentHover;
  final Color accentSoft;
  final List<Color> brandGradient;

  // Mode
  final Color modeChat;
  final Color modeAgent;
  final Color modeTask;

  // Banner
  final List<Color> bannerGradient;
  final List<Color> bannerScrim;
  final Color bannerFg;
  final Color bannerFgDim;
  final Color bannerCtaBg;
  final Color bannerCtaFg;
  final Color bannerCtaBorder;

  // 中性
  final Color bgApp;
  final Color surface0;
  final Color surface1;
  final Color surface2;
  final Color surface3;
  final Color text1;
  final Color text2;
  final Color text3;
  final Color textMuted;
  final Color borderHairline;
  final Color borderSoft;
  final Color borderStrong;

  // Semantic
  final Color success;
  final Color successSoft;
  final Color warning;
  final Color warningSoft;
  final Color error;
  final Color errorSoft;
  final Color info;
  final Color infoSoft;

  /// Mode 颜色按枚举取 — 用来给 thread tile 上的 mode-dot / mode-tag 上色,
  /// 避免 widget 里 switch 三遍。
  Color modeColor(ChatMode mode) => switch (mode) {
        ChatMode.chat => modeChat,
        ChatMode.agent => modeAgent,
        ChatMode.task => modeTask,
      };

  // ── 工厂: 从 PaletteSpec + Brightness 构造 ────────────────────────────
  factory BiuColors.fromPalette(PaletteSpec p, Brightness b) {
    final neutral = NeutralTokens.forBrightness(b);
    final banner = p.bannerFor(b);
    return BiuColors(
      brand: p.brand.forBrightness(b),
      brandHover: p.brandHover.forBrightness(b),
      brandSoft: p.brandSoft.forBrightness(b),
      accent: p.accent.forBrightness(b),
      accentHover: p.accentHover.forBrightness(b),
      accentSoft: p.accentSoft.forBrightness(b),
      brandGradient: p.brandGradientFor(b),
      modeChat: p.modeChat.forBrightness(b),
      modeAgent: p.modeAgent.forBrightness(b),
      modeTask: p.modeTask.forBrightness(b),
      bannerGradient: banner.gradient,
      bannerScrim: banner.scrim,
      bannerFg: banner.fg,
      bannerFgDim: banner.fgDim,
      bannerCtaBg: banner.ctaBg,
      bannerCtaFg: banner.ctaFg,
      bannerCtaBorder: banner.ctaBorder,
      bgApp: neutral.bgApp,
      surface0: neutral.surface0,
      surface1: neutral.surface1,
      surface2: neutral.surface2,
      surface3: neutral.surface3,
      text1: neutral.text1,
      text2: neutral.text2,
      text3: neutral.text3,
      textMuted: neutral.textMuted,
      borderHairline: neutral.borderHairline,
      borderSoft: neutral.borderSoft,
      borderStrong: neutral.borderStrong,
      success: SemanticTokens.success,
      successSoft: SemanticTokens.successSoft,
      warning: SemanticTokens.warning,
      warningSoft: SemanticTokens.warningSoft,
      error: SemanticTokens.error,
      errorSoft: b == Brightness.dark
          ? SemanticTokens.errorSoftD
          : SemanticTokens.errorSoftL,
      info: SemanticTokens.info,
      infoSoft: SemanticTokens.infoSoft,
    );
  }

  // ── ThemeExtension required ───────────────────────────────────────────

  @override
  BiuColors copyWith({
    Color? brand,
    Color? brandHover,
    Color? brandSoft,
    Color? accent,
    Color? accentHover,
    Color? accentSoft,
    List<Color>? brandGradient,
    Color? modeChat,
    Color? modeAgent,
    Color? modeTask,
    List<Color>? bannerGradient,
    List<Color>? bannerScrim,
    Color? bannerFg,
    Color? bannerFgDim,
    Color? bannerCtaBg,
    Color? bannerCtaFg,
    Color? bannerCtaBorder,
    Color? bgApp,
    Color? surface0,
    Color? surface1,
    Color? surface2,
    Color? surface3,
    Color? text1,
    Color? text2,
    Color? text3,
    Color? textMuted,
    Color? borderHairline,
    Color? borderSoft,
    Color? borderStrong,
    Color? success,
    Color? successSoft,
    Color? warning,
    Color? warningSoft,
    Color? error,
    Color? errorSoft,
    Color? info,
    Color? infoSoft,
  }) =>
      BiuColors(
        brand: brand ?? this.brand,
        brandHover: brandHover ?? this.brandHover,
        brandSoft: brandSoft ?? this.brandSoft,
        accent: accent ?? this.accent,
        accentHover: accentHover ?? this.accentHover,
        accentSoft: accentSoft ?? this.accentSoft,
        brandGradient: brandGradient ?? this.brandGradient,
        modeChat: modeChat ?? this.modeChat,
        modeAgent: modeAgent ?? this.modeAgent,
        modeTask: modeTask ?? this.modeTask,
        bannerGradient: bannerGradient ?? this.bannerGradient,
        bannerScrim: bannerScrim ?? this.bannerScrim,
        bannerFg: bannerFg ?? this.bannerFg,
        bannerFgDim: bannerFgDim ?? this.bannerFgDim,
        bannerCtaBg: bannerCtaBg ?? this.bannerCtaBg,
        bannerCtaFg: bannerCtaFg ?? this.bannerCtaFg,
        bannerCtaBorder: bannerCtaBorder ?? this.bannerCtaBorder,
        bgApp: bgApp ?? this.bgApp,
        surface0: surface0 ?? this.surface0,
        surface1: surface1 ?? this.surface1,
        surface2: surface2 ?? this.surface2,
        surface3: surface3 ?? this.surface3,
        text1: text1 ?? this.text1,
        text2: text2 ?? this.text2,
        text3: text3 ?? this.text3,
        textMuted: textMuted ?? this.textMuted,
        borderHairline: borderHairline ?? this.borderHairline,
        borderSoft: borderSoft ?? this.borderSoft,
        borderStrong: borderStrong ?? this.borderStrong,
        success: success ?? this.success,
        successSoft: successSoft ?? this.successSoft,
        warning: warning ?? this.warning,
        warningSoft: warningSoft ?? this.warningSoft,
        error: error ?? this.error,
        errorSoft: errorSoft ?? this.errorSoft,
        info: info ?? this.info,
        infoSoft: infoSoft ?? this.infoSoft,
      );

  /// 主题切换不需要插值 (用户切色板瞬间生效) — 直接二选一。
  /// 仅 t < 0.5 用 a,否则 b。
  @override
  BiuColors lerp(ThemeExtension<BiuColors>? other, double t) {
    if (other is! BiuColors) return this;
    return t < 0.5 ? this : other;
  }
}

/// Chat thread mode — 跟 BiuColors.modeColor() 配套用。组件传 ChatMode,
/// 拿到 mode-dot 颜色,不需要重复 switch。
enum ChatMode { chat, agent, task }

// ═════════════════════════════════════════════════════════════════════════
// BiuMetrics — 字号联动的尺寸 (FontSizeTokens 的 ThemeExtension 包装)
// ═════════════════════════════════════════════════════════════════════════

@immutable
class BiuMetrics extends ThemeExtension<BiuMetrics> {
  const BiuMetrics({required this.tokens});

  /// 持有原始 FontSizeTokens — 直接 forward 字段,不重复定义。
  final FontSizeTokens tokens;

  // ── 字段 forward (方便组件直接 `m.cardPad` 而不是 `m.tokens.cardPad`) ─
  double get navRailWidth => tokens.navRailWidth;
  double get threadListWidth => tokens.threadListWidth;
  double get navItemPadH => tokens.navItemPadH;
  double get navItemPadV => tokens.navItemPadV;
  double get tilePadH => tokens.tilePadH;
  double get tilePadV => tokens.tilePadV;
  double get cardPad => tokens.cardPad;
  double get bannerPadH => tokens.bannerPadH;
  double get bannerPadV => tokens.bannerPadV;
  double get gapGrid => tokens.gapGrid;
  double get gapSection => tokens.gapSection;
  double get fontBase => tokens.fontBase;
  double get fontSm => tokens.fontSm;
  double get fontTileTitle => tokens.fontTileTitle;
  double get fontTileMeta => tokens.fontTileMeta;
  double get fontCardTitle => tokens.fontCardTitle;
  double get fontCardBody => tokens.fontCardBody;
  double get fontHero => tokens.fontHero;
  double get fontH2 => tokens.fontH2;
  double get avatarSize => tokens.avatarSize;
  double get modeDotSize => tokens.modeDotSize;
  double get lineTight => tokens.lineTight;
  double get lineBody => tokens.lineBody;

  @override
  BiuMetrics copyWith({FontSizeTokens? tokens}) =>
      BiuMetrics(tokens: tokens ?? this.tokens);

  /// 字号档切换不插值 — 跟主题色一样瞬间生效,避免布局颠簸。
  @override
  BiuMetrics lerp(ThemeExtension<BiuMetrics>? other, double t) {
    if (other is! BiuMetrics) return this;
    return t < 0.5 ? this : other;
  }
}

// ═════════════════════════════════════════════════════════════════════════
// BiuMotion
// ═════════════════════════════════════════════════════════════════════════

@immutable
class BiuMotion extends ThemeExtension<BiuMotion> {
  const BiuMotion({
    required this.fast,
    required this.normal,
    required this.slow,
    required this.standard,
    required this.bounce,
  });

  final Duration fast;
  final Duration normal;
  final Duration slow;
  final Curve standard;
  final Curve bounce;

  /// 静态默认 — 直接复用 MotionTokens 常量。
  static const BiuMotion defaults = BiuMotion(
    fast: MotionTokens.fast,
    normal: MotionTokens.normal,
    slow: MotionTokens.slow,
    standard: MotionTokens.standard,
    bounce: MotionTokens.bounce,
  );

  @override
  BiuMotion copyWith({
    Duration? fast,
    Duration? normal,
    Duration? slow,
    Curve? standard,
    Curve? bounce,
  }) =>
      BiuMotion(
        fast: fast ?? this.fast,
        normal: normal ?? this.normal,
        slow: slow ?? this.slow,
        standard: standard ?? this.standard,
        bounce: bounce ?? this.bounce,
      );

  @override
  BiuMotion lerp(ThemeExtension<BiuMotion>? other, double t) {
    if (other is! BiuMotion) return this;
    return t < 0.5 ? this : other;
  }
}
