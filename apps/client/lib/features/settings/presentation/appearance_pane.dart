// AppearancePane — 颜色主题 / 字体大小 / 模式 三段式外观设置。
//
// 设计文档:docs/BiuMind-Theme-System-Design.md §5
//
// UX 决策:
//   * 主题色 grid:推荐区(featured)在前,然后所有 17 个色板按色系归组
//   * Swatch chip:双色小圆点(brand 60% + accent 40%) + 名称 + 选中 ✓
//   * 字号 segmented + live preview(切换立刻看效果)
//   * 模式 radio:跟随系统 / 浅色 / 深色
//
// 切换全部立即生效(无需 restart) — settings 写入 → MaterialApp 重 build →
// buildTheme 重跑 → 整个 app 同步切换。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme/extensions.dart';
import '../../../app/theme/font_size.dart';
import '../../../app/theme/palettes.dart';
import '../../../app/theme/tokens.dart';
import '../../../core/ui/biu_card.dart';
import '../../../l10n/app_localizations.dart';
import '../../../services/settings_repo.dart';
import '../../chat/application/chat_preferences.dart';
import '../application/settings_controller.dart';

class AppearancePane extends ConsumerWidget {
  const AppearancePane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final c = Theme.of(context).extension<BiuColors>()!;
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final cur = settings?.palette ?? PaletteId.inkblueOrange;
    final curFontSize = settings?.fontSize ?? FontSize.small;
    final curTheme = settings?.theme ?? ThemePreference.system;
    // 语言是 app 级 UI 语言(main.dart 据此设 Locale),存在 chatPreferences
    // 里只是历史落点;控件归到外观这里统一管。
    final localeOverride =
        ref.watch(chatPreferencesProvider.select((p) => p.localeOverride));

    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: SpacingTokens.s5, vertical: SpacingTokens.s6),
          children: [
            Text(t.settingsNavAppearance,
                style: Theme.of(context).textTheme.headlineLarge),
            const SizedBox(height: SpacingTokens.s1),
            Text(t.settingsAppearanceSectionSubtitle,
                style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: SpacingTokens.s5),

            // ── 语言 ──────────────────────────────────────────────
            _SectionCard(
              title: t.chatV2SettingsLanguage,
              child: SegmentedButton<String?>(
                segments: [
                  ButtonSegment(
                    value: null,
                    label: Text(t.chatV2SettingsLanguageSystem),
                  ),
                  ButtonSegment(
                    value: 'zh',
                    label: Text(t.chatV2SettingsLanguageZh),
                  ),
                  ButtonSegment(
                    value: 'en',
                    label: Text(t.chatV2SettingsLanguageEn),
                  ),
                ],
                selected: {localeOverride},
                onSelectionChanged: (s) => ref
                    .read(chatPreferencesProvider.notifier)
                    .setLocaleOverride(s.first),
              ),
            ),
            const SizedBox(height: SpacingTokens.s4),

            // ── 颜色主题 ──────────────────────────────────────────
            _SectionCard(
              title: t.settingsAppearanceColorTheme,
              child: _PaletteGrid(
                selected: cur,
                onPick: (p) async {
                  await ref
                      .read(settingsControllerProvider.notifier)
                      .updatePalette(p);
                },
                recommendedLabel: t.settingsAppearanceColorThemeRecommended,
              ),
            ),
            const SizedBox(height: SpacingTokens.s4),

            // ── 字体大小 ──────────────────────────────────────────
            _SectionCard(
              title: t.settingsAppearanceFontSize,
              child: _FontSizeBlock(
                selected: curFontSize,
                onPick: (f) async {
                  await ref
                      .read(settingsControllerProvider.notifier)
                      .updateFontSize(f);
                },
              ),
            ),
            const SizedBox(height: SpacingTokens.s4),

            // ── 模式 ──────────────────────────────────────────────
            _SectionCard(
              title: t.settingsAppearanceMode,
              child: _ModeBlock(
                selected: curTheme,
                onPick: (m) async {
                  await ref
                      .read(settingsControllerProvider.notifier)
                      .updateTheme(m);
                },
              ),
            ),

            const SizedBox(height: SpacingTokens.s8),
            _ColorChipRow(c: c),
          ],
        ),
      ),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// SectionCard
