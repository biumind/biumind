// Saved tab — entries the user marked star/pin/wiki/shared. Inner
// 4-tab switcher; each pulls marks_list with the matching mark.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';

class SavedTab extends ConsumerStatefulWidget {
  const SavedTab({super.key});
  @override
  ConsumerState<SavedTab> createState() => _SavedTabState();
}

class _SavedTabState extends ConsumerState<SavedTab>
    with SingleTickerProviderStateMixin {
  late final TabController _t;
  static const _tabs = <(String, String, IconData)>[
    ('star', '收藏', Icons.star_outline),
    ('wiki', '已沉 Wiki', Icons.bookmark_outline),
    ('pin', '钉住', Icons.push_pin_outlined),
    ('shared', '已分享', Icons.share_outlined),
  ];

  @override
  void initState() {
    super.initState();
    _t = TabController(length: _tabs.length, vsync: this);
  }

  @override
  void dispose() {
    _t.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Material(
          color: BiuTokens.bg,
          child: TabBar(
            controller: _t,
            isScrollable: true,
            tabs: _tabs
                .map((e) => Tab(icon: Icon(e.$3, size: 16), text: e.$2))
                .toList(),
          ),
        ),
        Expanded(
          child: TabBarView(
            controller: _t,
            children: _tabs.map((e) => _SavedList(mark: e.$1)).toList(),
          ),
        ),
      ],
    );
  }
}

class _SavedList extends ConsumerWidget {
  const _SavedList({required this.mark});
  final String mark;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(savedProvider(mark));
    return RefreshIndicator(
      onRefresh: () async {
        ref.invalidate(savedProvider(mark));
        await ref.read(savedProvider(mark).future);
      },
      child: async.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
            child: Text('加载失败: $e',
                style: TextStyle(color: BiuTokens.textSecondary))),
        data: (items) {
          if (items.isEmpty) {
            return ListView(
              padding: const EdgeInsets.all(BiuTokens.space5),
              children: [
                const SizedBox(height: 80),
                Center(
                  child: Icon(Icons.inbox_outlined,
                      size: 48, color: BiuTokens.textMuted),
                ),
                const SizedBox(height: BiuTokens.space2),
                Center(
                  child: Text('暂无内容',
                      style: TextStyle(color: BiuTokens.textSecondary)),
                ),
              ],
            );
          }
          return ListView.separated(
            padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
            itemCount: items.length,
            separatorBuilder: (_, _) => Divider(
                height: 1, color: BiuTokens.borderSubtle),
            itemBuilder: (_, i) => _Row(item: items[i]),
          );
        },
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.item});
  final SavedItem item;
  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () {
        if (item.url.isNotEmpty) {
          launchUrl(Uri.parse(item.url),
              mode: LaunchMode.externalApplication);
        }
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space4, vertical: BiuTokens.space3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                if (item.feedTitle.isNotEmpty)
                  Flexible(
                    child: Text(
                      item.feedTitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 11,
                          color: BiuTokens.textMuted,
                          fontWeight: FontWeight.w500),
                    ),
                  ),
                if (item.markedAt != null) ...[
                  const SizedBox(width: 4),
                  Text(
                    '· ${_relTime(item.markedAt!)}',
                    style: TextStyle(
                        fontSize: 11, color: BiuTokens.textMuted),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 4),
            Text(
              item.title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                  fontSize: 14, fontWeight: FontWeight.w600, height: 1.35),
            ),
            if (item.aiTakeaway.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                item.aiTakeaway,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                    fontSize: 12,
                    fontStyle: FontStyle.italic,
                    color: BiuTokens.textSecondary,
                    height: 1.4),
              ),
            ],
            if (item.aiTopics.isNotEmpty) ...[
              const SizedBox(height: 4),
              Wrap(
                spacing: 4,
                children: item.aiTopics
                    .take(3)
                    .map((t) => Container(
                          padding: const EdgeInsets.symmetric(
                              horizontal: 6, vertical: 1),
                          decoration: BoxDecoration(
                            color: BiuTokens.borderSubtle,
                            borderRadius:
                                BorderRadius.circular(BiuTokens.radiusXs),
                          ),
                          child: Text(t,
                              style: TextStyle(
                                  fontSize: 10,
                                  color: BiuTokens.textSecondary)),
                        ))
                    .toList(),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _relTime(DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 60) return '${d.inMinutes} 分钟前';
    if (d.inHours < 24) return '${d.inHours} 小时前';
    if (d.inDays < 7) return '${d.inDays} 天前';
    return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
  }
}
