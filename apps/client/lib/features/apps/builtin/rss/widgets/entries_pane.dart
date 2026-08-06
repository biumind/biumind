// Middle pane of the inbox tab — list of entries belonging to the
// currently-selected feed (or all). Tapping a row selects it +
// schedules a 1.5s mark-read so a quick skim doesn't accidentally
// clear unread state.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';

/// M12.1 — below this width we treat the UI as a phone-style single pane and
/// enable swipe gestures on entry rows. Exposed for the widget test.
const double kRssNarrowWidth = 720;

class EntriesPane extends ConsumerStatefulWidget {
  const EntriesPane({super.key});

  @override
  ConsumerState<EntriesPane> createState() => _EntriesPaneState();
}

class _EntriesPaneState extends ConsumerState<EntriesPane> {
  Timer? _markReadTimer;
  String? _markReadFor;

  @override
  void dispose() {
    _markReadTimer?.cancel();
    super.dispose();
  }

  void _scheduleMarkRead(Entry entry) {
    if (!entry.unread) return;
    if (_markReadFor == entry.id) return;
    _markReadFor = entry.id;
    _markReadTimer?.cancel();
    _markReadTimer = Timer(const Duration(milliseconds: 1500), () async {
      if (!mounted) return;
      final actions = ref.read(rssActionsProvider);
      if (actions == null) return;
      // Optimistic local override so the row immediately stops looking
      // bold, even before the network round-trip completes.
      ref.read(rssSelectionProvider.notifier).markEntryRead(entry.id, true);
      try {
        await actions.entriesMarkRead(entry.id, true);
        ref.refreshEntries();
        ref.refreshFeeds();
      } catch (_) {
        // Roll back the optimistic flip on failure.
        ref.read(rssSelectionProvider.notifier).markEntryRead(entry.id, false);
      }
    });
  }

  Future<void> _setRead(Entry entry, bool read) async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    ref.read(rssSelectionProvider.notifier).markEntryRead(entry.id, read);
    try {
      await actions.entriesMarkRead(entry.id, read);
      ref.refreshEntries();
      ref.refreshFeeds();
    } catch (_) {
      ref.read(rssSelectionProvider.notifier).markEntryRead(entry.id, !read);
    }
  }

  Future<void> _setStarred(Entry entry, bool starred) async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      await actions.entriesStar(entry.id, starred);
      ref.refreshEntries();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('操作失败: $e')));
      }
    }
  }

  // M12.1 — long-press action sheet. Available on all widths; the primary
  // affordance for non-pointer (touch) devices that can't hover a toolbar.
  void _showActionSheet(Entry entry, bool isUnread) {
    showModalBottomSheet<void>(
      context: context,
      builder: (sheetCtx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: Icon(isUnread
                  ? Icons.mark_email_read_outlined
                  : Icons.mark_email_unread_outlined),
              title: Text(isUnread ? '标记已读' : '标记未读'),
              onTap: () {
                Navigator.pop(sheetCtx);
                _setRead(entry, isUnread);
              },
            ),
            ListTile(
              leading: Icon(
                  entry.starred ? Icons.star : Icons.star_outline,
                  color: entry.starred ? StarredColors.iconAlt : null),
              title: Text(entry.starred ? '取消收藏' : '收藏'),
              onTap: () {
                Navigator.pop(sheetCtx);
                _setStarred(entry, !entry.starred);
              },
            ),
            ListTile(
              leading: const Icon(Icons.menu_book_outlined),
              title: const Text('沉入 Wiki'),
              onTap: () async {
                Navigator.pop(sheetCtx);
                final actions = ref.read(rssActionsProvider);
                if (actions == null) return;
                try {
                  await actions.entriesToWiki(entry.id);
                  if (mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('已沉入 Wiki')));
                  }
                } catch (e) {
                  if (mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(content: Text('沉入失败: $e')));
                  }
                }
              },
            ),
            if (entry.url.isNotEmpty)
              ListTile(
                leading: const Icon(Icons.link_outlined),
                title: const Text('复制链接'),
                onTap: () async {
                  Navigator.pop(sheetCtx);
                  await Clipboard.setData(ClipboardData(text: entry.url));
                  if (mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('链接已复制')));
                  }
                },
              ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final selection = ref.watch(rssSelectionProvider);
    final query = EntriesQuery(feedId: selection.selectedFeedId);
    final entriesAsync = ref.watch(entriesProvider(query));
    final narrow = MediaQuery.sizeOf(context).width < kRssNarrowWidth;

    return Container(
      width: 400,
      decoration: BoxDecoration(
        color: BiuTokens.bg,
        border: Border(right: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: entriesAsync.when(
        loading: () => const _EntriesSkeleton(),
        error: (e, _) => _ErrorState(
          message: '$e',
          onRetry: () => ref.invalidate(entriesProvider(query)),
        ),
        data: (entries) {
          if (entries.isEmpty) {
            return const _EmptyEntries();
          }
          return ListView.separated(
            padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
            itemCount: entries.length,
            separatorBuilder: (_, _) =>
                Divider(height: 1, color: BiuTokens.borderSubtle),
            itemBuilder: (_, i) {
              final e = entries[i];
              final overrideUnread =
                  selection.entryReadOverride[e.id] ?? e.unread;
              final isSelected = selection.selectedEntryId == e.id;
              final row = _EntryRow(
                entry: e,
                unread: overrideUnread,
                selected: isSelected,
                onTap: () {
                  ref.read(rssSelectionProvider.notifier).selectEntry(e.id);
                  _scheduleMarkRead(e);
                },
                onLongPress: () => _showActionSheet(e, overrideUnread),
              );
              if (!narrow) return row;
              // M12.1 — phone-style swipe actions:
              //   右滑 (startToEnd) → 收藏；左滑 (endToStart) → 已读。
              // confirmDismiss returns false: we toggle state but keep the
              // row in place (swipe-action, not swipe-to-remove).
              return Dismissible(
                key: ValueKey('swipe-${e.id}'),
                background: _SwipeBg(
                  alignment: Alignment.centerLeft,
                  color: StarredColors.iconAlt,
                  icon: e.starred ? Icons.star_outline : Icons.star,
                  label: e.starred ? '取消收藏' : '收藏',
                ),
                secondaryBackground: _SwipeBg(
                  alignment: Alignment.centerRight,
                  color: BiuTokens.purple,
                  icon: Icons.mark_email_read_outlined,
                  label: '已读',
                ),
                confirmDismiss: (dir) async {
                  if (dir == DismissDirection.startToEnd) {
                    await _setStarred(e, !e.starred);
                  } else {
                    await _setRead(e, true);
                  }
                  return false; // keep row; action already applied
                },
                child: row,
              );
            },
          );
        },
      ),
    );
  }
}

