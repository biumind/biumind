// Palettes — 17 个候选色板的数据契约 + 实例。
//
// PaletteSpec 是一个色板的全部色值。新增色板:
//   1. 在 PaletteId 加一项 (camelCase enum + kebab-case wireId)
//   2. 在文件下方加 const _xxx PaletteSpec
//   3. 在 _palettes map 加一条
//   4. 在 i18n 加 paletteName_xxx / paletteDesc_xxx 两个 key
//   5. 跑 WCAG 对比度检查 (脚本待加 — 见 §10 验收)
//
// 暗色适配规则 (§4.3):
//   * 主色暗 (brand 光度 < L40 — onyx / inkblue-orange / quantum-titanium /
//     claude-warm / graphite-cyan / indigo-sand) → dark 下 brand 与 accent
//     互换 (用浅色作 dark.brand)
//   * 主色亮 (其他) → dark.brand 比 light.brand 提一档 (#7C3AED → #8B5CF6),
//     soft alpha 加倍 (0.10 → 0.18)
//   * 全部色板 dark.banner 用 light.banner 的"加深版"渐变
//
// 读色规则:
//   * 业务代码: Theme.of(context).extension<BiuColors>()!.brand
//   * 不要直接 import palettes.dart — 它是数据源,不是消费层

import 'package:flutter/material.dart';

// ═════════════════════════════════════════════════════════════════════════
// 色板枚举
// ═════════════════════════════════════════════════════════════════════════

enum PaletteId {
  purpleOrange,        // ✅ default — 紫 + 橘
  purple,              // 1. 紫(原品牌)
  purpleBlue,          // 2. 紫 + 蓝
  purplePink,          // 3. 紫 + 粉
  purpleEmerald,       // 4. 紫 + 翡翠
  aurora,              // 5. 极光(青-紫-粉)
  sunset,              // 6. 日落(橙-粉-紫)
  cyber,               // 7. 赛博(青-紫-品红)
  ocean,               // 8. 海洋(蓝-青)
  emeraldGold,         // 9. 翡翠金
  rose,                // 10. 玫瑰
  onyx,                // 11. 黑曜青
  inkblueOrange,       // 12. 墨蓝 + 信号橙
  quantumTitanium,     // 13. 量子紫 + 钛金
  claudeWarm,          // 14. Claude 暖
  graphiteCyan,        // 15. 石墨 + 量子青
  indigoSand,          // 16. 靛青 + 暖砂
  wikiGreen;           // 17. Wiki 翠绿 — 旧 wiki 的 emerald 主色 + 沙金 accent

  /// 持久化用 wire id (kebab-case)。
  String get wireId => switch (this) {
        PaletteId.purpleOrange => 'purple-orange',
        PaletteId.purple => 'purple',
        PaletteId.purpleBlue => 'purple-blue',
        PaletteId.purplePink => 'purple-pink',
        PaletteId.purpleEmerald => 'purple-emerald',
        PaletteId.aurora => 'aurora',
        PaletteId.sunset => 'sunset',
        PaletteId.cyber => 'cyber',
        PaletteId.ocean => 'ocean',
        PaletteId.emeraldGold => 'emerald-gold',
        PaletteId.rose => 'rose',
        PaletteId.onyx => 'onyx',
        PaletteId.inkblueOrange => 'inkblue-orange',
        PaletteId.quantumTitanium => 'quantum-titanium',
        PaletteId.claudeWarm => 'claude-warm',
        PaletteId.graphiteCyan => 'graphite-cyan',
        PaletteId.indigoSand => 'indigo-sand',
        PaletteId.wikiGreen => 'wiki-green',
      };

  static PaletteId byWireId(String? id) {
    for (final p in PaletteId.values) {
      if (p.wireId == id) return p;
    }
    // 跟 AppSettings 默认保持一致 — wire id 不识别时 fallback 到 prototype 默认。
    return PaletteId.inkblueOrange;
  }
}

// ═════════════════════════════════════════════════════════════════════════
// 色对 / banner 规格 / 渐变
// ═════════════════════════════════════════════════════════════════════════

@immutable
class ColorPair {
  const ColorPair({required this.light, required this.dark});
  final Color light;
  final Color dark;

  Color forBrightness(Brightness b) =>
      b == Brightness.dark ? dark : light;
}

/// Banner 专属色 token — 防止深色渐变 banner 上文字对比度雷区(§2.5 / §4.6)。
@immutable
class BannerSpec {
  const BannerSpec({
    required this.gradient,
    required this.fg,
    required this.fgDim,
    required this.scrim,
    required this.ctaBg,
    required this.ctaFg,
    required this.ctaBorder,
  });

  final List<Color> gradient;
  final Color fg;
  final Color fgDim;
  final List<Color> scrim;
  final Color ctaBg;
  final Color ctaFg;
  final Color ctaBorder;
}

