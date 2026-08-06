// buildTheme — 整套主题系统的唯一构造入口。
//
//   ThemeData buildTheme({
//     required PaletteId palette,    // 用户在 设置 → 外观 选的色板
//     required Brightness mode,      // light / dark (system 解析后传具体值)
//     required FontSize fontSize,    // small (默认) / medium / large
//   })
//
// 在 main.dart::BiuMindApp.build 调用,light + dark 各调一次。
// 其他模块禁止直接调 — 走 Theme.of(context) 即可。
//
// 加新 token: 见 extensions.dart 顶部说明。
// 加新色板: 见 palettes.dart 顶部说明。

import 'package:flutter/material.dart';

import 'extensions.dart';
import 'font_size.dart';
import 'palettes.dart';
import 'tokens.dart';

ThemeData buildTheme({
  required PaletteId palette,
  required Brightness mode,
  required FontSize fontSize,
}) {
  final spec = paletteSpecOf(palette);
  final neutral = NeutralTokens.forBrightness(mode);
  final fst = FontSizeTokens.of(fontSize);
  final biuColors = BiuColors.fromPalette(spec, mode);

  final brand = spec.brand.forBrightness(mode);
  final accent = spec.accent.forBrightness(mode);
  final brandSoft = spec.brandSoft.forBrightness(mode);
  final isDark = mode == Brightness.dark;

  final colorScheme = ColorScheme(
    brightness: mode,
    primary: brand,
    onPrimary: Colors.white,
    primaryContainer: brandSoft,
    onPrimaryContainer: brand,
    secondary: accent,
    onSecondary: Colors.white,
    secondaryContainer: spec.accentSoft.forBrightness(mode),
    onSecondaryContainer: accent,
    tertiary: SemanticTokens.success,
    onTertiary: Colors.white,
    tertiaryContainer:
        isDark ? const Color(0xFF064E3B) : SemanticTokens.successSoft,
    onTertiaryContainer:
        isDark ? const Color(0xFFD1FAE5) : const Color(0xFF064E3B),
    error: SemanticTokens.error,
    onError: Colors.white,
    errorContainer:
        isDark ? SemanticTokens.errorSoftD : SemanticTokens.errorSoftL,
    onErrorContainer:
        isDark ? const Color(0xFFFCA5A5) : const Color(0xFF7F1D1D),
    surface: neutral.bgApp,
    onSurface: neutral.text1,
    surfaceContainerLowest: neutral.surface0,
    surfaceContainerLow: neutral.surface1,
    surfaceContainer: neutral.surface2,
    surfaceContainerHigh: neutral.surface2,
    surfaceContainerHighest: neutral.surface3,
    onSurfaceVariant: neutral.text2,
    outline: neutral.borderSoft,
    outlineVariant: neutral.borderHairline,
    shadow: const Color(0x1A000000),
    scrim: const Color(0x66000000),
    inverseSurface: neutral.text1,
    onInverseSurface: neutral.surface0,
    inversePrimary: brandSoft,
  );

  final textTheme = _buildTextTheme(neutral, fst);

  return ThemeData(
    useMaterial3: true,
    brightness: mode,
    colorScheme: colorScheme,
    scaffoldBackgroundColor: neutral.bgApp,
    visualDensity: VisualDensity.adaptivePlatformDensity,
    // 桌面平台禁用 page 转场。tabPage/subPage 的 NoTransitionPage 只罩叶子页,
    // 嵌套 ShellRoute(WikiShell/CreationShell) 挂载时走平台默认 → macOS 是
    // CupertinoPageTransitionsBuilder 横向滑入, 进入 /wiki /creation 整面板
    // 滑入, 违背「tab 切换应当即时」(router.dart §3.2)。桌面三平台一律 no-op。
    // 移动端不列出 = 保留平台默认: iOS Cupertino(右滑返回) / android Zoom
    // (返回栈感知), subPage 的 MaterialPage 依赖它们。显式列默认会踩
    // CupertinoPageTransitionsBuilder 在 Flutter 版本间搬库 (material ↔
    // cupertino) 的坑, 本地 3.41 与 CI 3.44 编译目标不同会直接挂。
    pageTransitionsTheme: const PageTransitionsTheme(
      builders: <TargetPlatform, PageTransitionsBuilder>{
        TargetPlatform.macOS: _NoPageTransitionsBuilder(),
        TargetPlatform.windows: _NoPageTransitionsBuilder(),
        TargetPlatform.linux: _NoPageTransitionsBuilder(),
      },
    ),
    textTheme: textTheme,
    splashFactory: NoSplash.splashFactory,
    splashColor: Colors.transparent,
    highlightColor: Colors.transparent,
    hoverColor: neutral.surface2,
    extensions: <ThemeExtension<dynamic>>[
      biuColors,
      BiuMetrics(tokens: fst),
      BiuMotion.defaults,
    ],
    cardTheme: const CardThemeData(
      elevation: 0,
      margin: EdgeInsets.zero,
      clipBehavior: Clip.antiAlias,
    ),
    dividerTheme: DividerThemeData(
      color: neutral.borderSoft,
      thickness: 1,
      space: 1,
    ),
    appBarTheme: AppBarTheme(
      elevation: 0,
      scrolledUnderElevation: 0,
      backgroundColor: neutral.bgApp,
      surfaceTintColor: Colors.transparent,
      centerTitle: true,
      titleTextStyle: TextStyle(
        color: neutral.text1,
        fontSize: fst.fontH2,
        fontWeight: FontWeight.w600,
      ),
    ),
    iconTheme: IconThemeData(color: neutral.text2, size: 20),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: neutral.surface0,
      contentPadding: EdgeInsets.symmetric(
        horizontal: fst.cardPad,
        vertical: fst.tilePadV + 4,
      ),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        borderSide: BorderSide(color: neutral.borderSoft),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        borderSide: BorderSide(color: neutral.borderSoft),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        borderSide: BorderSide(color: brand, width: 1.5),
      ),
      hintStyle: TextStyle(color: neutral.textMuted, fontSize: fst.fontBase),
      labelStyle: TextStyle(color: neutral.text2, fontSize: fst.fontBase),
      prefixIconColor: neutral.textMuted,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: brand,
        foregroundColor: Colors.white,
        textStyle: TextStyle(fontSize: fst.fontBase, fontWeight: FontWeight.w500),
        padding: EdgeInsets.symmetric(
          horizontal: fst.cardPad,
          vertical: fst.tilePadV + 4,
        ),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(RadiusTokens.md),
        ),
        elevation: 0,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: neutral.text1,
        textStyle: TextStyle(fontSize: fst.fontBase, fontWeight: FontWeight.w500),
        padding: EdgeInsets.symmetric(
          horizontal: fst.cardPad,
          vertical: fst.tilePadV + 4,
        ),
        side: BorderSide(color: neutral.borderSoft),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(RadiusTokens.md),
        ),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: neutral.text2,
        textStyle: TextStyle(fontSize: fst.fontBase, fontWeight: FontWeight.w500),
      ),
    ),
    switchTheme: SwitchThemeData(
      thumbColor: WidgetStateProperty.resolveWith((_) => Colors.white),
      trackColor: WidgetStateProperty.resolveWith((states) =>
          states.contains(WidgetState.selected)
              ? SemanticTokens.success
              : (isDark
                  ? const Color(0xFF52525B)
                  : const Color(0xFFEEEEEE))),
      trackOutlineColor: WidgetStateProperty.all(Colors.transparent),
    ),
    chipTheme: ChipThemeData(
      backgroundColor: neutral.surface2,
      labelStyle: TextStyle(color: neutral.text1, fontSize: fst.fontSm),
      side: BorderSide(color: neutral.borderHairline),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(RadiusTokens.sm),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
    ),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(
        color: neutral.text1.withValues(alpha: 0.9),
        borderRadius: BorderRadius.circular(RadiusTokens.sm),
        boxShadow: ShadowTokens.forBrightness(mode).sm,
      ),
      textStyle: TextStyle(color: neutral.surface0, fontSize: fst.fontSm),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      // 进场 fade 200ms (Material 默认 0,跟 prototype `transition: opacity 160ms` 对齐)
      waitDuration: const Duration(milliseconds: 400),
      showDuration: const Duration(seconds: 4),
    ),
    tabBarTheme: TabBarThemeData(
      indicator: UnderlineTabIndicator(
        borderSide: BorderSide(color: brand, width: 2),
        insets: const EdgeInsets.symmetric(horizontal: 4),
      ),
      indicatorSize: TabBarIndicatorSize.label,
      labelColor: brand,
      unselectedLabelColor: neutral.text2,
      labelStyle: TextStyle(
        fontSize: fst.fontBase,
        fontWeight: FontWeight.w600,
      ),
      unselectedLabelStyle: TextStyle(
        fontSize: fst.fontBase,
        fontWeight: FontWeight.w500,
      ),
      dividerColor: Colors.transparent,
    ),
  );
}

