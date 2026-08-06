// Today tab — AI-curated daily front page. Default landing view of
// the RSS app. Layout (≥ 1280): big headline card 60% width + 4 small
// cards 2x2 grid + "missed" horizontal scroller + trends bars +
// stats footer.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';
import 'briefing_button.dart';

class TodayTab extends ConsumerWidget {
  const TodayTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(todayProvider);
    return RefreshIndicator(
      onRefresh: () async {
        ref.invalidate(todayProvider);
        await ref.read(todayProvider.future);
      },
      child: async.when(
        loading: () => const _TodaySkeleton(),
        error: (e, _) => _TodayError(
          message: '$e',
          onRetry: () => ref.invalidate(todayProvider),
        ),
        data: (picks) => _TodayBody(picks: picks),
      ),
    );
  }
}

class _TodayBody extends StatelessWidget {
  const _TodayBody({required this.picks});
  final TodayPicks picks;

  @override
  Widget build(BuildContext context) {
    final isWide = MediaQuery.of(context).size.width >= 1280;

    return ListView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      children: [
        _Header(picks: picks),
        const SizedBox(height: BiuTokens.space5),
        if (picks.headline.isEmpty)
          _EmptyState()
        else if (isWide)
          _WideHeadline(picks: picks)
        else
          _NarrowHeadline(picks: picks),
        if (picks.missed.isNotEmpty) ...[
          const SizedBox(height: BiuTokens.space6),
          _SectionTitle('你今天可能错过的', icon: Icons.history),
          const SizedBox(height: BiuTokens.space3),
          _MissedRow(missed: picks.missed),
        ],
        if (picks.trends.isNotEmpty) ...[
          const SizedBox(height: BiuTokens.space6),
          _SectionTitle('话题热度', icon: Icons.trending_up),
          const SizedBox(height: BiuTokens.space3),
          _TrendsBar(trends: picks.trends),
        ],
        const SizedBox(height: BiuTokens.space6),
        _StatsFooter(stats: picks.stats),
        const SizedBox(height: BiuTokens.space6),
        Center(
          child: Text(
            picks.generatedAt == null
                ? ''
                : '本页根据你的阅读偏好生成 · ${_relTime(picks.generatedAt!)}',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          ),
        ),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.picks});
  final TodayPicks picks;

  @override
  Widget build(BuildContext context) {
    final dateLabel = _formatDate(picks.generatedAt ?? DateTime.now());
    final unread = picks.stats.unreadTotal;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Today · $dateLabel',
                style: const TextStyle(
                  fontSize: 22,
                  fontWeight: FontWeight.w700,
                  height: 1.2,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                unread > 0
                    ? '你有 $unread 条未读 · AI 已挑出最值得看的 ${picks.headline.length} 条'
                    : '你已看完所有更新, 看看 AI 推荐的趋势吧',
                style: TextStyle(fontSize: 13, color: BiuTokens.textSecondary),
              ),
            ],
          ),
        ),
        const BriefingButton(),
      ],
    );
  }
}

class _WideHeadline extends StatelessWidget {
  const _WideHeadline({required this.picks});
  final TodayPicks picks;

  @override
  Widget build(BuildContext context) {
    final h = picks.headline;
    final big = h.first;
    final rest = h.length > 1 ? h.sublist(1).take(4).toList() : <TodayEntry>[];
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(flex: 6, child: _HeadlineCard(entry: big, big: true)),
        const SizedBox(width: BiuTokens.space5),
        Expanded(
          flex: 4,
          child: GridView.count(
            crossAxisCount: 2,
            childAspectRatio: 1.2,
            mainAxisSpacing: BiuTokens.space3,
            crossAxisSpacing: BiuTokens.space3,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            children: [
              for (final e in rest) _HeadlineCard(entry: e, big: false),
              if (rest.length < 4)
                for (var i = rest.length; i < 4; i++) const _PlaceholderCard(),
            ],
          ),
        ),
      ],
    );
  }
}

class _NarrowHeadline extends StatelessWidget {
  const _NarrowHeadline({required this.picks});
  final TodayPicks picks;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < picks.headline.length; i++) ...[
          _HeadlineCard(entry: picks.headline[i], big: i == 0),
          if (i < picks.headline.length - 1)
            const SizedBox(height: BiuTokens.space3),
        ],
      ],
    );
  }
}

