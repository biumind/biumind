// Right pane of the radar tab — timeline of hits for the currently
// selected rule (or all rules).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';

class HitsPane extends ConsumerWidget {
  const HitsPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final selection = ref.watch(rssSelectionProvider);
    final query = HitsQuery(
      ruleId: selection.selectedRuleId,
      unreadOnly: selection.radarUnreadOnly,
    );
    final hitsAsync = ref.watch(hitsProvider(query));

    return Container(
      color: BiuTokens.bg,
      child: Column(
        children: [
          if (selection.selectedRuleId != null)
            _ActionRunsExpander(ruleId: selection.selectedRuleId!),
          Expanded(
            child: hitsAsync.when(
        loading: () => const _Skeleton(),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(BiuTokens.space5),
            child: SelectableText('加载失败：$e',
                style: const TextStyle(fontSize: 13)),
          ),
        ),
        data: (hits) {
          if (hits.isEmpty) {
            return _Empty(unreadOnly: selection.radarUnreadOnly);
          }
          return ListView.separated(
            padding: const EdgeInsets.all(BiuTokens.space4),
            itemCount: hits.length,
            separatorBuilder: (_, _) => const SizedBox(height: BiuTokens.space2),
            itemBuilder: (_, i) {
              final h = hits[i];
              final overrideUnread = selection.hitReadOverride[h.id] ?? h.unread;
              return _HitTile(
                hit: h.copyWith(unread: overrideUnread),
                onMarkRead: () => _markRead(ref, h),
                onOpen: () => _open(context, ref, h),
              );
            },
          );
        },
      ),
          ),
        ],
      ),
    );
  }

  Future<void> _markRead(WidgetRef ref, Hit hit) async {
    if (!hit.unread) return;
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    ref.read(rssSelectionProvider.notifier).markHitRead(hit.id, true);
    try {
      await actions.hitsMarkRead(hit.id);
      ref.refreshHits();
    } catch (_) {
      ref.read(rssSelectionProvider.notifier).markHitRead(hit.id, false);
    }
  }

  Future<void> _open(BuildContext context, WidgetRef ref, Hit hit) async {
    final uri = safeParseUri(hit.url);
    if (uri == null) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('该命中没有可访问的 URL')));
      return;
    }
    await launchUrl(uri, mode: LaunchMode.externalApplication);
    await _markRead(ref, hit);
  }
}

class _HitTile extends StatelessWidget {
  const _HitTile({
    required this.hit,
    required this.onMarkRead,
    required this.onOpen,
  });
  final Hit hit;
  final VoidCallback onMarkRead;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: BiuTokens.surface,
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        onTap: onOpen,
        child: Container(
          padding: const EdgeInsets.all(BiuTokens.space4),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
            border: Border.all(
              color: hit.unread
                  ? severityColor(hit.severity).withValues(alpha: 0.4)
                  : BiuTokens.borderSubtle,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    margin: const EdgeInsets.only(top: 4),
                    width: 8,
                    height: 8,
                    decoration: BoxDecoration(
                      color: severityColor(hit.severity),
                      shape: BoxShape.circle,
                    ),
                  ),
                  const SizedBox(width: BiuTokens.space2),
                  Expanded(
                    child: Text(
                      hit.title,
                      style: TextStyle(
                        fontSize: 14,
                        height: 1.4,
                        fontWeight: hit.unread
                            ? FontWeight.w600
                            : FontWeight.w500,
                        color: hit.unread
                            ? BiuTokens.text
                            : BiuTokens.textSecondary,
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: BiuTokens.space2),
              Wrap(
                spacing: BiuTokens.space2,
                runSpacing: 4,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  _Tag(
                    label: hit.ruleName.isEmpty ? '未命名规则' : hit.ruleName,
                    color: severityColor(hit.severity),
                  ),
                  if (hit.source.isNotEmpty)
                    _Tag(label: _sourceLabel(hit.source)),
                  Text(
                    relativeTime(hit.hitAt),
                    style: TextStyle(
                        fontSize: 11, color: BiuTokens.textMuted),
                  ),
                ],
              ),
              const SizedBox(height: BiuTokens.space3),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  if (hit.unread)
                    TextButton.icon(
                      onPressed: onMarkRead,
                      icon: const Icon(Icons.done, size: 14),
                      label: const Text('标记已读'),
                    ),
                  const SizedBox(width: BiuTokens.space2),
                  OutlinedButton.icon(
                    onPressed: onOpen,
                    icon: const Icon(Icons.open_in_new, size: 14),
                    label: const Text('打开'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _sourceLabel(String source) {
    if (source == 'rss') return 'RSS';
    if (source.startsWith('boards:')) return '榜单·${source.substring(7)}';
    return source;
  }
}

class _Tag extends StatelessWidget {
  const _Tag({required this.label, this.color});
  final String label;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final c = color ?? BiuTokens.textSecondary;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: c.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w500,
          color: c,
        ),
      ),
    );
  }
}