/// 桌面平台 page 转场 no-op —— buildTransitions 直接返回 child,
/// 无 fade / slide / scale。挂到 ThemeData.pageTransitionsTheme 的
/// macOS/windows/linux 槽, 关掉嵌套 ShellRoute 挂载时的 Cupertino 横向滑入。
class _NoPageTransitionsBuilder extends PageTransitionsBuilder {
  const _NoPageTransitionsBuilder();

  @override
  Widget buildTransitions<T extends Object?>(
    PageRoute<T> route,
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) => child;
}

/// TextTheme — 字号从 FontSizeTokens 取,文字色用中性色。
/// 跟 docs/UI-Design-System.md §2.4 保持的相对层级 (display > headline > title >
/// body > label),但每档字号从 fst 拿,联动用户字体大小档。
///
/// Hybrid 调性 letter-spacing 系统 (按 prototype Hybrid `--letter-display: -0.022em`):
///   * displayLarge   ≈ -0.022em → fontHero × 2.0 × -0.022 ≈ fontHero × -0.044
///   * displayMedium  同上 (≈ -0.033em 折算)
///   * headlineLarge  -0.018em
///   * headlineMedium -0.015em
///   * 用 px 数值更直观,大致 -fontSize × 0.025
///
/// fontFeatures:开 stylisticSet(1) 让 Inter 的 'a' 'g' 字符使用现代变体
/// (CSS 'ss01')。系统字体里仅 Inter / SF Pro 等少数支持,其他字体无影响。
TextTheme _buildTextTheme(NeutralTokens n, FontSizeTokens fst) {
  // FontFeature 工厂构造器都不是 const(`tag` 字段是动态构造的)
  final heroFeatures = [
    FontFeature.stylisticSet(1), // 'ss01' — Inter 双层 a / g 现代变体
    FontFeature.tabularFigures(), // 等宽数字,标题里数字对齐
  ];
  return TextTheme(
    displayLarge: TextStyle(
      fontSize: fst.fontHero * 2.0,
      fontWeight: FontWeight.w800, // Hybrid display 800
      color: n.text1,
      letterSpacing: fst.fontHero * 2.0 * -0.025,
      height: 1.1,
      fontFeatures: heroFeatures,
    ),
    displayMedium: TextStyle(
      fontSize: fst.fontHero * 1.5,
      fontWeight: FontWeight.w700,
      color: n.text1,
      letterSpacing: fst.fontHero * 1.5 * -0.025,
      height: 1.2,
      fontFeatures: heroFeatures,
    ),
    headlineLarge: TextStyle(
      fontSize: fst.fontHero * 1.16,
      fontWeight: FontWeight.w700,
      color: n.text1,
      letterSpacing: fst.fontHero * 1.16 * -0.022,
      height: 1.25,
      fontFeatures: heroFeatures,
    ),
    headlineMedium: TextStyle(
      fontSize: fst.fontHero * 0.92,
      fontWeight: FontWeight.w700,
      color: n.text1,
      letterSpacing: fst.fontHero * 0.92 * -0.022,
      height: 1.3,
      fontFeatures: heroFeatures,
    ),
    titleLarge: TextStyle(
      fontSize: fst.fontH2 * 1.2,
      fontWeight: FontWeight.w600,
      color: n.text1,
    ),
    titleMedium: TextStyle(
      fontSize: fst.fontH2,
      fontWeight: FontWeight.w600,
      color: n.text1,
    ),
    titleSmall: TextStyle(
      fontSize: fst.fontCardTitle,
      fontWeight: FontWeight.w500,
      color: n.text1,
    ),
    bodyLarge: TextStyle(
      fontSize: fst.fontBase + 1,
      color: n.text1,
      height: fst.lineBody,
    ),
    bodyMedium: TextStyle(
      fontSize: fst.fontBase,
      color: n.text1,
      height: fst.lineBody,
    ),
    bodySmall: TextStyle(
      fontSize: fst.fontSm,
      color: n.text2,
      height: fst.lineTight,
    ),
    labelLarge: TextStyle(
      fontSize: fst.fontBase,
      fontWeight: FontWeight.w500,
      color: n.text1,
    ),
    labelMedium: TextStyle(
      fontSize: fst.fontSm,
      fontWeight: FontWeight.w500,
      color: n.text2,
      letterSpacing: 0.2,
    ),
    labelSmall: TextStyle(
      fontSize: fst.fontTileMeta,
      fontWeight: FontWeight.w500,
      color: n.text2,
      letterSpacing: 0.3,
    ),
  );
}