class _EntryRow extends StatelessWidget {
  const _EntryRow({
    required this.entry,
    required this.unread,
    required this.selected,
    required this.onTap,
    this.onLongPress,
  });

  final Entry entry;
  final bool unread;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback? onLongPress;

  @override
  Widget build(BuildContext context) {
    final fade = !selected && !unread;
    return InkWell(
      onTap: onTap,
      onLongPress: onLongPress,
      child: Container(
        color: selected ? BiuTokens.purpleSoft : Colors.transparent,
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space4, vertical: BiuTokens.space3),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Unread dot
            Padding(
              padding: const EdgeInsets.only(top: 6, right: BiuTokens.space2),
              child: Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  color: unread ? BiuTokens.purple : Colors.transparent,
                  shape: BoxShape.circle,
                ),
              ),
            ),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(
                        child: Text(
                          entry.title,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontSize: 14,
                            height: 1.35,
                            fontWeight:
                                unread ? FontWeight.w600 : FontWeight.w500,
                            color: fade
                                ? BiuTokens.textSecondary
                                : BiuTokens.text,
                          ),
                        ),
                      ),
                      if (entry.aiImportance > 0) ...[
                        const SizedBox(width: BiuTokens.space2),
                        _ImportanceStars(level: entry.aiImportance),
                      ],
                    ],
                  ),
                  const SizedBox(height: BiuTokens.space1),
                  Text(
                    _subtitle(entry),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontSize: 11, color: BiuTokens.textMuted),
                  ),
                  // AI 区: takeaway 一行 + 3 bullets。这是 v2 的核心
                  // 体验差异——不进 reader 就能判断要不要读。
                  if (entry.aiTakeaway.isNotEmpty) ...[
                    const SizedBox(height: BiuTokens.space2),
                    _AITakeawayRow(text: entry.aiTakeaway),
                    if (entry.aiBullets.isNotEmpty) ...[
                      const SizedBox(height: 4),
                      _AIBullets(bullets: entry.aiBullets),
                    ],
                  ] else if (!entry.aiProcessed) ...[
                    const SizedBox(height: BiuTokens.space2),
                    _AIPlaceholder(),
                  ] else if (entry.snippet.isNotEmpty) ...[
                    // AI processed but takeaway empty (probably error
                    // or zero-content entry) — fall back to snippet.
                    const SizedBox(height: BiuTokens.space2),
                    Text(
                      entry.snippet,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 12,
                        height: 1.45,
                        color: BiuTokens.textSecondary,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            if (entry.starred) ...[
              const SizedBox(width: BiuTokens.space2),
              Icon(Icons.star, size: 14, color: StarredColors.iconAlt),
            ],
          ],
        ),
      ),
    );
  }

  String _subtitle(Entry e) {
    final parts = <String>[];
    if (e.author.isNotEmpty) parts.add(e.author);
    final rel = relativeTime(e.publishedAt ?? e.fetchedAt);
    if (rel.isNotEmpty) parts.add(rel);
    return parts.join(' · ');
  }
}