// ─────────────────────────────────────────────────────────────────────────

class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.child});
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    // 设置面板的 section 卡 — lift=0 (静态 card,不需要 hover 上抬),
    // 但保留 brand 3% 微染 + shadow-sm + hairline 边框,跟 prototype
    // .settings-section 视觉一致。
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(SpacingTokens.s4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w600,
                color: c.text2,
                letterSpacing: 0.2,
              )),
          const SizedBox(height: SpacingTokens.s3),
          child,
        ],
      ),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// 主题色 grid
// ─────────────────────────────────────────────────────────────────────────

class _PaletteGrid extends StatelessWidget {
  const _PaletteGrid({
    required this.selected,
    required this.onPick,
    required this.recommendedLabel,
  });

  final PaletteId selected;
  final ValueChanged<PaletteId> onPick;
  final String recommendedLabel;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final featured = featuredPalettes;
    final rest = availablePalettes
        .where((p) => !p.isFeatured)
        .toList(growable: false);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (featured.isNotEmpty) ...[
          Padding(
            padding: const EdgeInsets.only(bottom: SpacingTokens.s2),
            child: Text(recommendedLabel,
                style: TextStyle(
                  fontSize: 11,
                  color: c.textMuted,
                  letterSpacing: 0.4,
                )),
          ),
          _Grid(
            items: featured,
            selected: selected,
            onPick: onPick,
          ),
          const SizedBox(height: SpacingTokens.s3),
          Divider(height: 1, color: c.borderHairline),
          const SizedBox(height: SpacingTokens.s3),
        ],
        _Grid(
          items: rest,
          selected: selected,
          onPick: onPick,
        ),
      ],
    );
  }
}

class _Grid extends StatelessWidget {
  const _Grid({
    required this.items,
    required this.selected,
    required this.onPick,
  });
  final List<PaletteSpec> items;
  final PaletteId selected;
  final ValueChanged<PaletteId> onPick;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (ctx, cons) {
        // 4 列布局 - 在窄宽度下退化到 3 / 2 列
        final cols = cons.maxWidth >= 540 ? 4 : (cons.maxWidth >= 380 ? 3 : 2);
        final gap = SpacingTokens.s2;
        final w = (cons.maxWidth - gap * (cols - 1)) / cols;
        return Wrap(
          spacing: gap,
          runSpacing: gap,
          children: [
            for (final p in items)
              SizedBox(
                width: w,
                child: _SwatchChip(
                  spec: p,
                  selected: p.id == selected,
                  onTap: () => onPick(p.id),
                ),
              ),
          ],
        );
      },
    );
  }
}

class _SwatchChip extends StatelessWidget {
  const _SwatchChip({
    required this.spec,
    required this.selected,
    required this.onTap,
  });

  final PaletteSpec spec;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final brand = spec.brand.forBrightness(Theme.of(context).brightness);
    final accent = spec.accent.forBrightness(Theme.of(context).brightness);
    final name = _paletteName(AppLocalizations.of(context)!, spec.id);

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        child: AnimatedContainer(
          duration: MotionTokens.fast,
          padding: const EdgeInsets.symmetric(
              horizontal: SpacingTokens.s3, vertical: SpacingTokens.s2 + 2),
          decoration: BoxDecoration(
            color: selected
                ? brand.withValues(alpha: isDark ? 0.18 : 0.08)
                : c.surface1,
            borderRadius: BorderRadius.circular(RadiusTokens.md),
            border: Border.all(
              color: selected ? brand : c.borderSoft,
              width: selected ? 1.5 : 1,
            ),
          ),
          child: Row(
            children: [
              _DualDot(brand: brand, accent: accent),
              const SizedBox(width: SpacingTokens.s2),
              Expanded(
                child: Text(
                  name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                    color: selected ? brand : c.text1,
                  ),
                ),
              ),
              if (selected)
                Icon(Icons.check, size: 14, color: brand),
            ],
          ),
        ),
      ),
    );
  }
}

