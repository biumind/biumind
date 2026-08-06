// M1 任务作用域过滤的单测 —— scopeTasksToProject 纯函数。
// 验证多项目下任务按 projectId 严格隔离 + 无激活项目时为空。

import 'package:biumind/features/code/application/projects_controller.dart';
import 'package:biumind/features/code/domain/code_task.dart';
import 'package:flutter_test/flutter_test.dart';

CodeTask task(String id, String? projectId) => CodeTask(
      id: id,
      title: id,
      prompt: 'p',
      agent: AgentKind.biu,
      mode: PermissionMode.ask,
      status: CodeTaskStatus.queued,
      events: const [],
      cost: const TaskCost(),
      createdAt: DateTime(2026),
      projectId: projectId,
    );

void main() {
  final all = [
    task('t1', 'projA'),
    task('t2', 'projB'),
    task('t3', 'projA'),
    task('t4', null), // pre-M1 老任务,无归属
  ];

  test('filters strictly by active project', () {
    final scoped = scopeTasksToProject(all, 'projA');
    expect(scoped.map((t) => t.id), ['t1', 't3']);
  });

  test('other project sees only its own', () {
    expect(scopeTasksToProject(all, 'projB').map((t) => t.id), ['t2']);
  });

  test('no active project → empty', () {
    expect(scopeTasksToProject(all, null), isEmpty);
  });

  test('null-projectId legacy tasks belong to no project', () {
    expect(
      scopeTasksToProject(all, 'projA').any((t) => t.id == 't4'),
      false,
    );
    expect(
      scopeTasksToProject(all, 'projB').any((t) => t.id == 't4'),
      false,
    );
  });
}
