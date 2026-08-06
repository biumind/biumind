// Theme primitive tokens — palette-independent, font-size-independent.
//
// 这层放跟主题色 / 字号无关、跟设计系统永远绑定的常量:
//   * 中性色 (surface / text / border)            — 仅按 light / dark 切
//   * Radius / Shadow / Motion / 4px-grid spacing — 完全静态
//
// 主题色相关 token (brand / accent / banner / mode-color) 见 palettes.dart;
// 字号 / 列表宽度 / 列表 padding / avatar 尺寸 见 font_size.dart。
//
// 改这里前先看 docs/BiuMind-Theme-System-Design.md §2.4 / §3.1 / §3.2 / §3.3。
// 新增数值禁止硬编码 hex 或魔法数 — 必须在这里命名后引用。

import 'package:flutter/material.dart';

// ─────────────────────────────────────────────────────────────────────────
// 中性色 (palette-independent, mode-dependent) — 见 §2.4
// ─────────────────────────────────────────────────────────────────────────

class NeutralTokens {
  const NeutralTokens({
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
  });

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

  static const NeutralTokens light = NeutralTokens(
    bgApp:          Color(0xFFFAFAFC),
    surface0:       Color(0xFFFFFFFF),
    surface1:       Color(0xFFFAFAFC),
    surface2:       Color(0xFFF4F4F8),
    surface3:       Color(0xFFECECF1),
    text1:          Color(0xFF0A0A0F),
    text2:          Color(0xFF4A4A57),
    text3:          Color(0xFF6E6E80),
    textMuted:      Color(0xFF9A9AA8),
    borderHairline: Color(0x0F0A0A0F), // rgba(10,10,15,0.06)
    borderSoft:     Color(0x1A0A0A0F), // rgba(10,10,15,0.10)
    borderStrong:   Color(0x2E0A0A0F), // rgba(10,10,15,0.18)
  );

  static const NeutralTokens dark = NeutralTokens(
    bgApp:          Color(0xFF0A0A0F),
    surface0:       Color(0xFF0F0F14),
    surface1:       Color(0xFF16161D),
    surface2:       Color(0xFF1D1D26),
    surface3:       Color(0xFF252531),
    text1:          Color(0xFFF5F5F7),
    text2:          Color(0xFFC7C7D1),
    text3:          Color(0xFF9A9AA8),
    textMuted:      Color(0xFF6E6E80),
    borderHairline: Color(0x0FFFFFFF), // rgba(255,255,255,0.06)
    borderSoft:     Color(0x1AFFFFFF), // rgba(255,255,255,0.10)
    borderStrong:   Color(0x33FFFFFF), // rgba(255,255,255,0.20)
  );

  static NeutralTokens forBrightness(Brightness b) =>
      b == Brightness.dark ? dark : light;
}

// ─────────────────────────────────────────────────────────────────────────
// Semantic 静态色 (跟主题无关 — 表达"成功/失败"语义,品牌色不该承载这些)
// ─────────────────────────────────────────────────────────────────────────

class SemanticTokens {
  const SemanticTokens._();

  static const Color success     = Color(0xFF10B981);
  static const Color successSoft = Color(0xFFD1FAE5);
  static const Color warning     = Color(0xFFF59E0B);
  static const Color warningSoft = Color(0xFFFEF3C7);
  static const Color error       = Color(0xFFDC2626);
  static const Color errorSoftL  = Color(0xFFFEE2E2);
  static const Color errorSoftD  = Color(0xFF3F1818);
  static const Color info        = Color(0xFF3B82F6);
  static const Color infoSoft    = Color(0xFFDBEAFE);
}

// ─────────────────────────────────────────────────────────────────────────
// Radius — 见 §3.1 (比 UI-Design-System.md 旧值收紧 2-4px)
// ─────────────────────────────────────────────────────────────────────────

class RadiusTokens {
  const RadiusTokens._();

