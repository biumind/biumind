// FontSize — 三档字体大小,联动间距 / 字号 / 元素尺寸 / 列表宽度。
//
// 用户在 设置 → 外观 → 字体大小 选择,默认 small (紧凑)。
// 数值表见 docs/BiuMind-Theme-System-Design.md §3.4。
//
// 使用方式:
//   final m = Theme.of(context).extension<BiuMetrics>()!;
//   Container(width: m.threadListWidth, padding: EdgeInsets.all(m.cardPad), …)
//
// 加新数值: 在 FontSizeTokens 加字段 + 在 small/medium/large 三个常量都给值,
// 然后映射到 BiuMetrics ThemeExtension。三档之间数值的"档差"应该一致 — 不要
// 给 small 偷工减料。

/// User-facing 字号档位。default = small。
enum FontSize {
  small,
  medium,
  large;

  /// 持久化 wire id (小写)。 settings 写盘走这个。
  String get wireId => switch (this) {
        FontSize.small => 'small',
        FontSize.medium => 'medium',
        FontSize.large => 'large',
      };

  static FontSize byWireId(String? id) => switch (id) {
        'medium' => FontSize.medium,
        'large' => FontSize.large,
        _ => FontSize.small, // null / 未知 / legacy 都 fallback 默认
      };
}

/// 一档字号的全套数值 (21 字段) — 21 个组件子系统的 padding / 字号 / 尺寸 /
/// 列表宽度。组件读 BiuMetrics 拿这套,不再硬编码。
///
/// 字段命名规则:
///   * fontXxx       — 字号
///   * paddingXxx    — 内边距
///   * gapXxx        — 元素间距
///   * widthXxx      — 列表 / 容器固定宽度
///   * sizeXxx       — 元素尺寸 (avatar / dot)
///   * lineXxx       — line-height 比例
class FontSizeTokens {
  const FontSizeTokens({
    // ── 列表/容器宽度 ──────────────────────────────────────────
    required this.navRailWidth,
    required this.threadListWidth,
    // ── padding ───────────────────────────────────────────────
    required this.navItemPadH,
    required this.navItemPadV,
    required this.tilePadH,
    required this.tilePadV,
    required this.cardPad,
    required this.bannerPadH,
    required this.bannerPadV,
    // ── gap ───────────────────────────────────────────────────
    required this.gapGrid,
    required this.gapSection,
    // ── 字号 ──────────────────────────────────────────────────
    required this.fontBase,
    required this.fontSm,
    required this.fontTileTitle,
    required this.fontTileMeta,
    required this.fontCardTitle,
    required this.fontCardBody,
    required this.fontHero,
    required this.fontH2,
    // ── 元素尺寸 ──────────────────────────────────────────────
    required this.avatarSize,
    required this.modeDotSize,
    // ── line-height ───────────────────────────────────────────
    required this.lineTight,
    required this.lineBody,
  });

  final double navRailWidth;
  final double threadListWidth;

  final double navItemPadH;
  final double navItemPadV;
  final double tilePadH;
  final double tilePadV;
  final double cardPad;
  final double bannerPadH;
  final double bannerPadV;

  final double gapGrid;
  final double gapSection;

  final double fontBase;
  final double fontSm;
  final double fontTileTitle;
  final double fontTileMeta;
  final double fontCardTitle;
  final double fontCardBody;
  final double fontHero;
  final double fontH2;

  final double avatarSize;
  final double modeDotSize;

  final double lineTight;
  final double lineBody;

  // ── §3.4 三档常量 ────────────────────────────────────────────────────

  /// Small (默认) — 13" 屏单屏可见 ≥ 8 个 thread tile,工作效率优先。
  ///
  /// 跟 prototype v3 对照后做了一次"节奏向中线靠拢":thread tile 尤其紧凑
  /// 时 mode-dot 看起来像"未读 badge"夺走标题视觉中心。把 tilePadV / 字号 /
  /// dot 微调一档(仍小于 medium),让 small 档保留紧凑优势同时不再"挤"。
  /// 调整范围只在 thread list / hero card 节奏指标,sidebar nav 不变。
  static const FontSizeTokens small = FontSizeTokens(
    navRailWidth: 208,
    threadListWidth: 280,
    navItemPadH: 8,
    navItemPadV: 6,
    tilePadH: 12,
    tilePadV: 9,
    cardPad: 14,
    bannerPadH: 14,
    bannerPadV: 10,
    gapGrid: 12,
    gapSection: 20,
    fontBase: 12.5,
    fontSm: 11,
    fontTileTitle: 13,
    fontTileMeta: 11,
    fontCardTitle: 13.5,
    fontCardBody: 12,
    fontHero: 26,
    fontH2: 15,
    avatarSize: 40,
    modeDotSize: 7,
    lineTight: 1.4,
    lineBody: 1.5,
  );

  /// Medium — 大致对应 UI-Design-System.md §2.4 旧 typography 表。
  static const FontSizeTokens medium = FontSizeTokens(
    navRailWidth: 232,
    threadListWidth: 320,
    navItemPadH: 10,
    navItemPadV: 8,
    tilePadH: 12,
    tilePadV: 10,
    cardPad: 16,
    bannerPadH: 18,
    bannerPadV: 14,
    gapGrid: 14,
    gapSection: 24,
    fontBase: 14,
    fontSm: 12,
    fontTileTitle: 13.5,
    fontTileMeta: 11.5,
    fontCardTitle: 14.5,
    fontCardBody: 12.5,
    fontHero: 30,
    fontH2: 17,
    avatarSize: 44,
    modeDotSize: 8,
    lineTight: 1.4,
    lineBody: 1.55,
  );

  /// Large — 高可见性优先,适合演示 / 视障用户。
  static const FontSizeTokens large = FontSizeTokens(
    navRailWidth: 252,
    threadListWidth: 344,
    navItemPadH: 12,
    navItemPadV: 11,
    tilePadH: 16,
    tilePadV: 14,
    cardPad: 22,
    bannerPadH: 22,
    bannerPadV: 18,
    gapGrid: 18,
    gapSection: 32,
    fontBase: 15,
    fontSm: 12.5,
    fontTileTitle: 14.5,
    fontTileMeta: 12,
    fontCardTitle: 16,
    fontCardBody: 13.5,
    fontHero: 36,
    fontH2: 19,
    avatarSize: 52,
    modeDotSize: 10,
    lineTight: 1.4,
    lineBody: 1.65,
  );

  static const Map<FontSize, FontSizeTokens> all = {
    FontSize.small: small,
    FontSize.medium: medium,
    FontSize.large: large,
  };

  static FontSizeTokens of(FontSize s) => all[s]!;
}
