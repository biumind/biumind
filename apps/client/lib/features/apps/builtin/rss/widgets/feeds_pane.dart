// Left pane of the inbox tab — list of feeds, with a synthetic "全部"
// row at the top selected by default.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../../../../../services/auth_service.dart';
import '../../../presentation/app_icon_resolver.dart';
import '../models.dart';
import '../providers.dart';
import 'add_feed_sheet.dart';

class FeedsPane extends ConsumerWidget {
  const FeedsPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feedsAsync = ref.watch(feedsProvider);
    final selection = ref.watch(rssSelectionProvider);
    final controller = ref.read(rssSelectionProvider.notifier);
    final creds = ref.watch(hubCredentialsProvider);

    return Container(
      width: 240,
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(right: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: feedsAsync.when(
              loading: () => const _FeedsSkeleton(),
              error: (e, _) => _ErrorRow(
                message: '$e',
                onRetry: () => ref.refreshFeeds(),
              ),
              data: (feeds) {
                final unreadTotal =
                    feeds.fold<int>(0, (sum, f) => sum + f.unread);
                final children = <Widget>[
                  _FeedTile(
                    selected: selection.selectedFeedId == 'all',
                    title: '全部',
                    iconWidget: Icon(Icons.all_inbox_outlined,
                        size: 16, color: BiuTokens.purple),
                    unread: unreadTotal,
                    onTap: () => controller.selectFeed('all'),
                  ),
                  if (feeds.isEmpty)
                    const _EmptyFeeds()
                  else
                    ...feeds.map(
                      (f) => _FeedTile(
                        selected: selection.selectedFeedId == f.id,
                        title: f.title,
                        iconWidget: _FeedFavicon(feed: f, creds: creds),
                        unread: f.unread,
                        forced: f.forced,
                        kind: f.kind,
                        onTap: () => controller.selectFeed(f.id),
                        // M11.4: 强制订阅成员不可删, 长按改为提示.
                        onLongPress: f.forced
                            ? () => ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(
                                      content: Text('该订阅由组织管理员强制订阅，无法取消'),
                                      duration: Duration(seconds: 2)),
                                )
                            : () => _confirmRemove(context, ref, f),
                      ),
                    ),
                ];
                return ListView(
                  padding: const EdgeInsets.symmetric(
                      vertical: BiuTokens.space2),
                  children: children,
                );
              },
            ),
          ),
          Container(
            padding: const EdgeInsets.all(BiuTokens.space3),
            decoration: BoxDecoration(
              border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
            ),
            child: SizedBox(
              width: double.infinity,
              child: OutlinedButton.icon(
                icon: const Icon(Icons.add, size: 16),
                label: const Text('添加订阅'),
                onPressed: () => showAddFeedSheet(context, ref),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _confirmRemove(
      BuildContext context, WidgetRef ref, Feed feed) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('取消订阅'),
        content: Text('确认取消订阅 ${feed.title}？此操作不可撤销。'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消')),
          FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('确认')),
        ],
      ),
    );
    if (ok != true) return;
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      await actions.feedsRemove(feed.id);
      ref.refreshFeeds();
      ref.refreshEntries();
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('取消订阅失败: $e')),
      );
    }
  }
}

class _FeedTile extends StatelessWidget {
  const _FeedTile({
    required this.selected,
    required this.title,
    required this.iconWidget,
    required this.unread,
    required this.onTap,
    this.onLongPress,
    this.forced = false,
    this.kind = 'rss',
  });

  final bool selected;
  final String title;
  final Widget iconWidget;
  final int unread;
  final bool forced;
  final String kind; // M13.1 — 来源类型角标
  final VoidCallback onTap;
  final VoidCallback? onLongPress;

