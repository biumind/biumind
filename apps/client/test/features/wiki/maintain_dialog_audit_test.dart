// MaintainDialog 改动清单 widget 测试（BiuMind-Agent-Experience-Design §1.2
// P1 审计视图）。覆盖：
//   * SSE 写工具事件聚合成「改动清单」区（徽章 + 标题）
//   * 点开 diff：before = 写前快照（revision blocks_json 还原 markdown），
//     after = result body_md；缺快照 → diff unavailable 降级
//   * undo：update → restore 对应 revision（成功标「已撤销」，失败行内提示）；
//     create → deletePage（「删除此新建页」二次确认）；merge → 置灰
//
// 后端依赖全部 fake：agentRunner 注入假事件流，MaintainAuditClient 记录调用，
// 无网络。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/api/_http_helpers.dart' show ApiError;
import 'package:biumind/data/api/chat_client.dart';
import 'package:biumind/data/api/relay_catalog_client.dart';
import 'package:biumind/data/api/wiki_client.dart'
    show WikiAgentRun, WikiAgentRunChange, WikiPage, WikiPageRevision;
import 'package:biumind/data/providers_providers.dart'
    show relayCatalogListProvider;
import 'package:biumind/features/wiki/application/maintain_changes.dart';
import 'package:biumind/features/wiki/presentation/maintain_dialog.dart';
import 'package:biumind/features/wiki/presentation/selection_edit/word_diff_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

class _FakeAudit implements MaintainAuditClient {
  Map<String, List<WikiPageRevision>> revisions = {};
  Map<String, WikiPageRevision> details = {};

  /// (projectId, pageId, revisionId, ifMatchVersion)。
  final List<(String, String, String, int?)> restored = [];
  final List<(String, String)> deleted = [];
  bool failRestore = false;

  /// 非 null 时 restoreRevision 抛该异常（如 ApiError 409 模拟 OCC 冲突）。
  Object? restoreError;
  Map<String, WikiPage> pages = {};

  @override
  Future<List<WikiPageRevision>> listRevisions(
          String projectId, String pageId) async =>
      revisions[pageId] ?? const [];

  @override
  Future<WikiPageRevision> getRevision(
          String projectId, String pageId, String revisionId) async =>
      details[revisionId]!;

  @override
  Future<void> restoreRevision(
      String projectId, String pageId, String revisionId,
      {int? ifMatchVersion}) async {
    if (failRestore) throw Exception('version_conflict');
    if (restoreError != null) throw restoreError!;
    restored.add((projectId, pageId, revisionId, ifMatchVersion));
  }

  @override
  Future<void> deletePage(String projectId, String pageId) async {
    deleted.add((projectId, pageId));
  }

  @override
  Future<WikiPage> getPage(String projectId, String pageId) async =>
      pages[pageId]!;

  @override
  Future<List<WikiAgentRun>> listAgentRuns(String projectId) async =>
      const [];

  @override
  Future<(WikiAgentRun, List<WikiAgentRunChange>)> getAgentRun(
          String projectId, String runId) async =>
      throw UnimplementedError();
}

WikiPageRevision _revision(String id, String pageId, String beforeText) =>
    WikiPageRevision(
      id: id,
      pageId: pageId,
      projectId: 'p1',
      actorId: '',
      title: '第一页',
      changeType: 'edit',
      changeSummary: '',
      createdAt: DateTime.now().toUtc(),
      blocksJson: [
        {
          'id': 'b1',
          'page_id': pageId,
          'position': 1,
          'type': 'text',
          'content': {'text': beforeText},
          'version': 3,
        },
      ],
    );

List<ChatStreamEvent> _updateEvents() => [
      const ChatToolCreated(
        messageId: 'm1',
        blockId: 'tb1',
        name: 'wiki_update_page',
        input: {'page_id': 'pg1', 'version': 3, 'body_md': '贝塔内容'},
      ),
      const ChatToolCompleted(
        messageId: 'm1',
        blockId: 'tb1',
        result: {
          'page': {'id': 'pg1', 'title': '第一页', 'version': 4, 'body_md': '贝塔内容'},
        },
        durationMs: 5,
      ),
      const ChatMessageDone(messageId: 'm1'),
    ];

List<ChatStreamEvent> _createEvents() => [
      const ChatToolCreated(
        messageId: 'm1',
        blockId: 'tb1',
        name: 'wiki_create_page',
        input: {'title': '新页', 'body_md': '全新内容'},
      ),
      const ChatToolCompleted(
        messageId: 'm1',
        blockId: 'tb1',
        result: {
          'created': true,
          'page': {'id': 'pg2', 'title': '新页', 'version': 1, 'body_md': '全新内容'},
        },
        durationMs: 5,
      ),
      const ChatMessageDone(messageId: 'm1'),
    ];

