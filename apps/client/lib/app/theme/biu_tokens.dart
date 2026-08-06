// BiuTokens — 兼容 shim,让 ~129 个老调用点继续工作不立刻 break。
//
// **新代码禁止用 BiuTokens** — 走 `Theme.of(context).extension<BiuColors>()!`
// 与 `Theme.of(context).extension<BiuMetrics>()!`。
//
// 内部状态:
//   * `_isDark`         — 由 main.dart::BiuMindApp.builder 在每次 build 同步
//   * `palette`         — 由 buildTheme() 调用前设置;读色全走当前色板
//
// 所有 getter 标 @Deprecated,但保留运行时正确 — 老代码颜色会跟当前色板同步切。
// 若用户切到非紫色板 (Phase 3 后),`BiuTokens.purple` 实际返回当前 brand。
//
// 迁移路径: 每改一个文件就把 `BiuTokens.x` 换成 `c.x`(c = ext<BiuColors>),
// PR review 把关老代码 → 新代码的迁移。

import 'package:flutter/material.dart';

import 'palettes.dart';
import 'tokens.dart';

class BiuTokens {
  BiuTokens._();

  // ─── 全局状态 (theme builder 同步) ────────────────────────────
  static bool _isDark = false;

  /// 由 main.dart 在每次 build 前同步 (跟 brightness 一起)。
  static PaletteId palette = PaletteId.inkblueOrange;

  /// 顶层 ThemeBinding 改这个; widget 重 build 自动用新值。
  static set brightness(Brightness b) {
    _isDark = b == Brightness.dark;
  }

  static Brightness get brightness =>
      _isDark ? Brightness.dark : Brightness.light;

  // ── 内部 helper ──────────────────────────────────────────────
  static PaletteSpec get _spec => paletteSpecOf(palette);
  static NeutralTokens get _n =>
      NeutralTokens.forBrightness(_isDark ? Brightness.dark : Brightness.light);
  static Brightness get _b => _isDark ? Brightness.dark : Brightness.light;

  // ─────────────────────────────────────────────────────────────
  // Brand — 现在跟当前色板走 (老名字保留, "purple" 是品牌占位 — 即使色板
  // 切到 onyx, BiuTokens.purple 仍返当前 brand 色)。
  // ─────────────────────────────────────────────────────────────

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.brand')
  static Color get purple => _spec.brand.forBrightness(_b);

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.brandSoft')
  static Color get purpleSoft => _spec.brandSoft.forBrightness(_b);

  /// 老 `purpleLight` 映射到 brandSoft (大多数老调用是当 hover/selected 背景)。
  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.brandSoft')
  static Color get purpleLight => _spec.brandSoft.forBrightness(_b);

  /// 老 `green` 映射到 semantic success — 品牌色不该承载语义。
  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.success')
  static const Color green = SemanticTokens.success;

  // ─────────────────────────────────────────────────────────────
  // Neutrals — 仍按 light / dark 切,但走新中性色表 (§2.4)
  // ─────────────────────────────────────────────────────────────

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.bgApp')
  static Color get bg => _n.bgApp;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.surface0')
  static Color get surface => _n.surface0;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.surface2')
  static Color get surfaceMuted => _n.surface2;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.borderSoft')
  static Color get border => _n.borderSoft;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.borderHairline')
  static Color get borderSubtle => _n.borderHairline;

  // ─────────────────────────────────────────────────────────────
  // Text
  // ─────────────────────────────────────────────────────────────

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.text1')
  static Color get text => _n.text1;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.text2')
  static Color get textSecondary => _n.text2;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.textMuted')
  static Color get textMuted => _n.textMuted;

  /// 老 textDisabled — 原值跟 textMuted 接近 — 复用 textMuted。
  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.textMuted')
  static Color get textDisabled => _n.textMuted;

  // ─────────────────────────────────────────────────────────────
  // Semantic
  // ─────────────────────────────────────────────────────────────

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.error')
  static const Color error = SemanticTokens.error;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.errorSoft')
  static Color get errorSoft =>
      _isDark ? SemanticTokens.errorSoftD : SemanticTokens.errorSoftL;

  @Deprecated('Use Theme.of(context).extension<BiuColors>()!.success')
  static const Color success = SemanticTokens.success;

  // ─────────────────────────────────────────────────────────────
  // Radius — 跟主题无关;但 §3.1 整体收紧 2-4px (含语义性 break)
  //   旧: radiusMd=12, radiusLg=16, radiusXl=20
  //   新: radiusMd=10, radiusLg=14, radiusXl=16
  // ─────────────────────────────────────────────────────────────

  static const double radiusXs = RadiusTokens.xs;
  static const double radiusSm = RadiusTokens.sm;
  static const double radiusMd = RadiusTokens.md;
  static const double radiusLg = RadiusTokens.lg;
  static const double radiusXl = RadiusTokens.xl;
  static const double radiusFull = RadiusTokens.full;

  // ─────────────────────────────────────────────────────────────
  // Spacing — 4px-grid,完全静态
  // ─────────────────────────────────────────────────────────────

  static const double space1 = SpacingTokens.s1;
  static const double space2 = SpacingTokens.s2;
  static const double space3 = SpacingTokens.s3;
  static const double space4 = SpacingTokens.s4;
  static const double space5 = SpacingTokens.s5;
  static const double space6 = SpacingTokens.s6;
  static const double space8 = SpacingTokens.s8;

  // ─────────────────────────────────────────────────────────────
  // Sidebar — 老兼容值 (medium 档)。新代码用 BiuMetrics.navRailWidth。
  // ─────────────────────────────────────────────────────────────

  @Deprecated('Use Theme.of(context).extension<BiuMetrics>()!.navRailWidth')
  static const double sidebarWidth = 232.0;

  static const double sidebarItemHeight = 32.0;
}
