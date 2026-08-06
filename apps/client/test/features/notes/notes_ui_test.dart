// N2 待办视图排序 + 标签过滤 provider 组合测试。
//
// 形态对齐 test/data/notes_local_test.dart：AppDb.memory + 真
// NotesRepository（NotesClient 指向不可达地址 —— watch 流只走本地
// Drift，不发网络）。UI 层 provider 的组合逻辑（filter → repo 流 →
// 排序）在这里锁语义。

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

LocalNote _localNote(
  String id, {
  bool isTodo = false,
  DateTime? todoCompletedAt,
  double position = 0.0,
  DateTime? updatedAt,
}) =>
    LocalNote(
      id: id,
      notebookId: null,
      title: 't-$id',
      contentMd: '',
      isTodo: isTodo,
      todoCompletedAt: todoCompletedAt,
      position: position,
      version: 1,
      trashed: false,
      trashedAt: null,
      updatedAt: updatedAt ?? DateTime.now().toUtc(),
    );

RepoNote _repoNote(
  String id, {
  double position = 0.0,
  DateTime? todoCompletedAt,
}) =>
    RepoNote(
      id: id,
      title: id,
      contentMd: '',
      isTodo: true,
      todoCompletedAt: todoCompletedAt,
      position: position,
      version: 1,
      updatedAt: DateTime.now().toUtc(),
    );

void main() {
  late AppDb db;
  late NotesDao dao;
  late NotesRepository repo;
  late ProviderContainer container;

  setUp(() {
    db = AppDb.memory();
    dao = NotesDao(db);
    // 不可达地址即可：本组测试只走本地 Drift 流，不触发 HTTP。
    repo = NotesRepository(
      dao: dao,
      client: api.NotesClient(Uri.parse('http://127.0.0.1:1'), 'tok'),
    );
    container = ProviderContainer(overrides: <Override>[
      notesRepositoryProvider.overrideWithValue(repo),
    ]);
  });

  tearDown(() async {
    container.dispose();
    await db.close();
  });

  group('sortTodoNotes', () {
    test('未完成在前按 position 升序，已完成在后按完成时间倒序', () {
      final now = DateTime.now().toUtc();
      final sorted = sortTodoNotes(<RepoNote>[
        _repoNote('done-old', todoCompletedAt: now.subtract(const Duration(days: 2))),
        _repoNote('pending-b', position: 2.0),
        _repoNote('done-new', todoCompletedAt: now),
        _repoNote('pending-a', position: 1.0),
      ]);
      expect(
        sorted.map((n) => n.id).toList(),
        ['pending-a', 'pending-b', 'done-new', 'done-old'],
      );
    });
  });

  group('notesListProvider', () {
    test('todo 过滤：只含待办且按待办序（未完成 position 升序在前）', () async {
      final now = DateTime.now().toUtc();
      await dao.upsertNote(_localNote('plain'));
      await dao.upsertNote(_localNote('todo-p2', isTodo: true, position: 2.0));
      await dao.upsertNote(_localNote('todo-p1', isTodo: true, position: 1.0));
      await dao.upsertNote(_localNote('todo-done',
          isTodo: true, position: 0.0, todoCompletedAt: now));

      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.todo();
      final notes = await container.read(notesListProvider.future);

      expect(
        notes.map((n) => n.id).toList(),
        ['todo-p1', 'todo-p2', 'todo-done'],
        reason: '普通笔记被过滤；未完成按 position 升序在前，已完成沉底',
      );
    });

    test('tag 过滤：只含该标签的笔记（updatedAt 倒序）', () async {
      final now = DateTime.now().toUtc();
      await dao.upsertTag(const LocalNoteTag(id: 't1', name: '工作'));
      await dao.upsertNote(
          _localNote('n-old', updatedAt: now.subtract(const Duration(days: 1))));
      await dao.upsertNote(_localNote('n-new', updatedAt: now));
      await dao.upsertNote(_localNote('n-untagged', updatedAt: now));
      await dao.setNoteTags('n-old', ['t1']);
      await dao.setNoteTags('n-new', ['t1']);

      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.tag('t1');
      final notes = await container.read(notesListProvider.future);

      expect(notes.map((n) => n.id).toList(), ['n-new', 'n-old']);
    });

    test('过滤源互斥：选中标签即覆盖此前的待办过滤', () async {
      await dao.upsertNote(_localNote('n1', isTodo: true));
      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.todo();
      expect(container.read(notesFilterProvider).kind, NotesListKind.todo);

      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.tag('t1');
      final filter = container.read(notesFilterProvider);
      expect(filter.kind, NotesListKind.tag);
      expect(filter.tagId, 't1');

      final notes = await container.read(notesListProvider.future);
      expect(notes, isEmpty, reason: 't1 无关联笔记，标签过滤生效而非待办');
    });
  });

  group('noteTagIdsProvider', () {
    test('返回标签 id；setNoteTags 后 invalidate 拿到新值', () async {
      await dao.upsertTag(const LocalNoteTag(id: 't1', name: '工作'));
      await dao.upsertTag(const LocalNoteTag(id: 't2', name: '生活'));
      await dao.upsertNote(_localNote('n1'));
      await dao.setNoteTags('n1', ['t1']);

      expect(await container.read(noteTagIdsProvider('n1').future), ['t1']);

      await repo.setNoteTags('n1', ['t1', 't2']);
      container.invalidate(noteTagIdsProvider('n1'));
      expect(
        await container.read(noteTagIdsProvider('n1').future),
        ['t1', 't2'],
      );
    });
  });
}
