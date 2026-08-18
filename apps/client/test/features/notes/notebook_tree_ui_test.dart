// NotebookColumn 树形 UI widget 冒烟测试（PR4 多级目录）。
//
// 形态对齐 notes_ui_test.dart：AppDb.memory() + 真 NotesRepository
// （NotesClient 指不可达地址，写路径真实落本地 Drift + outbox）+
// ProviderScope override notesRepositoryProvider。
//
// 与仓库内其它 widget 测试的差异：本测试不用 repo 的 watch 流驱动 UI ——
// drift QueryStream 在 dispose 时注册零时长定时器，与 flutter_test 的
// fake_async「test 结束不允许 pending timer」不变量冲突（试过的收尾方案
// 都会卡死/误报）。故 notesNotebooksProvider / notesTagsProvider 用
// StreamController 手动推送（数据仍来自本地 Drift dao 查询），写路径
// （createNotebook/updateNotebook）走真 repo。响应性（watch 流联动）由
// notes_pending_watch_test.dart 等数据层测试覆盖，本文件只锁树形 UI 行为。
//
// SharedPreferences 用 mock initial values（收起集合持久化 key
// notes_notebooks_tree_collapsed）。

import 'dart:async';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:biumind/features/notes/presentation/notes_home_page.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  late AppDb db;
  late NotesDao dao;
  late NotesRepository repo;
  late StreamController<List<RepoNotebook>> notebooksCtrl;

  setUp(() async {
    SharedPreferences.setMockInitialValues({});
    db = AppDb.memory();
    dao = NotesDao(db, scope: 'test');
    // 指不可达地址 —— 写路径只落本地 Drift + outbox，不触网。
    final client = api.NotesClient(Uri.parse('http://127.0.0.1:1'), 'tok');
    repo = NotesRepository(dao: dao, client: client);
    notebooksCtrl = StreamController<List<RepoNotebook>>.broadcast();
  });

  tearDown(() async {
    await notebooksCtrl.close();
    await db.close();
  });

  /// 把本地 Drift 里的笔记本当前态推给被 override 的 provider。
  Future<void> pushNotebooks() async {
    final rows = await dao.listNotebooks();
    notebooksCtrl.add([for (final r in rows) RepoNotebook.fromLocal(r)]);
  }

  Future<void> seed(String id, String name, {String? parentId}) =>
      dao.upsertNotebook(LocalNoteNotebook(
        id: id,
        name: name,
        parentId: parentId,
        position: 0,
        ownerKey: 'test',
        updatedAt: DateTime.utc(2100),
      ));

  /// 标准树：父 P（根）→ 子 C → 孙 G；另有根 R。
  Future<void> seedTree() async {
    await seed('P', '父目录');
    await seed('C', '子目录', parentId: 'P');
    await seed('G', '孙目录', parentId: 'C');
    await seed('R', '根目录二');
  }

  Future<void> pumpColumn(WidgetTester tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [
        notesRepositoryProvider.overrideWithValue(repo),
        notesNotebooksProvider
            .overrideWith((ref) => notebooksCtrl.stream),
        notesTagsProvider
            .overrideWith((ref) => const Stream<List<RepoTag>>.empty()),
      ],
      child: const MaterialApp(
        home: Scaffold(body: SizedBox(width: 280, child: NotebookColumn())),
      ),
    ));
    await pushNotebooks();
    // broadcast 流事件在首个 pump 的微任务里投递，帧要再 pump 一次才重建。
    await tester.pump();
    await tester.pump();
  }

  /// 某笔记本行内的行尾菜单按钮（按行文本定位，与行排序无关）。
  Finder menuIconOf(String name) => find.descendant(
        of: find
            .ancestor(of: find.text(name), matching: find.byType(InkWell))
            .first,
        matching: find.byIcon(Icons.more_horiz),
      );

  testWidgets('嵌套笔记本渲染成树：父子行都在，子行有缩进', (tester) async {
    await seedTree();
    await pumpColumn(tester);

    for (final name in ['父目录', '子目录', '孙目录', '根目录二']) {
      expect(find.text(name), findsOneWidget, reason: '$name 应可见');
    }
    // 缩进：每深一层左边缘右移。
    final xP = tester.getTopLeft(find.text('父目录')).dx;
    final xC = tester.getTopLeft(find.text('子目录')).dx;
    final xG = tester.getTopLeft(find.text('孙目录')).dx;
    final xR = tester.getTopLeft(find.text('根目录二')).dx;
    expect(xC, greaterThan(xP), reason: '子目录应比父目录缩进');
    expect(xG, greaterThan(xC), reason: '孙目录应比子目录更缩进');
    expect(xR, xP, reason: '根级行缩进一致');
  });

  testWidgets('展开/收起箭头：收起父行后子树隐藏，再展开复现', (tester) async {
    await seedTree();
    await pumpColumn(tester);

    // 默认全展开：父/子目录行各有一个 expand_more 箭头。
    expect(find.byIcon(Icons.expand_more), findsNWidgets(2));

    // 收起「父目录」（第一行的箭头）。
    await tester.tap(find.byIcon(Icons.expand_more).first);
    await tester.pump();
    expect(find.text('子目录'), findsNothing, reason: '收起后子行隐藏');
    expect(find.text('孙目录'), findsNothing);
    expect(find.text('父目录'), findsOneWidget);
    expect(find.byIcon(Icons.chevron_right), findsOneWidget);

    // 再展开。
    await tester.tap(find.byIcon(Icons.chevron_right));
    await tester.pump();
    expect(find.text('子目录'), findsOneWidget, reason: '再展开后子行复现');
    expect(find.text('孙目录'), findsOneWidget);
  });

  testWidgets('行尾菜单：新建子目录/移动到…；非根节点还有升到根级',
      (tester) async {
    await seedTree();
    await pumpColumn(tester);

    // 根节点「父目录」的菜单：无「升到根级」。
    await tester.tap(menuIconOf('父目录'));
    await tester.pumpAndSettle();
    expect(find.text('新建子目录'), findsOneWidget);
    expect(find.text('移动到…'), findsOneWidget);
    expect(find.text('升到根级'), findsNothing, reason: '根节点不显示升根');
    await tester.tapAt(const Offset(10, 10)); // 点空白关掉菜单
    await tester.pumpAndSettle();

    // 非根节点「子目录」的菜单：有「升到根级」。
    await tester.tap(menuIconOf('子目录'));
    await tester.pumpAndSettle();
    expect(find.text('新建子目录'), findsOneWidget);
    expect(find.text('移动到…'), findsOneWidget);
    expect(find.text('升到根级'), findsOneWidget);
    await tester.tapAt(const Offset(10, 10));
    await tester.pumpAndSettle();
  });

  testWidgets('新建子目录：弹窗输入确认后落库 parentId 正确', (tester) async {
    await seed('P', '父目录');
    await pumpColumn(tester);

    await tester.tap(menuIconOf('父目录'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('新建子目录'));
    await tester.pumpAndSettle();

    expect(find.text('新建子目录'), findsOneWidget); // 对话框标题
    await tester.enterText(find.byType(TextField), '子目录X');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();

    // repo.createNotebook(parentId:) 真实落库。
    final rows = await dao.listNotebooks();
    final created = rows.where((r) => r.name == '子目录X').toList();
    expect(created, hasLength(1), reason: '本地应落新笔记本行');
    expect(created.single.parentId, 'P', reason: 'parentId 应为父目录 id');

    // 推送新状态 → UI 立即可见（父节点已自动展开）。
    await pushNotebooks();
    await tester.pump();
    await tester.pump();
    expect(find.text('子目录X'), findsOneWidget);
    expect(tester.getTopLeft(find.text('子目录X')).dx,
        greaterThan(tester.getTopLeft(find.text('父目录')).dx));
  });

  testWidgets('升到根级：确认后该行 parentId 变 null，缩进回到根级',
      (tester) async {
    await seedTree();
    await pumpColumn(tester);

    // 「子目录」行的菜单 → 升到根级。
    await tester.tap(menuIconOf('子目录'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('升到根级'));
    await tester.pumpAndSettle();

    final row = await dao.notebookById('C');
    expect(row?.parentId, isNull, reason: '升根后 parentId 应为 null');

    await pushNotebooks();
    await tester.pump();
    await tester.pump();
    // UI：子目录回到根级缩进；孙目录仍挂在它下面（更深一层）。
    final xP = tester.getTopLeft(find.text('父目录')).dx;
    final xC = tester.getTopLeft(find.text('子目录')).dx;
    final xG = tester.getTopLeft(find.text('孙目录')).dx;
    expect(xC, xP, reason: '升根后与根级行缩进一致');
    expect(xG, greaterThan(xC), reason: '孙目录仍在子目录下');
  });
}
