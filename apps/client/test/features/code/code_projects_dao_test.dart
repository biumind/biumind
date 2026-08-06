// CodeProjectsDao 单测 —— 在内存 Drift 上验证 CRUD + touch + setHidden + 排序。

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/code/data/code_projects_dao.dart';
import 'package:biumind/features/code/domain/project.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDb db;
  late CodeProjectsDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = CodeProjectsDao(db);
  });

  tearDown(() async => db.close());

  CodeProject mk(String id, String name, int openedMs) => CodeProject(
        id: id,
        name: name,
        path: '/repos/$name',
        lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(openedMs),
      );

  test('upsert + loadAll orders by lastOpenedAt desc', () async {
    await dao.upsert(mk('a', 'alpha', 1000));
    await dao.upsert(mk('b', 'beta', 3000));
    await dao.upsert(mk('c', 'gamma', 2000));

    final all = await dao.loadAll();
    expect(all.map((p) => p.id).toList(), ['b', 'c', 'a']);
    expect(all.first.path, '/repos/beta');
  });

  test('upsert replaces existing row by id', () async {
    await dao.upsert(mk('a', 'alpha', 1000));
    await dao.upsert(mk('a', 'alpha-renamed', 1000));
    final all = await dao.loadAll();
    expect(all.length, 1);
    expect(all.single.name, 'alpha-renamed');
  });

  test('touch updates lastOpenedAt (reorders)', () async {
    await dao.upsert(mk('a', 'alpha', 1000));
    await dao.upsert(mk('b', 'beta', 2000));
    await dao.touch('a', DateTime.fromMillisecondsSinceEpoch(5000));
    final all = await dao.loadAll();
    expect(all.first.id, 'a');
  });

  test('setHidden flips hiddenFromRail', () async {
    await dao.upsert(mk('a', 'alpha', 1000));
    await dao.setHidden('a', true);
    final all = await dao.loadAll();
    expect(all.single.hiddenFromRail, true);
  });

  test('deleteById removes row', () async {
    await dao.upsert(mk('a', 'alpha', 1000));
    await dao.deleteById('a');
    expect(await dao.loadAll(), isEmpty);
  });

  test('branch + avatarColor round-trip', () async {
    await dao.upsert(CodeProject(
      id: 'a',
      name: 'alpha',
      path: '/repos/alpha',
      branch: 'main',
      lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(1000),
      avatarColor: '#ff8800',
    ));
    final p = (await dao.loadAll()).single;
    expect(p.branch, 'main');
    expect(p.avatarColor, '#ff8800');
  });
}