Color severityColor(String severity) => switch (severity) {
      'error' => BiuTokens.error,
      'warn'  => PriorityColors.medium,
      _       => PriorityColors.low,
    };

class _Skeleton extends StatelessWidget {
  const _Skeleton();
  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.all(BiuTokens.space4),
      itemCount: 4,
      separatorBuilder: (_, _) => const SizedBox(height: BiuTokens.space2),
      itemBuilder: (_, _) => Container(
        height: 100,
        decoration: BoxDecoration(
          color: BiuTokens.surfaceMuted,
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
      ),
    );
  }
}

class _Empty extends StatelessWidget {
  const _Empty({required this.unreadOnly});
  final bool unreadOnly;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.radar_outlined, size: 36, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text(unreadOnly ? '暂无未读命中' : '暂无命中',
                style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w500,
                    color: BiuTokens.textSecondary)),
            const SizedBox(height: BiuTokens.space1),
            Text('当 RSS / 榜单中出现匹配关键词时，命中会出现在此',
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

// M9.3 — 雷达 rule 选中时, hits 顶部加 "执行历史" 折叠区, 展示该 rule
// 最近 N 次 action 执行 (notify/wiki/task/skill ok/error + duration_ms).
class _ActionRunsExpander extends ConsumerWidget {
  const _ActionRunsExpander({required this.ruleId});
  final String ruleId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(actionRunsProvider(ruleId));
    return Theme(
      data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
      child: ExpansionTile(
        tilePadding:
            const EdgeInsets.symmetric(horizontal: BiuTokens.space4, vertical: 0),
        title: Text(
          '执行历史',
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w500,
            color: BiuTokens.textSecondary,
          ),
        ),
        leading: Icon(Icons.history, size: 16, color: BiuTokens.textSecondary),
        children: [
          async.when(
            loading: () => const Padding(
              padding: EdgeInsets.all(BiuTokens.space3),
              child: Center(
                child: SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            ),
            error: (e, _) => Padding(
              padding: const EdgeInsets.all(BiuTokens.space3),
              child: Text('加载失败: $e',
                  style: TextStyle(
                      fontSize: 11, color: BiuTokens.textSecondary)),
            ),
            data: (rows) {
              if (rows.isEmpty) {
                return Padding(
                  padding: const EdgeInsets.all(BiuTokens.space3),
                  child: Text('暂无执行记录',
                      style: TextStyle(
                          fontSize: 11, color: BiuTokens.textMuted)),
                );
              }
              return Column(
                children: rows
                    .take(20)
                    .map((r) => _ActionRunRow(row: r))
                    .toList(),
              );
            },
          ),
        ],
      ),
    );
  }
}

class _ActionRunRow extends StatelessWidget {
  const _ActionRunRow({required this.row});
  final Map<String, dynamic> row;

  @override
  Widget build(BuildContext context) {
    final type = row['action_type']?.toString() ?? '?';
    final status = row['status']?.toString() ?? 'ok';
    final dur = row['duration_ms'] is int ? row['duration_ms'] as int : 0;
    final err = row['error']?.toString() ?? '';
    final startedAt = row['started_at']?.toString() ?? '';
    final ok = status == 'ok';

    return Padding(
      padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space4, vertical: 4),
      child: Row(
        children: [
          Icon(
            ok ? Icons.check_circle : Icons.error,
            size: 12,
            color: ok ? Colors.greenAccent : Colors.redAccent,
          ),
          const SizedBox(width: 6),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
            decoration: BoxDecoration(
              color: BiuTokens.surfaceMuted,
              borderRadius: BorderRadius.circular(3),
            ),
            child: Text(type,
                style: const TextStyle(
                    fontSize: 10, fontWeight: FontWeight.w600)),
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              ok ? '${dur}ms · ${_relTime(startedAt)}' : err,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 10,
                color: ok ? BiuTokens.textSecondary : Colors.redAccent,
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _relTime(String iso) {
    final t = DateTime.tryParse(iso);
    if (t == null) return iso;
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 1) return '刚刚';
    if (d.inMinutes < 60) return '${d.inMinutes} 分钟前';
    if (d.inHours < 24) return '${d.inHours} 小时前';
    return '${d.inDays} 天前';
  }
}