  // M13 — 非 RSS 来源的小角标 (公众号 / X / 播客). 返回 null 表示普通 RSS.
  ({IconData icon, String label})? get _kindBadge {
    switch (kind) {
      case 'wechat':
        return (icon: Icons.chat_outlined, label: '公众号');
      case 'x':
        return (icon: Icons.alternate_email, label: 'X 用户');
      case 'podcast':
        return (icon: Icons.podcasts_outlined, label: '播客');
      default:
        return null;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space2, vertical: 1),
      child: Material(
        color: selected ? BiuTokens.purpleSoft : Colors.transparent,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: InkWell(
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          onTap: onTap,
          onLongPress: onLongPress,
          child: Padding(
            padding: const EdgeInsets.symmetric(
                horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
            child: Row(
              children: [
                SizedBox(
                  width: 16,
                  height: 16,
                  child: Center(child: iconWidget),
                ),
                const SizedBox(width: BiuTokens.space2),
                Expanded(
                  child: Text(
                    title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: selected
                          ? FontWeight.w600
                          : FontWeight.w500,
                      color: selected ? BiuTokens.purple : BiuTokens.text,
                    ),
                  ),
                ),
                if (_kindBadge != null) ...[
                  const SizedBox(width: 4),
                  Tooltip(
                    message: _kindBadge!.label,
                    child: Icon(_kindBadge!.icon,
                        size: 12, color: BiuTokens.textMuted),
                  ),
                ],
                if (forced) ...[
                  const SizedBox(width: 4),
                  Tooltip(
                    message: '由组织订阅',
                    child: Icon(Icons.groups,
                        size: 13, color: BiuTokens.textMuted),
                  ),
                ],
                if (unread > 0) ...[
                  const SizedBox(width: BiuTokens.space2),
                  _UnreadPill(count: unread, selected: selected),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _UnreadPill extends StatelessWidget {
  const _UnreadPill({required this.count, required this.selected});
  final int count;
  final bool selected;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: selected ? BiuTokens.purple : BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      ),
      child: Text(
        count > 99 ? '99+' : '$count',
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w600,
          color: selected ? Colors.white : BiuTokens.textSecondary,
        ),
      ),
    );
  }
}

class _FeedFavicon extends StatelessWidget {
  const _FeedFavicon({required this.feed, required this.creds});
  final Feed feed;
  final HubCredentials? creds;

  @override
  Widget build(BuildContext context) {
    final (url, headers) = resolveAppIcon(feed.iconUrl, creds);
    if (url != null) {
      return ClipRRect(
        borderRadius: BorderRadius.circular(3),
        child: Image.network(
          url,
          headers: headers,
          width: 16,
          height: 16,
          fit: BoxFit.cover,
          errorBuilder: (_, _, _) => _LetterAvatar(text: feed.title),
        ),
      );
    }
    return _LetterAvatar(text: feed.title);
  }
}

class _LetterAvatar extends StatelessWidget {
  const _LetterAvatar({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final letter = text.isEmpty ? '?' : text.characters.first.toUpperCase();
    return Container(
      width: 16,
      height: 16,
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(3),
      ),
      alignment: Alignment.center,
      child: Text(
        letter,
        style: TextStyle(
          fontSize: 10,
          fontWeight: FontWeight.w600,
          color: BiuTokens.textSecondary,
        ),
      ),
    );
  }
}

class _FeedsSkeleton extends StatelessWidget {
  const _FeedsSkeleton();
  @override
  Widget build(BuildContext context) {
    return ListView.builder(
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      itemCount: 6,
      itemBuilder: (_, _) => Padding(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
        child: Row(
          children: [
            Container(
              width: 16,
              height: 16,
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(3),
              ),
            ),
            const SizedBox(width: BiuTokens.space2),
            Expanded(
              child: Container(
                height: 12,
                decoration: BoxDecoration(
                  color: BiuTokens.surfaceMuted,
                  borderRadius: BorderRadius.circular(3),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyFeeds extends StatelessWidget {
  const _EmptyFeeds();
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        children: [
          Icon(Icons.rss_feed, size: 32, color: BiuTokens.textMuted),
          const SizedBox(height: BiuTokens.space3),
          Text(
            '还没有订阅',
            style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w500,
                color: BiuTokens.textSecondary),
          ),
          const SizedBox(height: BiuTokens.space1),
          Text(
            '点击下方“添加订阅”开始',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
          ),
        ],
      ),
    );
  }
}

class _ErrorRow extends StatelessWidget {
  const _ErrorRow({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '加载失败',
            style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w600,
                color: BiuTokens.error),
          ),
          const SizedBox(height: BiuTokens.space1),
          SelectableText(
            message,
            style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
          ),
          const SizedBox(height: BiuTokens.space3),
          OutlinedButton(onPressed: onRetry, child: const Text('重试')),
        ],
      ),
    );
  }
}
