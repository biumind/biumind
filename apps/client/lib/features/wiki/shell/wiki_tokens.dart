// Wiki 模块内部尺寸常量。
//
// 颜色统一走 biumind 的 BiuTokens（见 app/theme.dart），不再引入
// knowcode 原 KColors。这里只保留 layout 维度上和 knowcode AppShell
// 一致的尺寸（sidebar 宽 220 / status 高 32 等），让"知识库"内部的
// 视觉密度跟 knowcode 截图一致，而 biumind 顶层 _AppShell 用 232px
// sidebar 不变。

class WikiTokens {
  const WikiTokens._();

  static const double sidebarWidth = 220;
  static const double statusBarHeight = 32;

  static const double space1 = 4;
  static const double space2 = 8;
  static const double space3 = 12;
  static const double space4 = 16;
  static const double space5 = 24;

  static const double radiusButton = 8;
  static const double radiusCard = 14;

  static const double fontXs = 11;
  static const double fontSm = 12;
  static const double fontMd = 13;
  static const double fontLg = 14;

  static const Duration fast = Duration(milliseconds: 120);
}
