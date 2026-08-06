// SseCursorsDao 单测 — v2-4 last-event-id 持久化层.

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/sse/sse_cursors_dao.dart';

void main() {
  late AppDb db;
  late SseCursorsDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = SseCursorsDao(db);
  });

  tearDown(() async {
    await db.close();
  });

  test('load 未写过返 null', () async {
    expect(await dao.load('aigc.tasks'), isNull);
  });

  test('save → load 来回', () async {
    await dao.save('aigc.tasks', '01HXY-9');
    expect(await dao.load('aigc.tasks'), '01HXY-9');
  });

  test('save 同 scope 多次 last-write-wins', () async {
    await dao.save('aigc.tasks', 'OLD');
    await dao.save('aigc.tasks', 'NEW');
    expect(await dao.load('aigc.tasks'), 'NEW');
  });

  test('多 scope 互不干扰', () async {
    await dao.save('aigc.tasks', 'A1');
    await dao.save('skills.events', 'B2');
    expect(await dao.load('aigc.tasks'), 'A1');
    expect(await dao.load('skills.events'), 'B2');
  });

  test('clear 删 cursor 后 load 返 null', () async {
    await dao.save('aigc.tasks', 'X');
    await dao.clear('aigc.tasks');
    expect(await dao.load('aigc.tasks'), isNull);
  });

  test('clearAll 清所有 scope (登出防下个用户用上个用户 cursor)', () async {
    await dao.save('aigc.tasks', 'A1');
    await dao.save('skills.events', 'B2');
    await dao.save('sidebar.layout', 'C3');
    await dao.clearAll();
    expect(await dao.load('aigc.tasks'), isNull);
    expect(await dao.load('skills.events'), isNull);
    expect(await dao.load('sidebar.layout'), isNull);
  });
}