class _HeadlineCard extends StatelessWidget {
  const _HeadlineCard({required this.entry, required this.big});
  final TodayEntry entry;
  final bool big;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final tint = scheme.primary.withValues(alpha: big ? 0.06 : 0.04);
    final border = scheme.primary.withValues(alpha: 0.16);
    return InkWell(
      onTap: () => _open(entry.url),
      borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
      child: Container(
        padding: EdgeInsets.all(big ? BiuTokens.space5 : BiuTokens.space4),
        decoration: BoxDecoration(
          color: tint,
          borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
          border: Border.all(color: border),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisAlignment: MainAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                if (entry.feedTitle.isNotEmpty)
                  Flexible(
                    child: Text(
                      entry.feedTitle,
                      style: TextStyle(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: scheme.primary,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                if (entry.publishedAt != null) ...[
                  const SizedBox(width: 6),
                  Text('· ${_relTime(entry.publishedAt!)}',
                      style:
                          TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
                ],
                const Spacer(),
                if (entry.aiImportance >= 2) _ImportanceDot(level: entry.aiImportance),
                if (entry.clusterSize > 1) ...[
                  const SizedBox(width: 6),
                  _ClusterChip(count: entry.clusterSize),
                ],
              ],
            ),
            SizedBox(height: big ? BiuTokens.space3 : BiuTokens.space2),
            Text(
              entry.title,
              maxLines: big ? 3 : 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: big ? 18 : 14,
                fontWeight: FontWeight.w700,
                height: 1.35,
              ),
            ),
            if (big && entry.aiTakeaway.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space3),
              Text(
                entry.aiTakeaway,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  fontSize: 13,
                  fontStyle: FontStyle.italic,
                  color: scheme.primary,
                  height: 1.45,
                ),
              ),
              const SizedBox(height: BiuTokens.space3),
              ...entry.aiBullets.take(3).map((b) => Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Padding(
                          padding: const EdgeInsets.only(top: 7, right: 6),
                          child: Container(
                            width: 4,
                            height: 4,
                            decoration: BoxDecoration(
                              color: scheme.primary.withValues(alpha: 0.5),
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
                              color: BiuTokens.textSecondary,
                            ),
                          ),
                        ),
                      ],
                    ),
                  )),
            ] else if (!big && entry.aiTakeaway.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                entry.aiTakeaway,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  fontSize: 11,
                  fontStyle: FontStyle.italic,
                  color: scheme.primary.withValues(alpha: 0.85),
                  height: 1.4,
                ),
              ),
            ],
            if (entry.aiTopics.isNotEmpty) ...[
              SizedBox(height: big ? BiuTokens.space3 : 6),
              Wrap(
                spacing: 4,
                runSpacing: 4,
                children: entry.aiTopics.take(3).map((t) => Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 6, vertical: 1),
                      decoration: BoxDecoration(
                        color: scheme.primary.withValues(alpha: 0.10),
                        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                      ),
                      child: Text(t,
                          style: TextStyle(
                              fontSize: 9.5,
                              color: scheme.primary,
                              fontWeight: FontWeight.w500)),
                    )).toList(),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _PlaceholderCard extends StatelessWidget {
  const _PlaceholderCard();
  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      alignment: Alignment.center,
      child: Text(
        '等待更多内容…',
        style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
      ),
    );
  }
}

class _ImportanceDot extends StatelessWidget {
  const _ImportanceDot({required this.level});
  final int level;
  @override
  Widget build(BuildContext context) {
    final color = level == 3 ? PriorityColors.high : PriorityColors.medium;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 6,
          height: 6,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 4),
        Text(
          level == 3 ? '重要' : '关注',
          style: TextStyle(
              fontSize: 10, fontWeight: FontWeight.w600, color: color),
        ),
      ],
    );
  }
}

class _ClusterChip extends StatelessWidget {
  const _ClusterChip({required this.count});
  final int count;
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: BiuTokens.borderSubtle,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
      ),
      child: Text(
        '另 ${count - 1} 个来源',
        style: TextStyle(
          fontSize: 10,
          color: BiuTokens.textSecondary,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle(this.text, {required this.icon});
  final String text;
  final IconData icon;
  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(icon, size: 16, color: BiuTokens.textSecondary),
        const SizedBox(width: 6),
        Text(text,
            style: const TextStyle(
                fontSize: 14, fontWeight: FontWeight.w600, height: 1.4)),
      ],
    );
  }
}

