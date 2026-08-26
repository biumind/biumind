// 「已分享」智能视图（分享中心 P1，docs/BiuMind-Share-Center-Design.md
// §2 D2）测试。
//
// 三层覆盖，对齐仓库既有测试形态：
//   1. NotebookColumn widget（notebook_tree_ui_test 形态：AppDb.memory +
//      真 repo，watch 流用 override 喂值，避开 drift QueryStream 的
//      fake_async pending-timer 冲突）—— 入口渲染 + 数量徽标 + 点击切视图；
//   2. notesListProvider shared 过滤（notes_ui_test 形态：provider 层，
//      真 Drift 流）—— 列表内容（只含活跃分享的笔记）+ 空数据；
//   3. noteListEmptyLabel 纯函数 —— 空态文案。

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/note_share_providers.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:biumind/features/notes/presentation/notes_home_page.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

api.NoteShareListItem _shareItem(
  String noteId,
  String title,
  api.NoteShareStatus status,
) =>
    api.NoteShareListItem(
      share: api.NoteShare(
        token: 'tok-$noteId',
        passwordSet: false,
        credentialVersion: 1,
        viewCount: 0,
        createdAt: DateTime.utc(2026, 8, 26),
        updatedAt: DateTime.utc(2026, 8, 26),
      ),
      noteId: noteId,
      noteTitle: title,
      status: status,
    );

void main() {
  late AppDb db;
  late NotesDao dao;
  late NotesRepository repo;

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    db = AppDb.memory();
    dao = NotesDao(db, scope: 'test');
    // 指不可达地址 —— 只走本地 Drift 流，不触网。
    repo = NotesRepository(
      dao: dao,
      client: api.NotesClient(Uri.parse('http://127.0.0.1:1'), 'tok'),
    );
  });

  tearDown(() async {
    await db.close();
  });

  Future<void> seedNote(String id, String title) => dao.upsertNote(LocalNote(
        id: id,
        notebookId: null,
        title: title,
        contentMd: 'content-$id',
        isTodo: false,
        position: 0,
        version: 1,
        trashed: false,
        updatedAt: DateTime.now().toUtc(),
        ownerKey: 'test',
      ));

  group('NotebookColumn「已分享」入口（widget）', () {
    Future<void> pumpColumn(
      WidgetTester tester,
      List<api.NoteShareListItem> shares,
    ) async {
      await tester.pumpWidget(ProviderScope(
        overrides: [
          notesRepositoryProvider.overrideWithValue(repo),
          notesNotebooksProvider.overrideWith(
              (ref) => const Stream<List<RepoNotebook>>.empty()),
          notesTagsProvider
              .overrideWith((ref) => const Stream<List<RepoTag>>.empty()),
          myNoteSharesProvider.overrideWith((ref) async => shares),
        ],
        child: const MaterialApp(
          home: Scaffold(body: SizedBox(width: 280, child: NotebookColumn())),
        ),
      ));
      await tester.pump();
      await tester.pump();
    }

    testWidgets('入口渲染 + 数量徽标 = 活跃分享数（停用不计）', (tester) async {
      await pumpColumn(tester, [
        _shareItem('n1', 'a', api.NoteShareStatus.active),
        _shareItem('n2', 'b', api.NoteShareStatus.active),
        _shareItem('n3', 'c', api.NoteShareStatus.disabled),
      ]);

      expect(find.text('已分享'), findsOneWidget);
      expect(find.text('2'), findsOneWidget,
          reason: '徽标只计 active（disabled 不算）');
    });

    testWidgets('无分享时不显示徽标', (tester) async {
      await pumpColumn(tester, const []);
      expect(find.text('已分享'), findsOneWidget);
      // 只有标签区可能渲染文本；徽标数字不应出现。
      expect(find.text('0'), findsNothing);
    });

    testWidgets('点击切到 shared 过滤视图', (tester) async {
      await pumpColumn(tester, [
        _shareItem('n1', 'a', api.NoteShareStatus.active),
      ]);
      await tester.tap(find.text('已分享'));
      await tester.pump();

      final container = ProviderScope.containerOf(
        tester.element(find.byType(NotebookColumn)),
      );
      expect(container.read(notesFilterProvider).kind, NotesListKind.shared);
    });
  });

  group('notesListProvider shared 视图（provider）', () {
    test('只含活跃分享的笔记（updatedAt 倒序）；停用分享不出现', () async {
      await seedNote('n1', '活跃分享');
      await seedNote('n2', '已停用分享');
      await seedNote('n3', '普通笔记');
      final container = ProviderContainer(overrides: [
        notesRepositoryProvider.overrideWithValue(repo),
        myNoteSharesProvider.overrideWith((ref) async => [
              _shareItem('n1', '活跃分享', api.NoteShareStatus.active),
              _shareItem('n2', '已停用分享', api.NoteShareStatus.disabled),
            ]),
      ]);
      addTearDown(container.dispose);

      // 先等分享列表加载完再切视图 —— notesListProvider 的 shared 分支在
      // 分享列表加载完成时会重建流，但 .future 只取首个事件（UI 里靠
      // rebuild 自然刷新，测试要对齐真实时序）。
      await container.read(myNoteSharesProvider.future);
      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.shared();
      final notes = await container.read(notesListProvider.future);

      expect(notes.map((n) => n.id).toList(), ['n1'],
          reason: 'disabled 分享与普通笔记都应被过滤');
    });

    test('无活跃分享 → 空列表（空态数据源）', () async {
      await seedNote('n1', '普通笔记');
      final container = ProviderContainer(overrides: [
        notesRepositoryProvider.overrideWithValue(repo),
        myNoteSharesProvider.overrideWith((ref) async => const []),
      ]);
      addTearDown(container.dispose);

      container.read(notesFilterProvider.notifier).state =
          const NotesFilter.shared();
      expect(await container.read(notesListProvider.future), isEmpty);
    });
  });

  group('noteListEmptyLabel 空态文案', () {
    test('shared 视图 → 还没有分享的笔记', () {
      expect(noteListEmptyLabel(NotesListKind.shared), '还没有分享的笔记');
      expect(noteListEmptyLabel(NotesListKind.todo), '暂无待办');
      expect(noteListEmptyLabel(NotesListKind.all), '暂无笔记');
    });
  });
}
