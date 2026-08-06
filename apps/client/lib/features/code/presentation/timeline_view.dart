// TimelineView — 跨项目任务时间线(M1)。
//
// 今天/昨天/更早 一级分组,组内按项目二级分组。点任务 → 切到该项目并激活该任务。
// 数据纯本地聚合(buildTimeline)。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../application/projects_controller.dart';
import '../application/tasks_controller.dart';
import '../application/timeline_builder.dart';
import '../domain/code_task.dart';

class CodeTimelineView extends ConsumerWidget {
  const CodeTimelineView({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tasks = ref.watch(codeTasksProvider);
    final projects = ref.watch(codeProjectsControllerProvider);
    final names = {for (final p in projects) p.id: p.name};
    final sections = buildTimeline(tasks, names, DateTime.now());

    if (sections.isEmpty) {
      return Center(
        child: Text('还没有任务',
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant)),
      );
    }

    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      children: [
        for (final section in sections) ...[
          _BucketHeader(bucket: section.bucket),
          for (final group in section.groups) ...[
            _ProjectHeader(name: group.projectName),
            ...group.tasks.map((t) => _TaskTile(task: t, projectId: group.projectId)),
            const SizedBox(height: 8),
          ],
        ],
      ],
    );
  }
}

class _BucketHeader extends StatelessWidget {
  const _BucketHeader({required this.bucket});
  final TimelineBucket bucket;

  @override
  Widget build(BuildContext context) {
    final label = switch (bucket) {
      TimelineBucket.today => '今天',
      TimelineBucket.yesterday => '昨天',
      TimelineBucket.earlier => '更早',
    };
    return Padding(
      padding: const EdgeInsets.only(top: 16, bottom: 4),
      child: Text(label,
          style: Theme.of(context)
              .textTheme
              .titleSmall
              ?.copyWith(fontWeight: FontWeight.w700)),
    );
  }
}

class _ProjectHeader extends StatelessWidget {
  const _ProjectHeader({required this.name});
  final String name;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 2, left: 4),
      child: Text(name,
          style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant)),
    );
  }
}

class _TaskTile extends ConsumerWidget {
  const _TaskTile({required this.task, required this.projectId});
  final CodeTask task;
  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 8),
      leading: _StatusIcon(status: task.status),
      title: Text(task.title.isEmpty ? task.prompt : task.title,
          maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: Text(_relativeTime(taskSortTime(task)),
          style: Theme.of(context).textTheme.bodySmall),
      onTap: projectId.isEmpty
          ? null // 未归属任务无项目可切
          : () {
              ref.read(activeCodeProjectIdProvider.notifier).state = projectId;
              ref.read(codeProjectsControllerProvider.notifier).touch(projectId);
              ref.read(activeCodeTaskIdProvider.notifier).state = task.id;
            },
    );
  }
}

class _StatusIcon extends StatelessWidget {
  const _StatusIcon({required this.status});
  final CodeTaskStatus status;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final (IconData icon, Color color) = switch (status) {
      CodeTaskStatus.running => (Icons.bolt, scheme.primary),
      CodeTaskStatus.inputRequired => (Icons.help_outline, Colors.orange),
      CodeTaskStatus.done => (Icons.check_circle_outline, Colors.green),
      CodeTaskStatus.failed => (Icons.error_outline, scheme.error),
      CodeTaskStatus.interrupted => (Icons.pause_circle_outline, scheme.outline),
      CodeTaskStatus.detached => (Icons.link_off, Colors.orange),
      CodeTaskStatus.paused => (Icons.pause_circle_outline, scheme.outline),
      CodeTaskStatus.queued => (Icons.schedule, scheme.outline),
    };
    return Icon(icon, size: 16, color: color);
  }
}

String _relativeTime(DateTime t) {
  final now = DateTime.now();
  final diff = now.difference(t);
  if (diff.inMinutes < 1) return '刚刚';
  if (diff.inMinutes < 60) return '${diff.inMinutes} 分钟前';
  if (diff.inHours < 24) return '${diff.inHours} 小时前';
  return '${diff.inDays} 天前';
}