// ── Banner 标准白色覆盖层 (Hybrid 调性默认 — 16/17 色板用)──────────────────

const Color _bFg        = Color(0xFFFFFFFF);
const Color _bFgDim     = Color(0xDCFFFFFF); // rgba(255,255,255,0.86)
const List<Color> _bScrim = [
  Color(0x05000000), // rgba(0,0,0,0.02)
  Color(0x29000000), // rgba(0,0,0,0.16)
];
const Color _bCtaBg     = Color(0x2EFFFFFF); // rgba(255,255,255,0.18)
const Color _bCtaFg     = Color(0xFFFFFFFF);
const Color _bCtaBorder = Color(0x52FFFFFF); // rgba(255,255,255,0.32)

// ── 静态 Mode-Chat 蓝 (跨色板恒定) ─────────────────────────────────────
//
// chat = info-blue,跟色板无关;agent = brand,task = accent。
// 例外:`ocean` (brand 已是蓝) 和 `purpleBlue` (accent 蓝) 在 PaletteSpec 内
// 主动 override modeChat 避免和品牌色撞色。

const ColorPair _kModeChatBlue = ColorPair(
  light: Color(0xFF3B82F6),
  dark:  Color(0xFF60A5FA),
);

// ═════════════════════════════════════════════════════════════════════════
// PaletteSpec
// ═════════════════════════════════════════════════════════════════════════

@immutable
class PaletteSpec {
  const PaletteSpec({
    required this.id,
    required this.displayNameKey,
    required this.descriptionKey,
    required this.brand,
    required this.brandHover,
    required this.brandSoft,
    required this.accent,
    required this.accentHover,
    required this.accentSoft,
    required this.modeChat,
    required this.modeAgent,
    required this.modeTask,
    required this.brandGradientLight,
    required this.brandGradientDark,
    required this.bannerLight,
    required this.bannerDark,
    this.isFeatured = false,
  });

  final PaletteId id;
  final String displayNameKey;
  final String descriptionKey;
  final ColorPair brand;
  final ColorPair brandHover;
  final ColorPair brandSoft;
  final ColorPair accent;
  final ColorPair accentHover;
  final ColorPair accentSoft;
  final ColorPair modeChat;
  final ColorPair modeAgent;
  final ColorPair modeTask;
  final List<Color> brandGradientLight;
  final List<Color> brandGradientDark;
  final BannerSpec bannerLight;
  final BannerSpec bannerDark;
  final bool isFeatured;

  BannerSpec bannerFor(Brightness b) =>
      b == Brightness.dark ? bannerDark : bannerLight;

  List<Color> brandGradientFor(Brightness b) =>
      b == Brightness.dark ? brandGradientDark : brandGradientLight;
}

// ═════════════════════════════════════════════════════════════════════════
// 色板实例
// ═════════════════════════════════════════════════════════════════════════
//
// 字段排列约定(每个色板都按这个顺序读得快):
//   id / displayNameKey / descriptionKey / isFeatured
//   brand / brandHover / brandSoft
//   accent / accentHover / accentSoft
//   modeChat / modeAgent / modeTask
//   brandGradientLight / brandGradientDark
//   bannerLight / bannerDark
//
// 色值出处:docs/ui-prototype/biumind-ui-explorer-v3.html line 62-102

