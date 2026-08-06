// One board card — newsnow-style colored panel.
//
// Top band uses the source's accent color. The body lists ALL items
// returned by boards_snapshot (limit 30), scrolling within the card
// when overflow. Click an item → external launch via url_launcher.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import 'board_icon.dart';
import '../providers.dart';

/// Background tint + accent color + on-accent text for a tailwind-ish
/// color name. Light/dark are computed off `BiuTokens.brightness`.
class _BoardPalette {
  const _BoardPalette({
    required this.tint,
    required this.bandTint,
    required this.accent,
    required this.onAccent,
    required this.border,
  });
  final Color tint; // card background
  final Color bandTint; // top band background
  final Color accent; // saturated for name + rank pills
  final Color onAccent; // text on accent
  final Color border;

  static _BoardPalette of(String name) {
    final isDark = BiuTokens.brightness == Brightness.dark;
    // 命名色查表 NamedPalette.byName (Tailwind-500 家族)。fallback 走当前主题
    // brand — 没识别的色名跟着用户色板走比硬给紫色更自然。
    final accent = NamedPalette.byName(name) ?? BiuTokens.purple;
    final tintAlpha = isDark ? 0.16 : 0.08;
    final bandAlpha = isDark ? 0.28 : 0.16;
    final borderAlpha = isDark ? 0.30 : 0.22;
    return _BoardPalette(
      tint: accent.withValues(alpha: tintAlpha),
      bandTint: accent.withValues(alpha: bandAlpha),
      accent: accent,
      onAccent: Colors.white,
      border: accent.withValues(alpha: borderAlpha),
    );
  }
}

/// Split a board name like "知乎 · 热榜" into ("知乎", "热榜").
({String primary, String chip}) _splitName(String name) {
  if (name.isEmpty) return (primary: '', chip: '');
  // Common separators newsnow uses: " · ", " - ", " | ", " — ".
  for (final sep in const [' · ', ' - ', ' | ', ' — ', '·', '-', '|']) {
    final idx = name.indexOf(sep);
    if (idx > 0 && idx < name.length - sep.length) {
      return (
        primary: name.substring(0, idx).trim(),
        chip: name.substring(idx + sep.length).trim(),
      );
    }
  }
  return (primary: name, chip: '');
}

class BoardCard extends ConsumerWidget {
  const BoardCard({super.key, required this.board});
  final Board board;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final snapshotAsync = ref.watch(boardSnapshotProvider(board.id));
    final palette = _BoardPalette.of(board.color);
    return Container(
      decoration: BoxDecoration(
        color: Color.alphaBlend(palette.tint, BiuTokens.surface),
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        border: Border.all(color: palette.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _Header(
            board: board,
            palette: palette,
            snapshotAsync: snapshotAsync,
            onRefresh: () =>
                ref.invalidate(boardSnapshotProvider(board.id)),
          ),
          if (board.lastStatus == 'error')
            _ErrorBanner(message: board.lastError),
          Expanded(
            child: snapshotAsync.when(
              loading: () => const _ItemsSkeleton(),
              error: (e, _) => Padding(
                padding: const EdgeInsets.all(BiuTokens.space3),
                child: Text(
                  '加载失败：$e',
                  style: const TextStyle(
                      fontSize: 12, color: BiuTokens.error),
                  maxLines: 4,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              data: (snap) {
                if (snap.items.isEmpty) {
                  return Center(
                    child: Padding(
                      padding: const EdgeInsets.all(BiuTokens.space4),
                      child: Text(
                        '等待首次抓取',
                        style: TextStyle(
                          fontSize: 12,
                          color: BiuTokens.textMuted,
                        ),
                      ),
                    ),
                  );
                }
                return ListView.builder(
                  padding: const EdgeInsets.symmetric(
                      horizontal: BiuTokens.space3,
                      vertical: BiuTokens.space2),
                  itemCount: snap.items.length,
                  itemBuilder: (_, i) =>
                      _ItemRow(item: snap.items[i], palette: palette),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.board,
    required this.palette,
    required this.snapshotAsync,
    required this.onRefresh,
  });
  final Board board;
  final _BoardPalette palette;
  final AsyncValue<BoardSnapshot> snapshotAsync;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    final captured =
        snapshotAsync.valueOrNull?.capturedAt ?? board.lastFetchedAt;
    final parts = _splitName(board.name.isEmpty ? board.id : board.name);
    final primaryChar = parts.primary.isNotEmpty
        ? parts.primary.characters.first
        : (board.id.isNotEmpty ? board.id[0] : '·');

    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: BiuTokens.space3),
      decoration: BoxDecoration(color: palette.bandTint),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          BoardLogo(
            sourceId: board.id,
            fallbackLetter: primaryChar.toUpperCase(),
            fallbackBg: palette.accent,
            fallbackFg: palette.onAccent,
            size: 28,
          ),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        parts.primary.isEmpty ? board.id : parts.primary,
                        style: TextStyle(
                          fontSize: 15,
                          fontWeight: FontWeight.w700,
                          color: palette.accent,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    if (parts.chip.isNotEmpty) ...[
                      const SizedBox(width: BiuTokens.space2),
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: palette.accent.withValues(alpha: 0.18),
                          borderRadius:
                              BorderRadius.circular(BiuTokens.radiusXs),
                        ),
                        child: Text(
                          parts.chip,
                          style: TextStyle(
                            fontSize: 10,
                            fontWeight: FontWeight.w600,
                            color: palette.accent,
                          ),
                        ),
                      ),
                    ],
                  ],
                ),
                const SizedBox(height: 2),
                Text(
                  captured == null
                      ? '正在抓取…'
                      : '${relativeTime(captured)}更新',
                  style: TextStyle(
                    fontSize: 11,
                    color: BiuTokens.textSecondary,
                  ),
                ),
              ],
            ),
          ),
          _IconAction(
            tooltip: '刷新',
            color: palette.accent,
            icon: Icons.refresh,
            onTap: onRefresh,
          ),
          _IconAction(
            tooltip: '收藏（暂未实现）',
            color: palette.accent,
            icon: Icons.star_border,
            onTap: () {},
          ),
          _IconAction(
            tooltip: '更多（暂未实现）',
            color: palette.accent,
            icon: Icons.more_horiz,
            onTap: () {},
          ),
        ],
      ),
    );
  }
}