class _DualDot extends StatelessWidget {
  const _DualDot({required this.brand, required this.accent});
  final Color brand;
  final Color accent;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 18,
      height: 12,
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          Positioned(
            left: 0,
            top: 0,
            child: Container(
              width: 12,
              height: 12,
              decoration: BoxDecoration(
                color: brand,
                shape: BoxShape.circle,
              ),
            ),
          ),
          Positioned(
            left: 8,
            top: 0,
            child: Container(
              width: 12,
              height: 12,
              decoration: BoxDecoration(
                color: accent,
                shape: BoxShape.circle,
                border: Border.all(
                  color: Theme.of(context)
                      .extension<BiuColors>()!
                      .surface0,
                  width: 1.5,
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// 字体大小 segmented + live preview
// ─────────────────────────────────────────────────────────────────────────

class _FontSizeBlock extends StatelessWidget {
  const _FontSizeBlock({required this.selected, required this.onPick});
  final FontSize selected;
  final ValueChanged<FontSize> onPick;

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final c = Theme.of(context).extension<BiuColors>()!;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.info_outline, size: 12, color: c.textMuted),
            const SizedBox(width: 4),
            Expanded(
              child: Text(
                t.settingsAppearanceFontSizeHint,
                style: TextStyle(fontSize: 11, color: c.text3),
              ),
            ),
          ],
        ),
        const SizedBox(height: SpacingTokens.s3),
        _Segmented<FontSize>(
          options: const [
            (FontSize.small, 'small'),
            (FontSize.medium, 'medium'),
            (FontSize.large, 'large'),
          ],
          labelOf: (f) => switch (f) {
            FontSize.small => t.settingsAppearanceFontSizeSmall,
            FontSize.medium => t.settingsAppearanceFontSizeMedium,
            FontSize.large => t.settingsAppearanceFontSizeLarge,
          },
          selected: selected,
          onPick: onPick,
        ),
        const SizedBox(height: SpacingTokens.s3),
        // Live preview - 用当前 fontSize 对应的 FontSizeTokens 渲染
        _FontSizePreview(fontSize: selected),
      ],
    );
  }
}

class _FontSizePreview extends StatelessWidget {
  const _FontSizePreview({required this.fontSize});
  final FontSize fontSize;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final t = AppLocalizations.of(context)!;
    final fst = FontSizeTokens.of(fontSize);

    return Container(
      padding: EdgeInsets.all(fst.cardPad),
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        border: Border.all(color: c.borderHairline),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            t.settingsAppearancePreviewTitle,
            style: TextStyle(
              fontSize: fst.fontH2,
              fontWeight: FontWeight.w600,
              color: c.text1,
              height: fst.lineTight,
            ),
          ),
          SizedBox(height: fst.gapGrid * 0.5),
          Text(
            t.settingsAppearancePreviewBody,
            style: TextStyle(
              fontSize: fst.fontBase,
              color: c.text2,
              height: fst.lineBody,
            ),
          ),
        ],
      ),
    );
  }
}

class _Segmented<T> extends StatelessWidget {
  const _Segmented({
    required this.options,
    required this.labelOf,
    required this.selected,
    required this.onPick,
  });

  final List<(T, String)> options;
  final String Function(T) labelOf;
  final T selected;
  final ValueChanged<T> onPick;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Container(
      decoration: BoxDecoration(
        color: c.surface2,
        borderRadius: BorderRadius.circular(RadiusTokens.md),
        border: Border.all(color: c.borderHairline),
      ),
      padding: const EdgeInsets.all(3),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final (val, _) in options)
            Expanded(
              child: _SegButton<T>(
                value: val,
                label: labelOf(val),
                selected: val == selected,
                onTap: () => onPick(val),
              ),
            ),
        ],
      ),
    );
  }
}