List<ChatStreamEvent> _mergeEvents() => [
      const ChatToolCreated(
        messageId: 'm1',
        blockId: 'tb1',
        name: 'wiki_merge_pages',
        input: {'canonical_id': 'pg1', 'duplicate_id': 'pg9'},
      ),
      const ChatToolCompleted(
        messageId: 'm1',
        blockId: 'tb1',
        result: {
          'canonical_id': 'pg1',
          'duplicate_id': 'pg9',
          'page': {'id': 'pg1', 'title': '第一页', 'version': 5, 'body_md': '合并后'},
        },
        durationMs: 5,
      ),
      const ChatMessageDone(messageId: 'm1'),
    ];

Future<void> _runDialog(
  WidgetTester tester, {
  required List<ChatStreamEvent> events,
  required _FakeAudit audit,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        relayCatalogListProvider.overrideWith(
          (ref) async => const [
            RelayCatalogModel(
              code: 'm1',
              displayName: 'M1',
              family: 'test',
              mode: 'chat',
            ),
          ],
        ),
      ],
      child: MaterialApp(
        theme: buildTheme(
          palette: PaletteId.inkblueOrange,
          mode: Brightness.light,
          fontSize: FontSize.small,
        ),
        home: Scaffold(
          body: MaintainDialog(
            projectId: 'p1',
            agentRunner: (
              projectId, {
              required runId,
              required instruction,
              required model,
              required mode,
            }) =>
                Stream.fromIterable(events),
            audit: audit,
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();

  // 填指令 + 选模型（目录覆盖给一个 m1）。
  await tester.enterText(find.byType(TextField).first, '整理一下');
  await tester.tap(find.text('选择模型'), warnIfMissed: false);
  await tester.pumpAndSettle();
  await tester.tap(find.text('M1').last);
  await tester.pumpAndSettle();

  await tester.tap(find.text('开始维护'));
  await tester.pumpAndSettle();
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('form 阶段有「历史运行」入口，点开出 run 历史对话框', (tester) async {
    final audit = _FakeAudit();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          relayCatalogListProvider.overrideWith(
            (ref) async => const [
              RelayCatalogModel(
                code: 'm1',
                displayName: 'M1',
                family: 'test',
                mode: 'chat',
              ),
            ],
          ),
        ],
        child: MaterialApp(
          theme: buildTheme(
            palette: PaletteId.inkblueOrange,
            mode: Brightness.light,
            fontSize: FontSize.small,
          ),
          home: Scaffold(
            body: MaintainDialog(projectId: 'p1', audit: audit),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('历史运行'), findsOneWidget);
    await tester.tap(find.text('历史运行'));
    await tester.pumpAndSettle();
    // _FakeAudit.listAgentRuns 返回空 → 空态文案。
    expect(find.textContaining('还没有历史运行'), findsOneWidget);
  });

  testWidgets('写工具事件聚合成改动清单（徽章 + 页标题）', (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')];
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    expect(find.text('改动清单 (1)'), findsOneWidget);
    expect(find.text('修改'), findsOneWidget);
    expect(find.text('第一页'), findsOneWidget);
  });

  testWidgets('点开行渲染词级 diff（before=快照 blocks 还原，after=改后全文）',
      (tester) async {
    final rev = _revision('r1', 'pg1', '阿尔法段落');
    final audit = _FakeAudit()
      ..revisions['pg1'] = [
        WikiPageRevision(
          id: rev.id,
          pageId: rev.pageId,
          projectId: rev.projectId,
          actorId: rev.actorId,
          title: rev.title,
          changeType: rev.changeType,
          changeSummary: rev.changeSummary,
          createdAt: rev.createdAt,
        ),
      ]
      ..details['r1'] = rev;
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    await tester.tap(find.text('第一页'));
    await tester.pumpAndSettle();

    expect(find.text('变更对比 · 第一页'), findsOneWidget);
    expect(find.byType(WordDiffView), findsWidgets);
    final plain = tester
        .widgetList<Text>(find.descendant(
          of: find.byType(WordDiffView),
          matching: find.byType(Text),
        ))
        .map((t) => t.textSpan?.toPlainText() ?? '')
        .join();
    expect(plain, contains('阿尔法段落'));
    expect(plain, contains('贝塔内容'));
  });

  testWidgets('缺快照 → diff unavailable 降级，undo 按钮禁用', (tester) async {
    final audit = _FakeAudit(); // revisions 空
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    final undoBtn = tester.widget<TextButton>(find.ancestor(
      of: find.text('撤销'),
      matching: find.byType(TextButton),
    ));
    expect(undoBtn.onPressed, isNull);

    await tester.tap(find.text('第一页'));
    await tester.pumpAndSettle();
    expect(find.textContaining('diff unavailable'), findsOneWidget);
  });

  testWidgets('update undo 调 restore 对应 revision（带 if_match OCC），成功标「已撤销」',
      (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')];
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();

    // P2 OCC：if_match = 本 run 最后写完的 version（result.page.version=4）。
    expect(audit.restored, [('p1', 'pg1', 'r1', 4)]);
    expect(find.text('已撤销'), findsOneWidget);
  });

  testWidgets('update undo 409 → 弹「run 之后有新修改」diff 确认，确认后无 if_match 重试',
      (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')]
      ..details['r1'] = _revision('r1', 'pg1', '阿尔法段落')
      ..pages['pg1'] = WikiPage(
        id: 'pg1',
        projectId: 'p1',
        title: '第一页',
        version: 5,
        updatedAt: DateTime.now(),
        bodyMd: '人工新改的内容',
      )
      // 第一次带 if_match 的 restore 抛 409；确认后重试放行。
      ..restoreError = const ApiError(path: '/restore', status: 409, body: '{}');
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();

    // 409 → 确认对话框（含当前态 vs 目标态 diff），尚未重试。
    expect(find.text('run 之后有新修改'), findsOneWidget);
    expect(find.text('仍要撤销'), findsOneWidget);
    expect(audit.restored, isEmpty);

    audit.restoreError = null;
    await tester.tap(find.text('仍要撤销'));
    await tester.pumpAndSettle();

    // 无 if_match 重试 → 成功。
    expect(audit.restored, [('p1', 'pg1', 'r1', null)]);
    expect(find.text('已撤销'), findsOneWidget);
  });

  testWidgets('update undo 409 确认框点「取消」不重试', (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')]
      ..details['r1'] = _revision('r1', 'pg1', '阿尔法段落')
      ..pages['pg1'] = WikiPage(
        id: 'pg1',
        projectId: 'p1',
        title: '第一页',
        version: 5,
        updatedAt: DateTime.now(),
        bodyMd: '人工新改的内容',
      )
      ..restoreError = const ApiError(path: '/restore', status: 409, body: '{}');
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();

    expect(audit.restored, isEmpty);
    expect(find.text('已撤销'), findsNothing);
  });

  testWidgets('update undo 失败（restore 抛错）行内提示，不静默', (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')]
      ..failRestore = true;
    await _runDialog(tester, events: _updateEvents(), audit: audit);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();

    expect(find.textContaining('撤销失败'), findsOneWidget);
    expect(find.text('已撤销'), findsNothing);
  });

  testWidgets('create undo：二次确认「删除此新建页」后调 deletePage',
      (tester) async {
    final audit = _FakeAudit();
    await _runDialog(tester, events: _createEvents(), audit: audit);

    expect(find.text('新建'), findsOneWidget);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();
    // 二次确认弹出；确认前不发请求。
    expect(find.text('删除此新建页'), findsOneWidget);
    expect(audit.deleted, isEmpty);

    await tester.tap(find.text('删除'));
    await tester.pumpAndSettle();
    expect(audit.deleted, [('p1', 'pg2')]);
    expect(find.text('已撤销'), findsOneWidget);
  });

  testWidgets('create undo 确认框点「取消」不删页', (tester) async {
    final audit = _FakeAudit();
    await _runDialog(tester, events: _createEvents(), audit: audit);

    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(audit.deleted, isEmpty);
    expect(find.text('已撤销'), findsNothing);
  });

  testWidgets('merge 行 undo 置灰（后续版本支持）', (tester) async {
    final audit = _FakeAudit()
      ..revisions['pg1'] = [_revision('r1', 'pg1', '阿尔法段落')];
    await _runDialog(tester, events: _mergeEvents(), audit: audit);

    expect(find.text('合并'), findsOneWidget);
    final undoBtn = tester.widget<TextButton>(find.ancestor(
      of: find.text('撤销'),
      matching: find.byType(TextButton),
    ));
    expect(undoBtn.onPressed, isNull);
    expect(audit.restored, isEmpty);
    expect(audit.deleted, isEmpty);
  });
}
