// maintain_changes 纯逻辑单测（BiuMind-Agent-Experience-Design §1.2 P1）：
//   * MaintainChangeTracker —— SSE 写工具事件 → 逐页改动清单聚合
//   * pickBeforeRevision —— 写前快照时间窗匹配（最早一条 edit；取不到返回
//     null，调用方标 diffUnavailable，不猜）

import 'package:biumind/data/api/wiki_client.dart' show WikiPageRevision;
import 'package:biumind/features/wiki/application/maintain_changes.dart';
import 'package:flutter_test/flutter_test.dart';

WikiPageRevision _rev(
  String id,
  DateTime at, {
  String changeType = 'edit',
  String? runId,
}) =>
    WikiPageRevision(
      id: id,
      pageId: 'pg1',
      projectId: 'p1',
      actorId: '',
      title: 't',
      changeType: changeType,
      changeSummary: '',
      createdAt: at,
      runId: runId,
    );

void main() {
  group('MaintainChangeTracker', () {
    test('create/update/merge 三类写工具聚合成清单', () {
      final t = MaintainChangeTracker();
      final now = DateTime.now().toUtc();

      t.onToolCreated('b1', 'wiki_create_page',
          {'title': '新页', 'body_md': '内容'}, at: now);
      t.onToolCompleted('b1', {
        'created': true,
        'page': {'id': 'pg2', 'title': '新页', 'version': 1, 'body_md': '内容'},
      }, at: now);

      t.onToolCreated('b2', 'wiki_update_page',
          {'page_id': 'pg1', 'version': 3, 'body_md': '改后'}, at: now);
      t.onToolCompleted('b2', {
        'page': {'id': 'pg1', 'title': '第一页', 'version': 4, 'body_md': '改后'},
      }, at: now);

      t.onToolCreated('b3', 'wiki_merge_pages',
          {'canonical_id': 'pg1', 'duplicate_id': 'pg9'}, at: now);
      t.onToolCompleted('b3', {
        'canonical_id': 'pg1',
        'duplicate_id': 'pg9',
        'page': {'id': 'pg1', 'title': '第一页', 'version': 5, 'body_md': '合后'},
      }, at: now);

      final changes = t.changes;
      expect(changes, hasLength(3));

      final create =
          changes.firstWhere((c) => c.op == MaintainChangeOp.create);
      expect(create.pageId, 'pg2');
      expect(create.title, '新页');
      expect(create.afterBodyMd, '内容');
      expect(create.beforeVersion, isNull);

      // pg1 先 update 后 merge：update 行聚合，merge 单独一行。
      final update =
          changes.firstWhere((c) => c.op == MaintainChangeOp.update);
      expect(update.pageId, 'pg1');
      expect(update.beforeVersion, 3);
      expect(update.afterBodyMd, '改后');

      final merge = changes.firstWhere((c) => c.op == MaintainChangeOp.merge);
      expect(merge.pageId, 'pg1');
      expect(merge.duplicateId, 'pg9');
      expect(merge.afterBodyMd, '合后');
    });

    test('同页多次 update 聚合为一行：before 取首条 version，after 滚动最新', () {
      final t = MaintainChangeTracker();
      final now = DateTime.now().toUtc();
      t.onToolCreated('b1', 'wiki_update_page',
          {'page_id': 'pg1', 'version': 3, 'body_md': 'v4'}, at: now);
      t.onToolCompleted('b1', {
        'page': {'id': 'pg1', 'title': 'P', 'version': 4, 'body_md': 'v4'},
      }, at: now);
      t.onToolCreated('b2', 'wiki_update_page',
          {'page_id': 'pg1', 'version': 4, 'body_md': 'v5'}, at: now);
      t.onToolCompleted('b2', {
        'page': {'id': 'pg1', 'title': 'P', 'version': 5, 'body_md': 'v5'},
      }, at: now);

      final changes = t.changes;
      expect(changes, hasLength(1));
      expect(changes.single.beforeVersion, 3);
      expect(changes.single.afterBodyMd, 'v5');
      // P2 OCC：afterVersion = 本 run 最后写完的 version（undo if_match 用）。
      expect(changes.single.afterVersion, 5);
    });

    test('create 后同页 update：保持 create（undo 仍是删页），after 滚动', () {
      final t = MaintainChangeTracker();
      final now = DateTime.now().toUtc();
      t.onToolCreated(
          'b1', 'wiki_create_page', {'title': 'N', 'body_md': 'a'}, at: now);
      t.onToolCompleted('b1', {
        'page': {'id': 'pg1', 'title': 'N', 'version': 1, 'body_md': 'a'},
      }, at: now);
      t.onToolCreated('b2', 'wiki_update_page',
          {'page_id': 'pg1', 'version': 1, 'body_md': 'b'}, at: now);
      t.onToolCompleted('b2', {
        'page': {'id': 'pg1', 'title': 'N', 'version': 2, 'body_md': 'b'},
      }, at: now);

      final c = t.changes.single;
      expect(c.op, MaintainChangeOp.create);
      expect(c.afterBodyMd, 'b');
    });

    test('读工具与 wiki_create_review 不进清单；completed 无 created 配对忽略',
        () {
      final t = MaintainChangeTracker();
      final now = DateTime.now().toUtc();
      t.onToolCreated('b1', 'wiki_search', {'q': 'x'}, at: now);
      t.onToolCompleted('b1', {'hits': []}, at: now);
      t.onToolCreated('b2', 'wiki_create_review', {'kind': 'x'}, at: now);
      t.onToolCompleted('b2', {'review_id': 'r'}, at: now);
      t.onToolCompleted('b9', {
        'page': {'id': 'pgX', 'title': 'x', 'version': 1, 'body_md': 'x'},
      }, at: now);
      expect(t.changes, isEmpty);
    });
  });

  group('pickBeforeRevision', () {
    final t0 = DateTime.now().toUtc();

    test('窗口内取最早一条 edit 快照', () {
      final revs = [
        _rev('r2', t0.add(const Duration(seconds: 30))),
        _rev('r1', t0.add(const Duration(seconds: 5))),
        _rev('r0', t0.subtract(const Duration(minutes: 10))), // 窗口外
      ];
      final hit = pickBeforeRevision(
        revs,
        firstWriteAt: t0,
        lastWriteDoneAt: t0.add(const Duration(seconds: 40)),
      );
      expect(hit?.id, 'r1');
    });

    test('restore 类型不算写前快照', () {
      final revs = [
        _rev('rr', t0.add(const Duration(seconds: 5)), changeType: 'restore'),
      ];
      final hit = pickBeforeRevision(
        revs,
        firstWriteAt: t0,
        lastWriteDoneAt: t0.add(const Duration(seconds: 10)),
      );
      expect(hit, isNull);
    });

    test('窗口外 / 空列表 → null（快照被服务端跳过时走 diff unavailable）', () {
      expect(
        pickBeforeRevision(
          [_rev('r0', t0.subtract(const Duration(minutes: 10)))],
          firstWriteAt: t0,
          lastWriteDoneAt: t0.add(const Duration(seconds: 10)),
        ),
        isNull,
      );
      expect(
        pickBeforeRevision(const [],
            firstWriteAt: t0, lastWriteDoneAt: t0),
        isNull,
      );
    });

    test('P2：有 runId 时按 run_id 精确匹配（忽略时间窗外的同 run 快照）', () {
      // 同 run 的快照在"时间窗"外（客户端时钟偏差大）也应命中——精确匹配
      // 不依赖时间；别的 run / 人工的快照即使在窗口内也不抢。
      final revs = [
        _rev('other', t0.add(const Duration(seconds: 5)), runId: 'run-b'),
        _rev('manual', t0.add(const Duration(seconds: 6))),
        _rev('mine2', t0.add(const Duration(minutes: 30)), runId: 'run-a'),
        _rev('mine1', t0.add(const Duration(minutes: 29)), runId: 'run-a'),
      ];
      final hit = pickBeforeRevision(
        revs,
        firstWriteAt: t0,
        lastWriteDoneAt: t0.add(const Duration(seconds: 10)),
        runId: 'run-a',
      );
      expect(hit?.id, 'mine1'); // 同 run 最早一条
    });

    test('P2：新服务端上该 run 无快照（>512KB 跳过）→ null，不回退到人工快照',
        () {
      final revs = [
        _rev('manual', t0.add(const Duration(seconds: 5))),
        _rev('other', t0.add(const Duration(seconds: 6)), runId: 'run-b'),
      ];
      final hit = pickBeforeRevision(
        revs,
        firstWriteAt: t0,
        lastWriteDoneAt: t0.add(const Duration(seconds: 10)),
        runId: 'run-a',
      );
      expect(hit, isNull);
    });

    test('P2：旧 run（列表里没有任何 run_id 快照）回退时间窗匹配', () {
      final revs = [
        _rev('r1', t0.add(const Duration(seconds: 5))),
        _rev('r0', t0.subtract(const Duration(minutes: 10))),
      ];
      final hit = pickBeforeRevision(
        revs,
        firstWriteAt: t0,
        lastWriteDoneAt: t0.add(const Duration(seconds: 10)),
        runId: 'run-old',
      );
      expect(hit?.id, 'r1');
    });
  });
}