class _SegButton<T> extends StatelessWidget {
  const _SegButton({
    required this.value,
    required this.label,
    required this.selected,
    required this.onTap,
  });
  final T value;
  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(RadiusTokens.sm),
        child: AnimatedContainer(
          duration: MotionTokens.fast,
          padding: const EdgeInsets.symmetric(vertical: 8),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: selected ? c.surface0 : Colors.transparent,
            borderRadius: BorderRadius.circular(RadiusTokens.sm),
            boxShadow: selected
                ? ShadowTokens.forBrightness(Theme.of(context).brightness).sm
                : null,
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 13,
              fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
              color: selected ? c.text1 : c.text3,
            ),
          ),
        ),
      ),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// 模式
// ─────────────────────────────────────────────────────────────────────────

class _ModeBlock extends StatelessWidget {
  const _ModeBlock({required this.selected, required this.onPick});
  final ThemePreference selected;
  final ValueChanged<ThemePreference> onPick;

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    return _Segmented<ThemePreference>(
      options: const [
        (ThemePreference.system, 'system'),
        (ThemePreference.light, 'light'),
        (ThemePreference.dark, 'dark'),
      ],
      labelOf: (m) => switch (m) {
        ThemePreference.system => t.themeSystem,
        ThemePreference.light => t.themeLight,
        ThemePreference.dark => t.themeDark,
      },
      selected: selected,
      onPick: onPick,
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// 当前主题色实时小预览 (mode chip)
// ─────────────────────────────────────────────────────────────────────────

class _ColorChipRow extends StatelessWidget {
  const _ColorChipRow({required this.c});
  final BiuColors c;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        _Chip(label: 'brand', color: c.brand),
        const SizedBox(width: 8),
        _Chip(label: 'accent', color: c.accent),
        const SizedBox(width: 8),
        _Chip(label: 'chat', color: c.modeChat),
        const SizedBox(width: 8),
        _Chip(label: 'agent', color: c.modeAgent),
        const SizedBox(width: 8),
        _Chip(label: 'task', color: c.modeTask),
      ],
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(RadiusTokens.sm),
        border: Border.all(color: color.withValues(alpha: 0.32)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(color: color, shape: BoxShape.circle),
          ),
          const SizedBox(width: 6),
          Text(label,
              style: TextStyle(
                fontSize: 11,
                color: color,
                fontWeight: FontWeight.w600,
                letterSpacing: 0.3,
              )),
        ],
      ),
    );
  }
}

// ─────────────────────────────────────────────────────────────────────────
// PaletteId → display name mapper
// ─────────────────────────────────────────────────────────────────────────

String _paletteName(AppLocalizations t, PaletteId id) => switch (id) {
      PaletteId.purpleOrange => t.paletteNamePurpleOrange,
      PaletteId.purple => t.paletteNamePurple,
      PaletteId.purpleBlue => t.paletteNamePurpleBlue,
      PaletteId.purplePink => t.paletteNamePurplePink,
      PaletteId.purpleEmerald => t.paletteNamePurpleEmerald,
      PaletteId.aurora => t.paletteNameAurora,
      PaletteId.sunset => t.paletteNameSunset,
      PaletteId.cyber => t.paletteNameCyber,
      PaletteId.ocean => t.paletteNameOcean,
      PaletteId.emeraldGold => t.paletteNameEmeraldGold,
      PaletteId.rose => t.paletteNameRose,
      PaletteId.onyx => t.paletteNameOnyx,
      PaletteId.inkblueOrange => t.paletteNameInkblueOrange,
      PaletteId.quantumTitanium => t.paletteNameQuantumTitanium,
      PaletteId.claudeWarm => t.paletteNameClaudeWarm,
      PaletteId.graphiteCyan => t.paletteNameGraphiteCyan,
      PaletteId.indigoSand => t.paletteNameIndigoSand,
      PaletteId.wikiGreen => t.paletteNameWikiGreen,
    };