class _IconAction extends StatelessWidget {
  const _IconAction({
    required this.tooltip,
    required this.color,
    required this.icon,
    required this.onTap,
  });
  final String tooltip;
  final Color color;
  final IconData icon;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tooltip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        child: Padding(
          padding: const EdgeInsets.all(4),
          child: Icon(icon, size: 16, color: color),
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: 6),
      color: BiuTokens.errorSoft,
      child: Text(
        message.isEmpty ? '上游异常 · 稍后重试' : '上游异常 · $message',
        style: const TextStyle(
          fontSize: 11,
          color: BiuTokens.error,
          fontWeight: FontWeight.w500,
        ),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
    );
  }
}

class _ItemRow extends StatelessWidget {
  const _ItemRow({required this.item, required this.palette});
  final BoardItem item;
  final _BoardPalette palette;

  @override
  Widget build(BuildContext context) {
    final uri = safeParseUri(item.url);
    // Right-side hint priority: info → delta_label → "".
    final hint = item.info.isNotEmpty
        ? item.info
        : (item.deltaLabel.isNotEmpty && item.deltaLabel != '—'
            ? item.deltaLabel
            : '');
    final hintColor = item.info.isNotEmpty
        ? BiuTokens.textMuted
        : (item.rankDelta > 0
            ? BiuTokens.green
            : (item.rankDelta < 0 ? BiuTokens.error : BiuTokens.textMuted));
    final isTop3 = item.rank >= 1 && item.rank <= 3;

    return InkWell(
      onTap: uri == null
          ? null
          : () => launchUrl(uri, mode: LaunchMode.externalApplication),
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 5, horizontal: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Rank pill
            Container(
              width: 22,
              height: 22,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: isTop3
                    ? palette.accent
                    : palette.accent.withValues(alpha: 0.14),
                borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
              ),
              child: Text(
                '${item.rank}',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: isTop3 ? palette.onAccent : palette.accent,
                ),
              ),
            ),
            const SizedBox(width: BiuTokens.space2),
            // Title + (optional) "新" tag
            Expanded(
              child: Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Wrap(
                  crossAxisAlignment: WrapCrossAlignment.center,
                  spacing: 4,
                  runSpacing: 2,
                  children: [
                    if (item.isNew)
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 4, vertical: 1),
                        decoration: BoxDecoration(
                          color: BiuTokens.error,
                          borderRadius:
                              BorderRadius.circular(BiuTokens.radiusXs),
                        ),
                        child: const Text(
                          '新',
                          style: TextStyle(
                            fontSize: 9,
                            fontWeight: FontWeight.w700,
                            color: Colors.white,
                            height: 1.2,
                          ),
                        ),
                      ),
                    Text(
                      item.title,
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                        height: 1.4,
                        color: BiuTokens.text,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            if (hint.isNotEmpty) ...[
              const SizedBox(width: BiuTokens.space2),
              Padding(
                padding: const EdgeInsets.only(top: 3),
                child: Text(
                  hint,
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w500,
                    color: hintColor,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _ItemsSkeleton extends StatelessWidget {
  const _ItemsSkeleton();
  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
      itemCount: 8,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (_, _) => Container(
        height: 14,
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(3),
        ),
      ),
    );
  }
}
