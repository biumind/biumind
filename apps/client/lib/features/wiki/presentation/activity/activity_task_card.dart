/// Full-card renderer for one Activity Feed task —— B2.7 简化版。
///
/// 作为简化版，本版本去掉了：
///   - cancel 按钮 / 优化锁（依赖 ingest store 接口的 cancel 联动 — B2.6.x 后做）
///   - retry 重新 enqueue 按钮（同上）
///   - "View timeline" 跳转按钮（B2.6 ingest_stream_page 已经存在路径，
///     等 activity 真正接通 ingest 后再加）
///
/// 当前展示：kind icon + label + status badge + 详情行 + 失败时的错误块。
library;

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import '../../../../core/ui/biu_card.dart';
import 'activity_kind_icon.dart';
import 'activity_state.dart';

class ActivityTaskCard extends StatelessWidget {
  const ActivityTaskCard({
    super.key,
    required this.task,
    required this.projectId,
  });

  final ActivityTask task;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    final visual = activityKindVisual(task.kind);
    final t = task;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: BiuCard(
        lift: 0,
        padding: const EdgeInsets.all(12),
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: <Widget>[
            Row(
              children: <Widget>[
                Icon(visual.icon, size: 14, color: visual.color),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    t.label,
                    style: TextStyle(
                      fontSize: 13,
                      color: BiuTokens.text,
                      fontWeight: FontWeight.w500,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                _StatusBadge(status: t.status),
                // 本机解析任务（processor=client 镜像，docproc §3.5）：
                // activity API 的 summary 带 processor 时标注「本机」。
                if (t.summary['processor'] == 'client') ...[
                  const SizedBox(width: 4),
                  const _ProcessorBadge(),
                ],
              ],
            ),
            const SizedBox(height: 6),
            _DetailLine(task: t),
            if (t.status == ActivityStatus.failed) ...[
              const SizedBox(height: 6),
              _ErrorBlock(task: t),
            ],
          ],
        ),
      ),
    );
  }
}

class _StatusBadge extends StatelessWidget {  const _StatusBadge({required this.status});
  final ActivityStatus status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      ActivityStatus.running => ('进行中', BiuTokens.purple),
      ActivityStatus.done => ('完成', BiuTokens.success),
      ActivityStatus.failed => ('失败', BiuTokens.error),
      ActivityStatus.cancelled => ('已取消', BiuTokens.textMuted),
      ActivityStatus.unknown => ('—', BiuTokens.textMuted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 10,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

/// 「本机」标签：processor=client 的本机解析镜像任务（docproc §3.5）。
class _ProcessorBadge extends StatelessWidget {
  const _ProcessorBadge();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: BiuTokens.purple.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: BiuTokens.purple.withValues(alpha: 0.3)),
      ),
      child: Text(
        '本机',
        style: TextStyle(
          color: BiuTokens.purple,
          fontSize: 10,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _DetailLine extends StatelessWidget {
  const _DetailLine({required this.task});
  final ActivityTask task;

  @override
  Widget build(BuildContext context) {
    final summary = task.summary;
    final phase = task.rawPhase;
    final fragments = <String>[];
    if (phase != null && phase.isNotEmpty) fragments.add(phase);
    switch (task.kind) {
      case ActivityKind.ingest:
        final completed = summary['pages_completed'];
        final total = summary['pages_planned'];
        if (completed is int && total is int) {
          fragments.add('$completed / $total 页');
        }
      case ActivityKind.research:
        final stage = summary['stage'];
        if (stage is String && stage.isNotEmpty) fragments.add(stage);
      case ActivityKind.lint:
      case ActivityKind.dedup:
      case ActivityKind.sweep:
      case ActivityKind.unknown:
        break;
    }
    if (fragments.isEmpty) {
      fragments.add(_relativeTime(task.lastUpdatedAt));
    } else {
      fragments.add(_relativeTime(task.lastUpdatedAt));
    }
    return Text(
      fragments.join(' · '),
      style: TextStyle(color: BiuTokens.textMuted, fontSize: 11),
      overflow: TextOverflow.ellipsis,
    );
  }
}

class _ErrorBlock extends StatelessWidget {
  const _ErrorBlock({required this.task});
  final ActivityTask task;

  @override
  Widget build(BuildContext context) {
    final err = task.summary['error'] ?? task.summary['message'] ?? '未知错误';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: BiuTokens.errorSoft,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Text(
        '$err',
        style: TextStyle(color: BiuTokens.error, fontSize: 11),
      ),
    );
  }
}

String _relativeTime(DateTime t) {
  final now = DateTime.now();
  final d = now.difference(t);
  if (d.inSeconds < 5) return '刚刚';
  if (d.inSeconds < 60) return '${d.inSeconds} 秒前';
  if (d.inMinutes < 60) return '${d.inMinutes} 分钟前';
  if (d.inHours < 24) return '${d.inHours} 小时前';
  return '${d.inDays} 天前';
}