class _MissedRow extends StatelessWidget {
  const _MissedRow({required this.missed});
  final List<TodayEntry> missed;
  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 110,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: missed.length,
        separatorBuilder: (_, _) => const SizedBox(width: BiuTokens.space3),
        itemBuilder: (_, i) {
          final e = missed[i];
          return SizedBox(
            width: 280,
            child: InkWell(
              onTap: () => _open(e.url),
              borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
              child: Container(
                padding: const EdgeInsets.all(BiuTokens.space3),
                decoration: BoxDecoration(
                  color: BiuTokens.surfaceMuted,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
                  border: Border.all(color: BiuTokens.borderSubtle),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        if (e.feedTitle.isNotEmpty)
                          Flexible(
                            child: Text(
                              e.feedTitle,
                              style: TextStyle(
                                  fontSize: 10,
                                  fontWeight: FontWeight.w600,
                                  color: BiuTokens.textMuted),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        if (e.publishedAt != null) ...[
                          const SizedBox(width: 4),
                          Text('· ${_relTime(e.publishedAt!)}',
                              style: TextStyle(
                                  fontSize: 10, color: BiuTokens.textMuted)),
                        ],
                      ],
                    ),
                    const SizedBox(height: 4),
                    Expanded(
                      child: Text(
                        e.aiTakeaway.isNotEmpty ? e.aiTakeaway : e.title,
                        maxLines: 4,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                            fontSize: 12, height: 1.4, fontWeight: FontWeight.w500),
                      ),
                    ),
                  ],
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}

class _TrendsBar extends StatelessWidget {
  const _TrendsBar({required this.trends});
  final List<TodayTrend> trends;
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final maxCount = trends.fold<int>(1, (m, t) => t.count > m ? t.count : m);
    return Column(
      children: trends.map((t) {
        final ratio = t.count / maxCount;
        return Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Row(
            children: [
              SizedBox(
                width: 80,
                child: Text(
                  t.topic,
                  style: const TextStyle(
                      fontSize: 12, fontWeight: FontWeight.w500),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Expanded(
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(4),
                  child: LinearProgressIndicator(
                    value: ratio,
                    minHeight: 6,
                    backgroundColor: BiuTokens.borderSubtle,
                    color: scheme.primary,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              SizedBox(
                width: 28,
                child: Text(
                  '${t.count}',
                  textAlign: TextAlign.right,
                  style: TextStyle(
                      fontSize: 11, color: BiuTokens.textMuted),
                ),
              ),
            ],
          ),
        );
      }).toList(),
    );
  }
}

class _StatsFooter extends StatelessWidget {
  const _StatsFooter({required this.stats});
  final TodayStats stats;
  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: BiuTokens.space5,
      runSpacing: BiuTokens.space2,
      children: [
        _stat('今日已读', '${stats.readToday}/${stats.unreadTotal + stats.readToday}'),
        _stat('连续阅读', stats.streakDays > 0 ? '${stats.streakDays} 天 🔥' : '—'),
        _stat('本周沉到 Wiki', '${stats.wikiThisWeek} 条'),
      ],
    );
  }

  Widget _stat(String label, String value) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('$label · ',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
        Text(value,
            style: const TextStyle(
                fontSize: 12, fontWeight: FontWeight.w600)),
      ],
    );
  }
}

class _EmptyState extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 60),
      child: Column(
        children: [
          Icon(Icons.coffee_outlined, size: 56, color: BiuTokens.textMuted),
          const SizedBox(height: BiuTokens.space3),
          Text('AI 还在等更多内容',
              style: const TextStyle(
                  fontSize: 16, fontWeight: FontWeight.w500)),
          const SizedBox(height: BiuTokens.space2),
          Text(
              '订阅几个源, 等首批 entries 拉回来 AI 处理后, 这里会有头条',
              textAlign: TextAlign.center,
              style: TextStyle(
                  fontSize: 12, color: BiuTokens.textSecondary)),
        ],
      ),
    );
  }
}

class _TodayError extends StatelessWidget {
  const _TodayError({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;
  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      children: [
        const SizedBox(height: 80),
        Center(child: Icon(Icons.error_outline, color: BiuTokens.textMuted, size: 48)),
        const SizedBox(height: BiuTokens.space3),
        Center(child: Text('加载失败: $message',
            style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary))),
        const SizedBox(height: BiuTokens.space3),
        Center(child: OutlinedButton(onPressed: onRetry, child: const Text('重试'))),
      ],
    );
  }
}

class _TodaySkeleton extends StatelessWidget {
  const _TodaySkeleton();
  @override
  Widget build(BuildContext context) {
    Widget bar({double width = double.infinity, double height = 12}) {
      return Container(
        width: width,
        height: height,
        decoration: BoxDecoration(
          color: BiuTokens.borderSubtle,
          borderRadius: BorderRadius.circular(4),
        ),
      );
    }
    return ListView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      children: [
        bar(width: 200, height: 18),
        const SizedBox(height: 8),
        bar(width: 320, height: 12),
        const SizedBox(height: BiuTokens.space5),
        Container(
          height: 280,
          decoration: BoxDecoration(
            color: BiuTokens.surfaceMuted,
            borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
          ),
        ),
      ],
    );
  }
}

void _open(String url) {
  if (url.isEmpty) return;
  launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
}

String _relTime(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inMinutes < 1) return '刚刚';
  if (d.inMinutes < 60) return '${d.inMinutes} 分钟前';
  if (d.inHours < 24) return '${d.inHours} 小时前';
  if (d.inDays < 7) return '${d.inDays} 天前';
  return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')}';
}

String _formatDate(DateTime t) {
  final weekdays = ['一', '二', '三', '四', '五', '六', '日'];
  return '${t.year}-${t.month.toString().padLeft(2, '0')}-${t.day.toString().padLeft(2, '0')} 周${weekdays[t.weekday - 1]}';
}