// ── ✅ 紫 + 橘(默认) ─────────────────────────────────────────────
const PaletteSpec _purpleOrange = PaletteSpec(
  id: PaletteId.purpleOrange,
  displayNameKey: 'paletteName_purpleOrange',
  descriptionKey: 'paletteDesc_purpleOrange',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandHover: ColorPair(light: Color(0xFF6D28D9), dark: Color(0xFF7C3AED)),
  brandSoft: ColorPair(light: Color(0x1A7C3AED), dark: Color(0x2E8B5CF6)),
  accent: ColorPair(light: Color(0xFFFB923C), dark: Color(0xFFFDBA74)),
  accentHover: ColorPair(light: Color(0xFFF97316), dark: Color(0xFFFB923C)),
  accentSoft: ColorPair(light: Color(0x1FFB923C), dark: Color(0x33FDBA74)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  modeTask: ColorPair(light: Color(0xFFFB923C), dark: Color(0xFFFDBA74)),
  brandGradientLight: [Color(0xFF7C3AED), Color(0xFFFB923C)],
  brandGradientDark: [Color(0xFF8B5CF6), Color(0xFFFDBA74)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFFB923C), Color(0xFF7C3AED), Color(0xFF4C1D95)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFFDBA74), Color(0xFF8B5CF6), Color(0xFF5B21B6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 1. 紫(原品牌延续) ───────────────────────────────────────────
const PaletteSpec _purple = PaletteSpec(
  id: PaletteId.purple,
  displayNameKey: 'paletteName_purple',
  descriptionKey: 'paletteDesc_purple',
  brand: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandHover: ColorPair(light: Color(0xFF6D28D9), dark: Color(0xFF7C3AED)),
  brandSoft: ColorPair(light: Color(0x1A7C3AED), dark: Color(0x2E8B5CF6)),
  accent: ColorPair(light: Color(0xFFA78BFA), dark: Color(0xFFC4B5FD)),
  accentHover: ColorPair(light: Color(0xFF8B5CF6), dark: Color(0xFFA78BFA)),
  accentSoft: ColorPair(light: Color(0x1FA78BFA), dark: Color(0x33C4B5FD)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  modeTask: ColorPair(light: Color(0xFFA78BFA), dark: Color(0xFFC4B5FD)),
  brandGradientLight: [Color(0xFF8B5CF6), Color(0xFF7C3AED), Color(0xFF4C1D95)],
  brandGradientDark: [Color(0xFFA78BFA), Color(0xFF8B5CF6), Color(0xFF6D28D9)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF8B5CF6), Color(0xFF7C3AED), Color(0xFF4C1D95)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFA78BFA), Color(0xFF8B5CF6), Color(0xFF6D28D9)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 2. 紫 + 蓝 ──────────────────────────────────────────────────
//
// modeChat 在此 override — 用 brand 紫,避免 accent 蓝跟 chat info-blue 撞。
const PaletteSpec _purpleBlue = PaletteSpec(
  id: PaletteId.purpleBlue,
  displayNameKey: 'paletteName_purpleBlue',
  descriptionKey: 'paletteDesc_purpleBlue',
  brand: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandHover: ColorPair(light: Color(0xFF6D28D9), dark: Color(0xFF7C3AED)),
  brandSoft: ColorPair(light: Color(0x1A7C3AED), dark: Color(0x2E8B5CF6)),
  accent: ColorPair(light: Color(0xFF3B82F6), dark: Color(0xFF60A5FA)),
  accentHover: ColorPair(light: Color(0xFF2563EB), dark: Color(0xFF3B82F6)),
  accentSoft: ColorPair(light: Color(0x1F3B82F6), dark: Color(0x3360A5FA)),
  modeChat: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  modeAgent: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  modeTask: ColorPair(light: Color(0xFF3B82F6), dark: Color(0xFF60A5FA)),
  brandGradientLight: [Color(0xFF8B5CF6), Color(0xFF6366F1), Color(0xFF3B82F6)],
  brandGradientDark: [Color(0xFFA78BFA), Color(0xFF818CF8), Color(0xFF60A5FA)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF8B5CF6), Color(0xFF6366F1), Color(0xFF3B82F6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // dark 加深尾色 — light blue → indigo-950 让白字可读
    gradient: [Color(0xFFA78BFA), Color(0xFF818CF8), Color(0xFF1E1B4B)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 3. 紫 + 粉 ─────────────────────────────────────────────────
const PaletteSpec _purplePink = PaletteSpec(
  id: PaletteId.purplePink,
  displayNameKey: 'paletteName_purplePink',
  descriptionKey: 'paletteDesc_purplePink',
  brand: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandHover: ColorPair(light: Color(0xFF6D28D9), dark: Color(0xFF7C3AED)),
  brandSoft: ColorPair(light: Color(0x1AEC4899), dark: Color(0x2EEC4899)),
  accent: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  accentHover: ColorPair(light: Color(0xFFDB2777), dark: Color(0xFFEC4899)),
  accentSoft: ColorPair(light: Color(0x1FEC4899), dark: Color(0x33F472B6)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  modeTask: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  brandGradientLight: [Color(0xFFEC4899), Color(0xFFA855F7), Color(0xFF6D28D9)],
  brandGradientDark: [Color(0xFFF472B6), Color(0xFFC084FC), Color(0xFF8B5CF6)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFEC4899), Color(0xFFA855F7), Color(0xFF6D28D9)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFF472B6), Color(0xFFC084FC), Color(0xFF8B5CF6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 4. 紫 + 翡翠 ───────────────────────────────────────────────
const PaletteSpec _purpleEmerald = PaletteSpec(
  id: PaletteId.purpleEmerald,
  displayNameKey: 'paletteName_purpleEmerald',
  descriptionKey: 'paletteDesc_purpleEmerald',
  brand: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandHover: ColorPair(light: Color(0xFF6D28D9), dark: Color(0xFF7C3AED)),
  brandSoft: ColorPair(light: Color(0x1A10B981), dark: Color(0x2E10B981)),
  accent: ColorPair(light: Color(0xFF10B981), dark: Color(0xFF34D399)),
  accentHover: ColorPair(light: Color(0xFF059669), dark: Color(0xFF10B981)),
  accentSoft: ColorPair(light: Color(0x1F10B981), dark: Color(0x3334D399)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  modeTask: ColorPair(light: Color(0xFF10B981), dark: Color(0xFF34D399)),
  brandGradientLight: [Color(0xFF7C3AED), Color(0xFF6366F1), Color(0xFF10B981)],
  brandGradientDark: [Color(0xFF8B5CF6), Color(0xFF818CF8), Color(0xFF34D399)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF7C3AED), Color(0xFF6366F1), Color(0xFF10B981)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFF8B5CF6), Color(0xFF818CF8), Color(0xFF34D399)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 5. 极光(青-紫-粉) ───────────────────────────────────────────
const PaletteSpec _aurora = PaletteSpec(
  id: PaletteId.aurora,
  displayNameKey: 'paletteName_aurora',
  descriptionKey: 'paletteDesc_aurora',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF8B5CF6), dark: Color(0xFFA78BFA)),
  brandHover: ColorPair(light: Color(0xFF7C3AED), dark: Color(0xFF8B5CF6)),
  brandSoft: ColorPair(light: Color(0x1A8B5CF6), dark: Color(0x338B5CF6)),
  accent: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  accentHover: ColorPair(light: Color(0xFFDB2777), dark: Color(0xFFEC4899)),
  accentSoft: ColorPair(light: Color(0x1FEC4899), dark: Color(0x33F472B6)),
  modeChat: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  modeAgent: ColorPair(light: Color(0xFF8B5CF6), dark: Color(0xFFA78BFA)),
  modeTask: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  brandGradientLight: [Color(0xFF06B6D4), Color(0xFF8B5CF6), Color(0xFFEC4899)],
  brandGradientDark: [Color(0xFF22D3EE), Color(0xFFA78BFA), Color(0xFFF472B6)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF06B6D4), Color(0xFF8B5CF6), Color(0xFFEC4899)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // 加深第一 stop 让 banner 文字在 dark 下也可读 (WCAG AA Large)
    gradient: [Color(0xFF0E7490), Color(0xFFA78BFA), Color(0xFFF472B6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 6. 日落(橙-粉-紫) ──────────────────────────────────────────
const PaletteSpec _sunset = PaletteSpec(
  id: PaletteId.sunset,
  displayNameKey: 'paletteName_sunset',
  descriptionKey: 'paletteDesc_sunset',
  brand: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  brandHover: ColorPair(light: Color(0xFFDB2777), dark: Color(0xFFEC4899)),
  brandSoft: ColorPair(light: Color(0x1AEC4899), dark: Color(0x33F472B6)),
  accent: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  accentHover: ColorPair(light: Color(0xFFD97706), dark: Color(0xFFF59E0B)),
  accentSoft: ColorPair(light: Color(0x1FF59E0B), dark: Color(0x33FCD34D)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFFEC4899), dark: Color(0xFFF472B6)),
  modeTask: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  brandGradientLight: [Color(0xFFF59E0B), Color(0xFFEC4899), Color(0xFF7C3AED)],
  brandGradientDark: [Color(0xFFFCD34D), Color(0xFFF472B6), Color(0xFFA78BFA)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFF59E0B), Color(0xFFEC4899), Color(0xFF7C3AED)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // dark 加深尾色 — 紫色调一档深
    gradient: [Color(0xFFFCD34D), Color(0xFFF472B6), Color(0xFF6D28D9)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 7. 赛博(青-紫-品红) ────────────────────────────────────────
const PaletteSpec _cyber = PaletteSpec(
  id: PaletteId.cyber,
  displayNameKey: 'paletteName_cyber',
  descriptionKey: 'paletteDesc_cyber',
  brand: ColorPair(light: Color(0xFFA855F7), dark: Color(0xFFC084FC)),
  brandHover: ColorPair(light: Color(0xFF9333EA), dark: Color(0xFFA855F7)),
  brandSoft: ColorPair(light: Color(0x1AA855F7), dark: Color(0x2E22D3EE)),
  accent: ColorPair(light: Color(0xFF22D3EE), dark: Color(0xFF67E8F9)),
  accentHover: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  accentSoft: ColorPair(light: Color(0x1F22D3EE), dark: Color(0x3367E8F9)),
  modeChat: ColorPair(light: Color(0xFF22D3EE), dark: Color(0xFF67E8F9)),
  modeAgent: ColorPair(light: Color(0xFFA855F7), dark: Color(0xFFC084FC)),
  modeTask: ColorPair(light: Color(0xFFF472B6), dark: Color(0xFFF9A8D4)),
  brandGradientLight: [Color(0xFF22D3EE), Color(0xFFA855F7), Color(0xFFF472B6)],
  brandGradientDark: [Color(0xFF67E8F9), Color(0xFFC084FC), Color(0xFFF9A8D4)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF22D3EE), Color(0xFFA855F7), Color(0xFFF472B6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // dark 加深首色 — cyan-300 → cyan-700,白字可读
    gradient: [Color(0xFF0E7490), Color(0xFFC084FC), Color(0xFFF9A8D4)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 8. 海洋(蓝-青) ────────────────────────────────────────────
//
// brand 已是蓝色,modeChat override 成 cyan(青)避免和 brand 撞。
const PaletteSpec _ocean = PaletteSpec(
  id: PaletteId.ocean,
  displayNameKey: 'paletteName_ocean',
  descriptionKey: 'paletteDesc_ocean',
  brand: ColorPair(light: Color(0xFF3B82F6), dark: Color(0xFF60A5FA)),
  brandHover: ColorPair(light: Color(0xFF2563EB), dark: Color(0xFF3B82F6)),
  brandSoft: ColorPair(light: Color(0x1A3B82F6), dark: Color(0x2E60A5FA)),
  accent: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  accentHover: ColorPair(light: Color(0xFF0891B2), dark: Color(0xFF06B6D4)),
  accentSoft: ColorPair(light: Color(0x1F06B6D4), dark: Color(0x3322D3EE)),
  modeChat: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  modeAgent: ColorPair(light: Color(0xFF3B82F6), dark: Color(0xFF60A5FA)),
  modeTask: ColorPair(light: Color(0xFF1E40AF), dark: Color(0xFF3B82F6)),
  brandGradientLight: [Color(0xFF06B6D4), Color(0xFF3B82F6), Color(0xFF1E40AF)],
  brandGradientDark: [Color(0xFF22D3EE), Color(0xFF60A5FA), Color(0xFF3B82F6)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF06B6D4), Color(0xFF3B82F6), Color(0xFF1E40AF)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFF22D3EE), Color(0xFF60A5FA), Color(0xFF3B82F6)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 9. 翡翠金 ──────────────────────────────────────────────────
//
// 注:light brand 用 emerald-600 (#059669) 而非 emerald-500 — 后者对白色 surface
// 对比度仅 2.54:1,不到 AA Large 3:1 阈值。
const PaletteSpec _emeraldGold = PaletteSpec(
  id: PaletteId.emeraldGold,
  displayNameKey: 'paletteName_emeraldGold',
  descriptionKey: 'paletteDesc_emeraldGold',
  brand: ColorPair(light: Color(0xFF059669), dark: Color(0xFF34D399)),
  brandHover: ColorPair(light: Color(0xFF047857), dark: Color(0xFF10B981)),
  brandSoft: ColorPair(light: Color(0x1F059669), dark: Color(0x2EF59E0B)),
  accent: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  accentHover: ColorPair(light: Color(0xFFD97706), dark: Color(0xFFF59E0B)),
  accentSoft: ColorPair(light: Color(0x1FF59E0B), dark: Color(0x33FCD34D)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF059669), dark: Color(0xFF34D399)),
  modeTask: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  brandGradientLight: [Color(0xFF10B981), Color(0xFF059669), Color(0xFFF59E0B)],
  brandGradientDark: [Color(0xFF34D399), Color(0xFF10B981), Color(0xFFFCD34D)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF10B981), Color(0xFF059669), Color(0xFFF59E0B)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // dark 锚一深 emerald — light all-light → 加 emerald-900 让白字可读
    gradient: [Color(0xFF34D399), Color(0xFF10B981), Color(0xFF064E3B)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 10. 玫瑰 ──────────────────────────────────────────────────
const PaletteSpec _rose = PaletteSpec(
  id: PaletteId.rose,
  displayNameKey: 'paletteName_rose',
  descriptionKey: 'paletteDesc_rose',
  brand: ColorPair(light: Color(0xFFE11D48), dark: Color(0xFFFB7185)),
  brandHover: ColorPair(light: Color(0xFFBE123C), dark: Color(0xFFE11D48)),
  brandSoft: ColorPair(light: Color(0x1AE11D48), dark: Color(0x33FB7185)),
  accent: ColorPair(light: Color(0xFFFB7185), dark: Color(0xFFFDA4AF)),
  accentHover: ColorPair(light: Color(0xFFE11D48), dark: Color(0xFFFB7185)),
  accentSoft: ColorPair(light: Color(0x1FFB7185), dark: Color(0x33FDA4AF)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFFE11D48), dark: Color(0xFFFB7185)),
  modeTask: ColorPair(light: Color(0xFFFB7185), dark: Color(0xFFFDA4AF)),
  brandGradientLight: [Color(0xFFFB7185), Color(0xFFE11D48), Color(0xFF881337)],
  brandGradientDark: [Color(0xFFFDA4AF), Color(0xFFFB7185), Color(0xFFBE123C)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFFB7185), Color(0xFFE11D48), Color(0xFF881337)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFFDA4AF), Color(0xFFFB7185), Color(0xFFBE123C)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 11. 黑曜青(swap palette — dark 下 brand/accent 互换) ─────────
const PaletteSpec _onyx = PaletteSpec(
  id: PaletteId.onyx,
  displayNameKey: 'paletteName_onyx',
  descriptionKey: 'paletteDesc_onyx',
  brand: ColorPair(light: Color(0xFF0F172A), dark: Color(0xFFF5F5F7)),
  brandHover: ColorPair(light: Color(0xFF020617), dark: Color(0xFFE5E5EA)),
  brandSoft: ColorPair(light: Color(0x0F0F172A), dark: Color(0x1AF5F5F7)),
  accent: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  accentHover: ColorPair(light: Color(0xFF0891B2), dark: Color(0xFF06B6D4)),
  accentSoft: ColorPair(light: Color(0x1F06B6D4), dark: Color(0x3322D3EE)),
  modeChat: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  modeAgent: ColorPair(light: Color(0xFF0F172A), dark: Color(0xFFF5F5F7)),
  modeTask: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  brandGradientLight: [Color(0xFF1E293B), Color(0xFF0F172A), Color(0xFF06B6D4)],
  brandGradientDark: [Color(0xFFF5F5F7), Color(0xFFA1A1AA), Color(0xFF22D3EE)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF1E293B), Color(0xFF0F172A), Color(0xFF06B6D4)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFF334155), Color(0xFF0F172A), Color(0xFF22D3EE)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 12. 墨蓝 + 信号橙(swap — Vercel 风极简) ─────────────────────
const PaletteSpec _inkblueOrange = PaletteSpec(
  id: PaletteId.inkblueOrange,
  displayNameKey: 'paletteName_inkblueOrange',
  descriptionKey: 'paletteDesc_inkblueOrange',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF1A1F4A), dark: Color(0xFFFF6B35)),
  brandHover: ColorPair(light: Color(0xFF0A0E27), dark: Color(0xFFFF8A5C)),
  brandSoft: ColorPair(light: Color(0x1AFF6B35), dark: Color(0x2EFF6B35)),
  accent: ColorPair(light: Color(0xFFFF6B35), dark: Color(0xFFFF8A5C)),
  accentHover: ColorPair(light: Color(0xFFE85B2C), dark: Color(0xFFFF6B35)),
  accentSoft: ColorPair(light: Color(0x1FFF6B35), dark: Color(0x33FF8A5C)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF1A1F4A), dark: Color(0xFFFF6B35)),
  modeTask: ColorPair(light: Color(0xFFFF6B35), dark: Color(0xFFFF8A5C)),
  brandGradientLight: [Color(0xFFFF6B35), Color(0xFF1A1F4A), Color(0xFF0A0E27)],
  brandGradientDark: [Color(0xFFFF8A5C), Color(0xFF1A1F4A), Color(0xFF0A0E27)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFFF6B35), Color(0xFF1A1F4A), Color(0xFF0A0E27)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFFF8A5C), Color(0xFF1A1F4A), Color(0xFF0A0E27)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 13. 量子紫 + 钛金(swap — Vision Pro 风) ────────────────────
const PaletteSpec _quantumTitanium = PaletteSpec(
  id: PaletteId.quantumTitanium,
  displayNameKey: 'paletteName_quantumTitanium',
  descriptionKey: 'paletteDesc_quantumTitanium',
  brand: ColorPair(light: Color(0xFF5B4B8A), dark: Color(0xFFD4C5A8)),
  brandHover: ColorPair(light: Color(0xFF3D3163), dark: Color(0xFFE0D2B8)),
  brandSoft: ColorPair(light: Color(0x2ED4C5A8), dark: Color(0x38D4C5A8)),
  accent: ColorPair(light: Color(0xFFD4C5A8), dark: Color(0xFFE0D2B8)),
  accentHover: ColorPair(light: Color(0xFFB6A788), dark: Color(0xFFD4C5A8)),
  accentSoft: ColorPair(light: Color(0x33D4C5A8), dark: Color(0x38E0D2B8)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF5B4B8A), dark: Color(0xFFD4C5A8)),
  modeTask: ColorPair(light: Color(0xFFD4C5A8), dark: Color(0xFFE0D2B8)),
  brandGradientLight: [Color(0xFFD4C5A8), Color(0xFF7B6BA8), Color(0xFF2D2547)],
  brandGradientDark: [Color(0xFFE0D2B8), Color(0xFF8B7BB8), Color(0xFF3D3260)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFD4C5A8), Color(0xFF7B6BA8), Color(0xFF2D2547)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFE0D2B8), Color(0xFF8B7BB8), Color(0xFF3D3260)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 14. Claude 暖(swap — 反共识温暖) ────────────────────────────
const PaletteSpec _claudeWarm = PaletteSpec(
  id: PaletteId.claudeWarm,
  displayNameKey: 'paletteName_claudeWarm',
  descriptionKey: 'paletteDesc_claudeWarm',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF7B4B2A), dark: Color(0xFFFAE5C7)),
  brandHover: ColorPair(light: Color(0xFF5A3620), dark: Color(0xFFFFF1D6)),
  brandSoft: ColorPair(light: Color(0x1F5B7CFF), dark: Color(0x335B7CFF)),
  accent: ColorPair(light: Color(0xFF5B7CFF), dark: Color(0xFF818CF8)),
  accentHover: ColorPair(light: Color(0xFF4A6BE0), dark: Color(0xFF5B7CFF)),
  accentSoft: ColorPair(light: Color(0x1F5B7CFF), dark: Color(0x33818CF8)),
  modeChat: ColorPair(light: Color(0xFF5B7CFF), dark: Color(0xFF818CF8)),
  modeAgent: ColorPair(light: Color(0xFF7B4B2A), dark: Color(0xFFFAE5C7)),
  modeTask: ColorPair(light: Color(0xFFC89A6B), dark: Color(0xFFFAE5C7)),
  brandGradientLight: [Color(0xFFFAE5C7), Color(0xFFC89A6B), Color(0xFF5A3620)],
  brandGradientDark: [Color(0xFFFAE5C7), Color(0xFFC89A6B), Color(0xFF7B4B2A)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFFAE5C7), Color(0xFFC89A6B), Color(0xFF5A3620)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFFAE5C7), Color(0xFFC89A6B), Color(0xFF7B4B2A)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 15. 石墨 + 量子青(swap — Cursor 风) ─────────────────────────
const PaletteSpec _graphiteCyan = PaletteSpec(
  id: PaletteId.graphiteCyan,
  displayNameKey: 'paletteName_graphiteCyan',
  descriptionKey: 'paletteDesc_graphiteCyan',
  brand: ColorPair(light: Color(0xFF1F2937), dark: Color(0xFF06B6D4)),
  brandHover: ColorPair(light: Color(0xFF0F172A), dark: Color(0xFF22D3EE)),
  brandSoft: ColorPair(light: Color(0x1F06B6D4), dark: Color(0x2E06B6D4)),
  accent: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  accentHover: ColorPair(light: Color(0xFF0891B2), dark: Color(0xFF06B6D4)),
  accentSoft: ColorPair(light: Color(0x1F06B6D4), dark: Color(0x3322D3EE)),
  modeChat: ColorPair(light: Color(0xFF06B6D4), dark: Color(0xFF22D3EE)),
  modeAgent: ColorPair(light: Color(0xFF1F2937), dark: Color(0xFF06B6D4)),
  modeTask: ColorPair(light: Color(0xFFF59E0B), dark: Color(0xFFFCD34D)),
  brandGradientLight: [Color(0xFF06B6D4), Color(0xFF0E7490), Color(0xFF0F172A)],
  brandGradientDark: [Color(0xFF22D3EE), Color(0xFF06B6D4), Color(0xFF1F2937)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF06B6D4), Color(0xFF0E7490), Color(0xFF0F172A)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFF22D3EE), Color(0xFF06B6D4), Color(0xFF1F2937)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 16. 靛青 + 暖砂(swap — 2026 趋势) ──────────────────────────
const PaletteSpec _indigoSand = PaletteSpec(
  id: PaletteId.indigoSand,
  displayNameKey: 'paletteName_indigoSand',
  descriptionKey: 'paletteDesc_indigoSand',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF3730A3), dark: Color(0xFFA5A4F4)),
  brandHover: ColorPair(light: Color(0xFF312E81), dark: Color(0xFFC7B7F8)),
  brandSoft: ColorPair(light: Color(0x2EFED7AA), dark: Color(0x33FED7AA)),
  accent: ColorPair(light: Color(0xFFFB923C), dark: Color(0xFFFED7AA)),
  accentHover: ColorPair(light: Color(0xFFF97316), dark: Color(0xFFFB923C)),
  accentSoft: ColorPair(light: Color(0x1FFB923C), dark: Color(0x33FED7AA)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF3730A3), dark: Color(0xFFA5A4F4)),
  modeTask: ColorPair(light: Color(0xFFFB923C), dark: Color(0xFFFED7AA)),
  brandGradientLight: [Color(0xFFFED7AA), Color(0xFF6366F1), Color(0xFF1E1B4B)],
  brandGradientDark: [Color(0xFFFED7AA), Color(0xFFA5A4F4), Color(0xFF3730A3)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFFFED7AA), Color(0xFF6366F1), Color(0xFF1E1B4B)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    gradient: [Color(0xFFFED7AA), Color(0xFFA5A4F4), Color(0xFF3730A3)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ── 17. Wiki 翠绿 — 旧 Wiki 的 emerald 主色 + 沙金 accent ──────────
//
// 老 wiki UI 用 #10B981 emerald-500 当主色 — 跟 BiuTokens.green = SemanticTokens.success
// 同源。本色板把这种 wiki 视觉抽出来,让喜欢绿调的用户能整站统一。
//
// 注:light brand 用 emerald-600 (#059669) 而非 emerald-500 — emerald-500 对白色
// surface 对比度仅 2.54:1 不到 AA Large 3:1 阈值。emeraldGold 也用同样修正。
//
// vs emeraldGold:emeraldGold 是"翡翠金"(emerald + 金黄),accent 是橙色 #F59E0B,
// 整体偏奢华;wikiGreen 改用更克制的"沙金 #D4A656",更接近笔记/wiki 阅读感。
const PaletteSpec _wikiGreen = PaletteSpec(
  id: PaletteId.wikiGreen,
  displayNameKey: 'paletteName_wikiGreen',
  descriptionKey: 'paletteDesc_wikiGreen',
  isFeatured: true,
  brand: ColorPair(light: Color(0xFF059669), dark: Color(0xFF34D399)),
  brandHover: ColorPair(light: Color(0xFF047857), dark: Color(0xFF10B981)),
  brandSoft: ColorPair(light: Color(0x1F10B981), dark: Color(0x2E10B981)),
  accent: ColorPair(light: Color(0xFFD4A656), dark: Color(0xFFE5C896)),
  accentHover: ColorPair(light: Color(0xFFB8924A), dark: Color(0xFFD4A656)),
  accentSoft: ColorPair(light: Color(0x1FD4A656), dark: Color(0x33E5C896)),
  modeChat: _kModeChatBlue,
  modeAgent: ColorPair(light: Color(0xFF059669), dark: Color(0xFF34D399)),
  modeTask: ColorPair(light: Color(0xFFD4A656), dark: Color(0xFFE5C896)),
  brandGradientLight: [Color(0xFF34D399), Color(0xFF059669), Color(0xFF065F46)],
  brandGradientDark: [Color(0xFF6EE7B7), Color(0xFF34D399), Color(0xFF10B981)],
  bannerLight: BannerSpec(
    gradient: [Color(0xFF34D399), Color(0xFF059669), Color(0xFF065F46)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
  bannerDark: BannerSpec(
    // dark 锚一深 emerald-900 让白字 banner 文字 AA Large 可读
    gradient: [Color(0xFF6EE7B7), Color(0xFF34D399), Color(0xFF064E3B)],
    fg: _bFg, fgDim: _bFgDim, scrim: _bScrim,
    ctaBg: _bCtaBg, ctaFg: _bCtaFg, ctaBorder: _bCtaBorder,
  ),
);

// ═════════════════════════════════════════════════════════════════════════
// 主索引 + 入口
// ═════════════════════════════════════════════════════════════════════════

/// 18 色板索引 (顺序 = 设置页展示顺序 — 默认在前,推荐紧随,然后按色系归组)。
const Map<PaletteId, PaletteSpec> _palettes = {
  // 默认 + 推荐 (5 个 isFeatured)
  PaletteId.purpleOrange: _purpleOrange,
  PaletteId.inkblueOrange: _inkblueOrange,
  PaletteId.aurora: _aurora,
  PaletteId.claudeWarm: _claudeWarm,
  PaletteId.indigoSand: _indigoSand,
  PaletteId.wikiGreen: _wikiGreen,
  // 紫系
  PaletteId.purple: _purple,
  PaletteId.purpleBlue: _purpleBlue,
  PaletteId.purplePink: _purplePink,
  PaletteId.purpleEmerald: _purpleEmerald,
  // 暖色系
  PaletteId.sunset: _sunset,
  PaletteId.rose: _rose,
  // 冷色系
  PaletteId.cyber: _cyber,
  PaletteId.ocean: _ocean,
  // 极简 / 工程师
  PaletteId.onyx: _onyx,
  PaletteId.graphiteCyan: _graphiteCyan,
  PaletteId.quantumTitanium: _quantumTitanium,
  // 平衡稀有
  PaletteId.emeraldGold: _emeraldGold,
};

/// 取色板规格。未实现的色板自动 fallback 到默认 inkblueOrange (跟 AppSettings 默认一致)。
PaletteSpec paletteSpecOf(PaletteId id) =>
    _palettes[id] ?? _inkblueOrange;

/// 全部已实现色板,顺序 = 设置页展示顺序。
List<PaletteSpec> get availablePalettes =>
    _palettes.values.toList(growable: false);

/// 推荐色板(在设置页置顶展示)。
List<PaletteSpec> get featuredPalettes =>
    _palettes.values.where((p) => p.isFeatured).toList(growable: false);