  static const double xs   = 6.0;     // 极小元素 (badge inline / kbd-key)
  static const double sm   = 8.0;     // 徽章 / chip / 小标签
  static const double md   = 10.0;    // 输入框 / 按钮 / nav item
  static const double lg   = 14.0;    // 卡片 / 对话框 / tile
  static const double xl   = 16.0;    // 主容器 / 弹层菜单
  static const double full = 999.0;   // 头像 / pill 按钮
}

// ─────────────────────────────────────────────────────────────────────────
// Spacing — 4px 阶,palette-independent。
// 注: tilePad / cardPad / gapGrid 等"随字号联动"的间距在 font_size.dart。
// ─────────────────────────────────────────────────────────────────────────

class SpacingTokens {
  const SpacingTokens._();

  static const double s1 = 4.0;
  static const double s2 = 8.0;
  static const double s3 = 12.0;
  static const double s4 = 16.0;
  static const double s5 = 24.0;
  static const double s6 = 32.0;
  static const double s7 = 40.0;
  static const double s8 = 48.0;
}

// ─────────────────────────────────────────────────────────────────────────
// Shadow — 见 §3.2,light / dark alpha 不同
// ─────────────────────────────────────────────────────────────────────────

class ShadowTokens {
  const ShadowTokens({required this.sm, required this.md, required this.lg, required this.xl});

  final List<BoxShadow> sm;
  final List<BoxShadow> md;
  final List<BoxShadow> lg;
  final List<BoxShadow> xl;

  static final ShadowTokens light = ShadowTokens(
    sm: [
      BoxShadow(color: const Color(0x0A0A0A0F), offset: const Offset(0, 1), blurRadius: 2),
    ],
    md: [
      BoxShadow(color: const Color(0x0F0A0A0F), offset: const Offset(0, 4), blurRadius: 12),
    ],
    lg: [
      BoxShadow(color: const Color(0x140A0A0F), offset: const Offset(0, 12), blurRadius: 32),
      BoxShadow(color: const Color(0x0A0A0A0F), offset: const Offset(0, 2), blurRadius: 6),
    ],
    xl: [
      BoxShadow(color: const Color(0x1A0A0A0F), offset: const Offset(0, 24), blurRadius: 60),
      BoxShadow(color: const Color(0x0F0A0A0F), offset: const Offset(0, 6), blurRadius: 16),
    ],
  );

  // dark 翻倍 alpha — saturated 黑色背景上小投影才看得见
  static final ShadowTokens dark = ShadowTokens(
    sm: [
      BoxShadow(color: const Color(0x14000000), offset: const Offset(0, 1), blurRadius: 2),
    ],
    md: [
      BoxShadow(color: const Color(0x1F000000), offset: const Offset(0, 4), blurRadius: 12),
    ],
    lg: [
      BoxShadow(color: const Color(0x29000000), offset: const Offset(0, 12), blurRadius: 32),
      BoxShadow(color: const Color(0x14000000), offset: const Offset(0, 2), blurRadius: 6),
    ],
    xl: [
      BoxShadow(color: const Color(0x33000000), offset: const Offset(0, 24), blurRadius: 60),
      BoxShadow(color: const Color(0x1F000000), offset: const Offset(0, 6), blurRadius: 16),
    ],
  );

  static ShadowTokens forBrightness(Brightness b) =>
      b == Brightness.dark ? dark : light;
}

// ─────────────────────────────────────────────────────────────────────────
// Motion — 见 §3.3
// ─────────────────────────────────────────────────────────────────────────

class MotionTokens {
  const MotionTokens._();

  static const Duration fast    = Duration(milliseconds: 120);
  static const Duration normal  = Duration(milliseconds: 200);
  static const Duration slow    = Duration(milliseconds: 320);

  static const Curve standard = Curves.easeOutCubic;
  static const Curve bounce   = Curves.easeOutBack;
}
