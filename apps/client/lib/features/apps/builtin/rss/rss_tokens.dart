// RSS-local 真黑阅读色 (M10.2).
//
// 全局 BiuTokens 的 dark 中性色是带蓝灰的 surface (#0E0E14 一类), 适合
// 通用 UI; 但 RSS 阅读器在 OLED 屏上 "真黑 #000000" 体验更好 (省电 +
// 沉浸). 这里只给 reader/today 用, 不动全局色板.
//
// 跟随 Theme.brightness: light 模式回退到普通 surface, dark 模式才切真黑.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

class RssReaderColors {
  RssReaderColors._();

  /// 阅读区背景. dark = 纯黑 #000000 (OLED 真黑); light = 普通 bg.
  static Color bg(BuildContext context) {
    return Theme.of(context).brightness == Brightness.dark
        ? const Color(0xFF000000)
        : BiuTokens.bg;
  }

  /// 阅读区卡片 / 次级表面. dark = 近黑 #0A0A0F (跟纯黑背景拉出微弱层次);
  /// light = 普通 surface.
  static Color surface(BuildContext context) {
    return Theme.of(context).brightness == Brightness.dark
        ? const Color(0xFF0A0A0F)
        : BiuTokens.surface;
  }

  /// 正文文字. dark = 纯白 #FFFFFF on 纯黑 (最高对比, WCAG AAA);
  /// light = 普通 text.
  static Color text(BuildContext context) {
    return Theme.of(context).brightness == Brightness.dark
        ? const Color(0xFFFFFFFF)
        : BiuTokens.text;
  }
}
