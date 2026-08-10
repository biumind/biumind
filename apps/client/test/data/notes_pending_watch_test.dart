// pending 标志随订阅期 outbox 变化刷新的回归测试（N2 bugfix）。
//
// 修复前：watchNotebooks/watchNotes/watchNotesForTag/watchTags 在订阅时
// 一次性 await pending 集合，订阅期间 outbox 变化不刷新，pendingCreate /
// pendingUpdate 过期。修复后：dao.watchOutbox() 作触发流与实体流 combine，
// enqueue/flush 都会推动重算。
//
// 形态对齐 notes_local_test.dart：AppDb.memory + 真 NotesRepository
// （NotesClient 指向不可达地址 —— watch 流只走本地 Drift，不发网络）。

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:flutter_test/flutter_test.dart';

Future<void> _waitFor(bool Function() cond, String what) async {
  for (var i = 0; i < 300; i++) {
    if (cond()) return;
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  fail('timeout waiting for: $what');
}

void main() {
  late AppDb db;
  late NotesDao dao;
  late NotesRepository repo;

  setUp(() {
    db = AppDb.memory();
    dao = NotesDao(db, scope: 'test-scope');
    // 不可达地址即可：本组测试只走本地 Drift 流，不触发 HTTP。
    repo = NotesRepository(
      dao: dao,
      client: api.NotesClient(Uri.parse('http://127.0.0.1:1'), 'tok'),
    );
  });

  tearDown(() async {
    await db.close();
  });

  test('watchNotes：订阅期间 updateNote 后 pendingUpdate 刷新为 true，'
      'outbox 清掉后回落 false', () async {
    await dao.upsertNote(LocalNote(
      id: 'n1',
      notebookId: null,
      title: 't1',
      contentMd: '',
      isTodo: false,
      todoCompletedAt: null,
      position: 0,
      version: 1,
      trashed: false,
      trashedAt: null,
      updatedAt: DateTime.now().toUtc(),
      ownerKey: 'test-scope',
    ));

    final emissions = <List<RepoNote>>[];
    final sub = repo.watchNotes().listen(emissions.add);
    addTearDown(sub.cancel);

    await _waitFor(() => emissions.isNotEmpty, '首次 emission');
    expect(emissions.last.single.pendingUpdate, isFalse);
    expect(emissions.last.single.pendingCreate, isFalse);

    // 订阅期间 enqueue 一条 update_note outbox op。
    await repo.updateNote('n1', title: 't1-updated');
    await _waitFor(
      () => emissions.last.single.pendingUpdate,
      'pendingUpdate 刷新为 true',
    );
    expect(emissions.last.single.title, 't1-updated');

    // 模拟 flush 成功：outbox 删掉后标志应回落。
    final outbox = await dao.allOutbox();
    expect(outbox.single.op, NoteOutboxOp.updateNote);
    await dao.deleteOutbox(outbox.single.id);
    await _waitFor(
      () => !emissions.last.single.pendingUpdate,
      'pendingUpdate 回落 false',
    );
  });

  test('watchNotes：订阅期间 createNote 后新笔记 pendingCreate 为 true',
      () async {
    final emissions = <List<RepoNote>>[];
    final sub = repo.watchNotes().listen(emissions.add);
    addTearDown(sub.cancel);

    await _waitFor(() => emissions.isNotEmpty, '首次 emission');
    expect(emissions.last, isEmpty);

    final created = await repo.createNote(title: 'new');
    await _waitFor(
      () => emissions.last.any((n) => n.id == created.id && n.pendingCreate),
      '新笔记 pendingCreate 为 true',
    );
  });

  test('watchNotebooks：订阅期间 createNotebook 后 pendingCreate 刷新',
      () async {
    final emissions = <List<RepoNotebook>>[];
    final sub = repo.watchNotebooks().listen(emissions.add);
    addTearDown(sub.cancel);

    await _waitFor(() => emissions.isNotEmpty, '首次 emission');
    expect(emissions.last, isEmpty);

    final created = await repo.createNotebook('工作');
    await _waitFor(
      () => emissions.last.any((nb) => nb.id == created.id && nb.pendingCreate),
      '笔记本 pendingCreate 为 true',
    );

    final outbox = await dao.allOutbox();
    await dao.deleteOutbox(outbox.single.id);
    await _waitFor(
      () => emissions.last.every((nb) => !nb.pendingCreate),
      'flush 后 pendingCreate 回落',
    );
  });

  test('watchTags：订阅期间 createTag 后 pendingCreate 刷新', () async {
    final emissions = <List<RepoTag>>[];
    final sub = repo.watchTags().listen(emissions.add);
    addTearDown(sub.cancel);

    await _waitFor(() => emissions.isNotEmpty, '首次 emission');
    expect(emissions.last, isEmpty);

    final created = await repo.createTag('标签A');
    await _waitFor(
      () => emissions.last.any((t) => t.id == created.id && t.pendingCreate),
      '标签 pendingCreate 为 true',
    );
  });
}
