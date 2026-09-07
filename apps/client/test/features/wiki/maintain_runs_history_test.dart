// MaintainRunsHistoryDialog widget 测试（BiuMind-Agent-Experience-Design
// §1.2 P2 run 持久化历史回看）。覆盖：
//   * run 列表渲染（指令 / 状态点 / 改动页数）
//   * 点进详情渲染改动页清单（操作徽章 + 写前标题）
//   * update 行 undo：if_match = 当前 version；409 → 确认后无 if_match 重试；
//     merge 行 undo 置灰
//
// 后端依赖全部 fake（MaintainAuditClient 记录调用），无网络。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/api/_http_helpers.dart' show ApiError;
import 'package:biumind/data/api/wiki_client.dart'
    show WikiAgentRun, WikiAgentRunChange, WikiPage, WikiPageRevision;
import 'package:biumind/features/wiki/application/maintain_changes.dart';
import 'package:biumind/features/wiki/presentation/maintain_runs_panel.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeAudit implements MaintainAuditClient {
  List<WikiAgentRun> runs = const [];
  Map<String, List<WikiAgentRunChange>> changes = {};
  Map<String, WikiPage> pages = {};
  Map<String, WikiPageRevision> details = {};

  /// (projectId, pageId, revisionId, ifMatchVersion)。
  final List<(String, String, String, int?)> restored = [];
  Object? restoreError;

  @override
  Future<List<WikiAgentRun>> listAgentRuns(String projectId) async => runs;

  @override
  Future<(WikiAgentRun, List<WikiAgentRunChange>)> getAgentRun(
          String projectId, String runId) async =>
      (runs.firstWhere((r) => r.runId == runId), changes[runId] ?? const []);

  @override
  Future<WikiPage> getPage(String projectId, String pageId) async =>
      pages[pageId]!;

  @override
  Future<void> restoreRevision(
      String projectId, String pageId, String revisionId,
      {int? ifMatchVersion}) async {
    if (restoreError != null) throw restoreError!;
    restored.add((projectId, pageId, revisionId, ifMatchVersion));
  }

  @override
  Future<WikiPageRevision> getRevision(
          String projectId, String pageId, String revisionId) async =>
      details[revisionId]!;

  @override
  Future<List<WikiPageRevision>> listRevisions(
          String projectId, String pageId) async =>
      const [];

  @override
  Future<void> deletePage(String projectId, String pageId) async {}
}

WikiAgentRun _run(String id, String status, {int changed = 1}) => WikiAgentRun(
      runId: id,
      projectId: 'p1',
      mode: 'standard',
      model: 'm1',
      instruction: '整理知识库 $id',
      status: status,
      startedAt: DateTime.utc(2026, 9, 7, 10),
      finishedAt:
          status == 'running' ? null : DateTime.utc(2026, 9, 7, 10, 3),
      changedPages: changed,
    );

WikiAgentRunChange _change(String revId, String op, {String title = '页'}) =>
    WikiAgentRunChange(
      revisionId: revId,
      pageId: 'pg-$revId',
      title: title,
      op: op,
      changeType: 'edit',
      createdAt: DateTime.utc(2026, 9, 7, 10, 1),
    );

Future<void> _pump(WidgetTester tester, _FakeAudit audit) async {
  await tester.pumpWidget(
    MaterialApp(
      theme: buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      home: Scaffold(
        body: MaintainRunsHistoryDialog(projectId: 'p1', audit: audit),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('run 列表渲染（指令 + 改动页数），点进详情出改动清单', (tester) async {
    final audit = _FakeAudit()
      ..runs = [_run('r1', 'done', changed: 2), _run('r2', 'failed')]
      ..changes['r1'] = [
        _change('rev-a', 'update', title: '第一页'),
        _change('rev-b', 'merge', title: '被合并页'),
      ];
    await _pump(tester, audit);

    expect(find.text('历史运行'), findsOneWidget);
    expect(find.text('整理知识库 r1'), findsOneWidget);
    expect(find.textContaining('改动 2 页'), findsOneWidget);

    await tester.tap(find.text('整理知识库 r1'));
    await tester.pumpAndSettle();

    expect(find.textContaining('运行详情'), findsOneWidget);
    expect(find.text('第一页'), findsOneWidget);
    expect(find.text('被合并页'), findsOneWidget);
    expect(find.text('修改'), findsOneWidget);
    expect(find.text('合并'), findsOneWidget);
  });

  testWidgets('历史 undo：if_match = 当前 version，成功标「已撤销」', (tester) async {
    final audit = _FakeAudit()
      ..runs = [_run('r1', 'done')]
      ..changes['r1'] = [_change('rev-a', 'update', title: '第一页')]
      ..pages['pg-rev-a'] = WikiPage(
        id: 'pg-rev-a',
        projectId: 'p1',
        title: '第一页',
        version: 7,
        updatedAt: DateTime.now(),
        bodyMd: '当前内容',
      );
    await _pump(tester, audit);

    await tester.tap(find.text('整理知识库 r1'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();

    expect(audit.restored, [('p1', 'pg-rev-a', 'rev-a', 7)]);
    expect(find.text('已撤销'), findsOneWidget);
  });

  testWidgets('历史 undo 409 → 「页面有新修改」确认后无 if_match 重试',
      (tester) async {
    final audit = _FakeAudit()
      ..runs = [_run('r1', 'done')]
      ..changes['r1'] = [_change('rev-a', 'update', title: '第一页')]
      ..pages['pg-rev-a'] = WikiPage(
        id: 'pg-rev-a',
        projectId: 'p1',
        title: '第一页',
        version: 7,
        updatedAt: DateTime.now(),
        bodyMd: '当前内容',
      )
      ..details['rev-a'] = WikiPageRevision(
        id: 'rev-a',
        pageId: 'pg-rev-a',
        projectId: 'p1',
        actorId: '',
        title: '第一页',
        changeType: 'edit',
        changeSummary: '',
        createdAt: DateTime.utc(2026, 9, 7, 10),
        blocksJson: [
          {
            'id': 'b1',
            'page_id': 'pg-rev-a',
            'position': 1,
            'type': 'text',
            'content': {'text': '目标内容'},
            'version': 3,
          },
        ],
      )
      ..restoreError =
          const ApiError(path: '/restore', status: 409, body: '{}');
    await _pump(tester, audit);

    await tester.tap(find.text('整理知识库 r1'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('撤销'));
    await tester.pumpAndSettle();

    expect(find.text('页面有新修改'), findsOneWidget);
    expect(audit.restored, isEmpty);

    audit.restoreError = null;
    await tester.tap(find.text('仍要撤销'));
    await tester.pumpAndSettle();

    expect(audit.restored, [('p1', 'pg-rev-a', 'rev-a', null)]);
    expect(find.text('已撤销'), findsOneWidget);
  });

  testWidgets('merge 行 undo 置灰', (tester) async {
    final audit = _FakeAudit()
      ..runs = [_run('r1', 'done')]
      ..changes['r1'] = [_change('rev-b', 'merge', title: '被合并页')];
    await _pump(tester, audit);

    await tester.tap(find.text('整理知识库 r1'));
    await tester.pumpAndSettle();

    final undoBtn = tester.widget<TextButton>(find.ancestor(
      of: find.text('撤销'),
      matching: find.byType(TextButton),
    ));
    expect(undoBtn.onPressed, isNull);
    expect(audit.restored, isEmpty);
  });
}
