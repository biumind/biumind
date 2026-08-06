// BiuMind Theme System — 公共入口。
//
// **加新模块时怎么用主题**
//
// 1. 颜色 — 不要硬编码 hex。
//      final c = Theme.of(context).extension<BiuColors>()!;
//      Container(color: c.brand);            // 主品牌色 (随色板切换)
//      Container(color: c.surface0);          // 卡片背景
//      Text('hi', style: TextStyle(color: c.text1));
//      // mode-tag dot:
//      Container(color: c.modeColor(ChatMode.task));
//      // 渐变 banner:
//      Container(decoration: BoxDecoration(
//        gradient: LinearGradient(colors: c.bannerGradient),
//      ));
//
// 2. 字号 / 间距 / 列表宽度 — 不要硬编码 magic number。
//      final m = Theme.of(context).extension<BiuMetrics>()!;
//      Container(width: m.threadListWidth, padding: EdgeInsets.all(m.cardPad));
//      Text('Hi', style: TextStyle(fontSize: m.fontHero));
//
// 3. 动画时长 / 曲线 — 走 BiuMotion。
//      final mo = Theme.of(context).extension<BiuMotion>()!;
//      AnimatedContainer(duration: mo.normal, curve: mo.standard, …);
//
// 4. Radius — 静态常量 (跟主题 / 字号无关)。
//      borderRadius: BorderRadius.circular(RadiusTokens.md);
//
// 5. Spacing — 4px 阶静态常量。
//      SizedBox(height: SpacingTokens.s4);
//
// **加新色板** — 见 palettes.dart 顶部说明。
// **加新 token 字段** — 见 extensions.dart 顶部说明。
// **改默认色板 / 默认字号** — 改 theme_builder 调用方 (main.dart) 即可。
//
// **不变量** (见 docs/BiuMind-Theme-System-Design.md §7):
//   T1 — 业务代码禁止 Color(0xFF...) 硬编码 (palettes.dart 例外)
//   T2 — 新代码禁止 import biu_tokens.dart
//   T3 — banner 文字色必须用 c.bannerFg,不可用 c.text1
//   T6 — 切主题 / 字号 / 模式必须立刻生效,不可要求 restart
//   T8 — popup menu 位置走 lib/core/ui/popup_position.dart

export 'brand.dart';
export 'category_colors.dart';
export 'effects.dart';
export 'extensions.dart';
export 'font_size.dart';
export 'palettes.dart';
export 'theme_builder.dart';
export 'tokens.dart';

// 兼容 shim 单独 export — 老代码逐步迁移。新代码不要直接 import。
export 'biu_tokens.dart';