/// M12.1 — colored swipe background revealed behind an entry row. icon +
/// label pinned to the swipe-origin edge.
class _SwipeBg extends StatelessWidget {
  const _SwipeBg({
    required this.alignment,
    required this.color,
    required this.icon,
    required this.label,
  });

  final Alignment alignment;
  final Color color;
  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    final leading = alignment == Alignment.centerLeft;
    final children = [
      Icon(icon, color: Colors.white, size: 20),
      const SizedBox(width: BiuTokens.space2),
      Text(label,
          style: const TextStyle(
              color: Colors.white, fontSize: 13, fontWeight: FontWeight.w600)),
    ];
    return Container(
      color: color,
      alignment: alignment,
      padding: const EdgeInsets.symmetric(horizontal: BiuTokens.space4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: leading ? children : children.reversed.toList(),
      ),
    );
  }
}

class _EntriesSkeleton extends StatelessWidget {
  const _EntriesSkeleton();
  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      itemCount: 8,
      separatorBuilder: (_, _) =>
          Divider(height: 1, color: BiuTokens.borderSubtle),
      itemBuilder: (_, _) => Padding(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space4, vertical: BiuTokens.space3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _bar(width: double.infinity, height: 12),
            const SizedBox(height: BiuTokens.space2),
            _bar(width: 120, height: 10),
            const SizedBox(height: BiuTokens.space2),
            _bar(width: double.infinity, height: 10),
            const SizedBox(height: 4),
            _bar(width: 200, height: 10),
          ],
        ),
      ),
    );
  }

  Widget _bar({required double width, required double height}) => Container(
        width: width,
        height: height,
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(3),
        ),
      );
}

class _EmptyEntries extends StatelessWidget {
  const _EmptyEntries();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.inbox_outlined, size: 36, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text('暂无文章',
                style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: BiuTokens.textSecondary)),
            const SizedBox(height: BiuTokens.space1),
            Text('点击右上角刷新拉取最新文章',
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

class _ErrorState extends StatelessWidget {
  const _ErrorState({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.warning_amber_rounded,
                size: 32, color: BiuTokens.error),
            const SizedBox(height: BiuTokens.space2),
            const Text('加载失败',
                style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.error)),
            const SizedBox(height: BiuTokens.space1),
            SelectableText(message,
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
            const SizedBox(height: BiuTokens.space3),
            OutlinedButton(onPressed: onRetry, child: const Text('重试')),
          ],
        ),
      ),
    );
  }
}

/// 1-3 颗 ★ 表示 AI 评估的重要度. 1 = 灰, 2 = amber, 3 = red.
class _ImportanceStars extends StatelessWidget {
  const _ImportanceStars({required this.level});
  final int level;

  @override
  Widget build(BuildContext context) {
    final color = switch (level) {
      3 => PriorityColors.high,
      2 => PriorityColors.medium,
      _ => BiuTokens.textMuted,
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: List.generate(level.clamp(1, 3), (_) {
        return Padding(
          padding: const EdgeInsets.only(left: 1),
          child: Icon(Icons.star, size: 12, color: color),
        );
      }),
    );
  }
}

/// AI takeaway: 1 行斜体, 紫色调点睛. ≤ 2 行省略.
class _AITakeawayRow extends StatelessWidget {
  const _AITakeawayRow({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(top: 2),
          child: Icon(Icons.auto_awesome, size: 12, color: scheme.primary),
        ),
        const SizedBox(width: 4),
        Expanded(
          child: Text(
            text,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              fontSize: 12,
              height: 1.4,
              fontStyle: FontStyle.italic,
              color: scheme.primary,
              fontWeight: FontWeight.w500,
            ),
          ),
        ),
      ],
    );
  }
}

/// 3 bullets dot list, 每行 ≤ 1 行省略.
class _AIBullets extends StatelessWidget {
  const _AIBullets({required this.bullets});
  final List<String> bullets;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: bullets.take(3).map((b) {
        return Padding(
          padding: const EdgeInsets.only(top: 2, left: 16),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.only(top: 7, right: 6),
                child: Container(
                  width: 3,
                  height: 3,
                  decoration: BoxDecoration(
                    color: BiuTokens.textMuted,
                    shape: BoxShape.circle,
                  ),
                ),
              ),
              Expanded(
                child: Text(
                  b,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 12,
                    height: 1.4,
                    color: BiuTokens.textSecondary,
                  ),
                ),
              ),
            ],
          ),
        );
      }).toList(),
    );
  }
}

/// AI 处理中骨架. 一段灰条 + "AI 处理中…"标签.
class _AIPlaceholder extends StatelessWidget {
  const _AIPlaceholder();
  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(Icons.auto_awesome, size: 11, color: BiuTokens.textMuted),
        const SizedBox(width: 4),
        Text(
          'AI 处理中…',
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
        ),
      ],
    );
  }
}
