// 跨项目任务时间线聚合(M1)。
//
// 纯函数 buildTimeline:按时间桶(今天/昨天/更早)一级分组,桶内按项目二级分组。
// 提取成纯函数便于单测;provider 在 projects_controller / 视图里组合 codeTasks +
// 项目名映射调用它。

import '../domain/code_task.dart';

/// 时间桶:今天 / 昨天 / 更早。
enum TimelineBucket { today, yesterday, earlier }

/// 一个项目在某时间桶内的任务组。
class TimelineProjectGroup {
  TimelineProjectGroup({
    required this.projectId,
    required this.projectName,
    required this.tasks,
  });

  /// 项目 id;null-projectId 老任务归到 id='' 的"未归属"组。
  final String projectId;
  final String projectName;
  final List<CodeTask> tasks;
}

/// 一个时间桶 section(含若干项目组)。
class TimelineSection {
  TimelineSection({required this.bucket, required this.groups});
  final TimelineBucket bucket;
  final List<TimelineProjectGroup> groups;
}

/// 任务排序/分桶用的时间:优先 updatedAt,回退 createdAt。
DateTime taskSortTime(CodeTask t) => t.updatedAt ?? t.createdAt;

/// 构建时间线。projectNames: projectId → 展示名(缺失/ null 归"未归属")。
/// now 显式传入(便于测试,也符合无 Date.now 的纯函数约定)。
/// 返回非空 section 列表,顺序固定 今天→昨天→更早;桶内项目组按其最新任务时间倒序;
/// 组内任务按时间倒序。
List<TimelineSection> buildTimeline(
  List<CodeTask> tasks,
  Map<String, String> projectNames,
  DateTime now,
) {
  final today = DateTime(now.year, now.month, now.day);
  final yesterday = today.subtract(const Duration(days: 1));

  TimelineBucket bucketOf(CodeTask t) {
    final ts = taskSortTime(t);
    final d = DateTime(ts.year, ts.month, ts.day);
    if (!d.isBefore(today)) return TimelineBucket.today;
    if (!d.isBefore(yesterday)) return TimelineBucket.yesterday;
    return TimelineBucket.earlier;
  }

  // bucket → projectId → tasks
  final byBucket = <TimelineBucket, Map<String, List<CodeTask>>>{};
  for (final t in tasks) {
    final b = bucketOf(t);
    final pid = t.projectId ?? '';
    (byBucket[b] ??= {}).putIfAbsent(pid, () => []).add(t);
  }

  final sections = <TimelineSection>[];
  for (final bucket in TimelineBucket.values) {
    final byProject = byBucket[bucket];
    if (byProject == null || byProject.isEmpty) continue;

    final groups = <TimelineProjectGroup>[];
    byProject.forEach((pid, list) {
      list.sort((a, b) => taskSortTime(b).compareTo(taskSortTime(a)));
      groups.add(TimelineProjectGroup(
        projectId: pid,
        projectName:
            pid.isEmpty ? '未归属项目' : (projectNames[pid] ?? '未知项目'),
        tasks: list,
      ));
    });
    // 项目组按其最新任务时间倒序。
    groups.sort((a, b) =>
        taskSortTime(b.tasks.first).compareTo(taskSortTime(a.tasks.first)));
    sections.add(TimelineSection(bucket: bucket, groups: groups));
  }
  return sections;
}
