// buildTimeline 纯函数单测 —— 时间分桶 + 项目分组 + 排序。

import 'package:biumind/features/code/application/timeline_builder.dart';
import 'package:biumind/features/code/domain/code_task.dart';
import 'package:flutter_test/flutter_test.dart';

CodeTask t(String id, String? projectId, DateTime updated) => CodeTask(
      id: id,
      title: id,
      prompt: 'p',
      agent: AgentKind.biu,
      mode: PermissionMode.ask,
      status: CodeTaskStatus.done,
      events: const [],
      cost: const TaskCost(),
      createdAt: updated,
      updatedAt: updated,
      projectId: projectId,
    );

void main() {
  final now = DateTime(2026, 6, 26, 10, 0);
  final names = {'a': 'Alpha', 'b': 'Beta'};

  test('buckets into today/yesterday/earlier', () {
    final tasks = [
      t('t_today', 'a', DateTime(2026, 6, 26, 9)),
      t('t_yest', 'a', DateTime(2026, 6, 25, 23)),
      t('t_old', 'a', DateTime(2026, 6, 1)),
    ];
    final sections = buildTimeline(tasks, names, now);
    expect(sections.map((s) => s.bucket),
        [TimelineBucket.today, TimelineBucket.yesterday, TimelineBucket.earlier]);
  });

  test('empty buckets are omitted', () {
    final tasks = [t('t_old', 'a', DateTime(2026, 6, 1))];
    final sections = buildTimeline(tasks, names, now);
    expect(sections.length, 1);
    expect(sections.single.bucket, TimelineBucket.earlier);
  });

  test('groups by project within a bucket, newest project first', () {
    final tasks = [
      t('a1', 'a', DateTime(2026, 6, 26, 8)),
      t('b1', 'b', DateTime(2026, 6, 26, 9)), // newer → Beta group first
    ];
    final today = buildTimeline(tasks, names, now).single;
    expect(today.groups.map((g) => g.projectName), ['Beta', 'Alpha']);
  });

  test('tasks within a group sorted newest-first', () {
    final tasks = [
      t('a_old', 'a', DateTime(2026, 6, 26, 7)),
      t('a_new', 'a', DateTime(2026, 6, 26, 9)),
    ];
    final group = buildTimeline(tasks, names, now).single.groups.single;
    expect(group.tasks.map((x) => x.id), ['a_new', 'a_old']);
  });

  test('null-projectId tasks fall under 未归属项目', () {
    final tasks = [t('orphan', null, DateTime(2026, 6, 26, 9))];
    final group = buildTimeline(tasks, names, now).single.groups.single;
    expect(group.projectId, '');
    expect(group.projectName, '未归属项目');
  });

  test('unknown projectId shows 未知项目', () {
    final tasks = [t('x', 'ghost', DateTime(2026, 6, 26, 9))];
    final group = buildTimeline(tasks, names, now).single.groups.single;
    expect(group.projectName, '未知项目');
  });
}
