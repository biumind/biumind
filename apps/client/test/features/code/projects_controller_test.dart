// CodeProjectsController 单测 —— 用真实内存 Drift DAO 验证添加/去重/touch 重排/
// 隐藏/删除。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/code/application/projects_controller.dart';
import 'package:biumind/features/code/data/code_projects_dao.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDb db;
  late CodeProjectsController ctrl;

  setUp(() {
    db = AppDb.memory();
    ctrl = CodeProjectsController(CodeProjectsDao(db));
  });

  tearDown(() async => db.close());

  test('addProjectByPath derives name from basename and activates list', () async {
    final p = await ctrl.addProjectByPath('/Users/me/repos/myapp');
    expect(p.name, 'myapp');
    expect(ctrl.state.length, 1);
    expect(ctrl.state.single.path, '/Users/me/repos/myapp');
  });

  test('adding same path twice does not duplicate', () async {
    final a = await ctrl.addProjectByPath('/repos/x');
    final b = await ctrl.addProjectByPath('/repos/x');
    expect(a.id, b.id);
    expect(ctrl.state.length, 1);
  });

  test('touch reorders most-recent-first', () async {
    final a = await ctrl.addProjectByPath('/repos/a');
    await ctrl.addProjectByPath('/repos/b'); // b now newest, at front
    expect(ctrl.state.first.path, '/repos/b');
    await ctrl.touch(a.id);
    expect(ctrl.state.first.id, a.id, reason: 'touched project moves to front');
  });

  test('setHidden marks hiddenFromRail', () async {
    final a = await ctrl.addProjectByPath('/repos/a');
    await ctrl.setHidden(a.id, true);
    expect(ctrl.state.single.hiddenFromRail, true);
  });

  test('remove deletes from state and DAO', () async {
    final a = await ctrl.addProjectByPath('/repos/a');
    await ctrl.remove(a.id);
    expect(ctrl.state, isEmpty);
    final reloaded = await CodeProjectsDao(db).loadAll();
    expect(reloaded, isEmpty);
  });

  test('trailing slash basename', () async {
    final p = await ctrl.addProjectByPath('/repos/trailing/');
    expect(p.name, 'trailing');
  });

  test('new project sorts to front (smaller sortIndex)', () async {
    await ctrl.addProjectByPath('/repos/a');
    await ctrl.addProjectByPath('/repos/b');
    // b added last → smallest sortIndex → front after reload-by-order
    final reloaded = await CodeProjectsDao(db).loadAll();
    expect(reloaded.first.path, '/repos/b');
  });

  test('setBranch updates and persists branch', () async {
    final a = await ctrl.addProjectByPath('/repos/a');
    await ctrl.setBranch(a.id, 'feature/x');
    expect(ctrl.state.single.branch, 'feature/x');
    final reloaded = await CodeProjectsDao(db).loadAll();
    expect(reloaded.single.branch, 'feature/x');
  });

  test('reorderVisible moves item and persists order', () async {
    await ctrl.addProjectByPath('/repos/a'); // sortIndex 0
    await ctrl.addProjectByPath('/repos/b'); // -1, front
    await ctrl.addProjectByPath('/repos/c'); // -2, front
    // current visible order: c, b, a
    expect(ctrl.state.map((p) => p.path), ['/repos/c', '/repos/b', '/repos/a']);
    // move c (index 0) to the end (newIndex 3 per ReorderableList convention)
    await ctrl.reorderVisible(0, 3);
    expect(ctrl.state.map((p) => p.path), ['/repos/b', '/repos/a', '/repos/c']);
    // persisted: reload from DAO matches
    final reloaded = await CodeProjectsDao(db).loadAll();
    expect(reloaded.map((p) => p.path), ['/repos/b', '/repos/a', '/repos/c']);
  });
}
