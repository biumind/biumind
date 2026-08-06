/// Single-line variant of an Activity Feed task — used for terminal
/// (done / cancelled) tasks whose duration was under the collapse
/// threshold. Failures 永远走 full card 让用户看到错误。
library;

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'activity_kind_icon.dart';
import 'activity_state.dart';

class ActivityCollapsedRow extends StatelessWidget {
  const ActivityCollapsedRow({super.key, required this.task});
  final ActivityTask task;

  @override
  Widget build(BuildContext context) {
    final visual = activityKindVisual(task.kind);
    final dur = task.duration;
    final durLabel = _formatDuration(dur);
    final summaryLine = _summaryLine(task);
    final isCancelled = task.status == ActivityStatus.cancelled;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        children: <Widget>[
          Icon(
            isCancelled ? Icons.do_not_disturb_on_outlined : Icons.check,
            size: 12,
            color: isCancelled ? BiuTokens.textMuted : BiuTokens.success,
          ),
          const SizedBox(width: 8),
          Icon(visual.icon, size: 11, color: visual.color),
          const SizedBox(width: 4),
          Expanded(
            child: Text(
              summaryLine,
              style: TextStyle(
                color: BiuTokens.textSecondary,
                fontSize: 12,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            durLabel,
            style: TextStyle(
              color: BiuTokens.textMuted,
              fontSize: 11,
              fontFeatures: const <FontFeature>[FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }

  String _summaryLine(ActivityTask t) {
    final s = t.summary;
    switch (t.kind) {
      case ActivityKind.lint:
        final added = s['issues_added'];
        final found = s['issues_found'];
        if (added is int) return '${t.label} · $added 新问题';
        if (found is int) return '${t.label} · $found 项';
        return t.label;
      case ActivityKind.dedup:
        final groups = s['groups'];
        final rewritten = s['rewritten_pages'];
        final deleted = s['deleted_pages'];
        if (groups is int) return '${t.label} · $groups 组';
        if (rewritten is int && deleted is int) {
          return '${t.label} · 重写 $rewritten / 删除 $deleted';
        }
        return t.label;
      case ActivityKind.sweep:
        final total =
            ((s['rule_resolved'] as int?) ?? 0) +
                ((s['llm_resolved'] as int?) ?? 0);
        return total > 0 ? '${t.label} · 已解决 $total' : t.label;
      case ActivityKind.research:
        final sources = s['sources_count'];
        if (sources is int) return '${t.label} · $sources 来源';
        return t.label;
      case ActivityKind.ingest:
        final completed = s['pages_completed'];
        if (completed is int) return '${t.label} · $completed 页';
        return t.label;
      case ActivityKind.unknown:
        return t.label;
    }
  }
}

String _formatDuration(Duration d) {
  if (d.inMilliseconds < 1000) return '${d.inMilliseconds} ms';
  if (d.inSeconds < 60) {
    final secs = (d.inMilliseconds / 1000).toStringAsFixed(1);
    return '${secs}s';
  }
  final m = d.inMinutes;
  final s = d.inSeconds % 60;
  return '${m}m ${s}s';
}
