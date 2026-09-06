// maintain_changes — wiki maintain agent 的「本 run 改动清单」纯客户端聚合
// （BiuMind-Agent-Experience-Design §1.2 P1，零服务端改动）。
//
// maintain agent 走 SSE（BlockEmitter v2 事件），tool.created 带完整 input、
// tool.completed 带 result，此前 maintain_dialog 显式丢弃。本文件把它们聚成
// 逐页改动清单，供 run 结束后的审计 UI（diff + 逐条 undo）消费。
//
// 关键语义（诚实降级，不许猜）：
//   * run 级 before = 该页第一条写操作对应的 edit 快照（服务端 5 分钟窗口
//     合并保证 run 内同页多次写只留首条写前快照）。page_revisions 没有
//     version 列，客户端按时间窗匹配：快照 created_at ∈
//     [firstWriteAt - skew, lastWriteDoneAt + skew] 的最早一条 edit。
//   * 快照可能不存在（blocks_json >512KB 服务端整条跳过）→ diffUnavailable，
//     undo 同步禁用——宁可标不可用也不猜。
//   * create 无快照 → undo = 删页；merge 的 undo 本期不做（需 un-delete
//     duplicate，服务端还不支持）。

import '../../../data/api/wiki_client.dart';

/// 写工具操作类型。
enum MaintainChangeOp { create, update, merge }

/// 一条页级改动（run 内同页多次写聚合为一行：before 取首条、after 取末条）。
class MaintainChange {
  MaintainChange({
    required this.pageId,
    required this.title,
    required this.op,
    required this.firstWriteAt,
    required this.lastWriteDoneAt,
    this.beforeVersion,
    this.afterBodyMd = '',
    this.duplicateId,
  });

  final String pageId;
  String title;
  final MaintainChangeOp op;

  /// update 首条写操作 input.version（改前 version）。create/merge 为 null。
  final int? beforeVersion;

  /// 最新一次写 result.page.body_md（改后全文）。
  String afterBodyMd;

  /// 首条写操作的 tool.created 到达时刻（UTC）与末条 tool.completed 到达
  /// 时刻——快照时间窗匹配用。
  final DateTime firstWriteAt;
  DateTime lastWriteDoneAt;

  /// merge：被吸收（软删）的 duplicate 页 id。
  final String? duplicateId;

  // ─── run 结束后由审计 UI 解析填充 ───

  /// 匹配到的写前快照 revision id；null = 尚未解析或未找到。
  String? beforeRevisionId;

  /// 快照列表已拉取但没找到匹配 edit 快照 → diff/undo 均不可用。
  bool diffUnavailable = false;

  /// undo 成功后置 true，清单行标「已撤销」。
  bool undone = false;

  /// undo 失败信息（不静默，行内展示）。
  String? undoError;
}

/// 写工具 → 操作类型。`wiki_create_review` 不改页面，不在清单内。
const Map<String, MaintainChangeOp> kMaintainWriteTools = {
  'wiki_create_page': MaintainChangeOp.create,
  'wiki_update_page': MaintainChangeOp.update,
  'wiki_merge_pages': MaintainChangeOp.merge,
};

/// 把 SSE 工具事件聚成改动清单。blockId 关联 created/completed 两半。
class MaintainChangeTracker {
  final Map<String, _PendingWrite> _pending = {};
  // pageId → 聚合行（merge 例外：每次 merge 单独一行，见 onToolCompleted）。
  final Map<String, MaintainChange> _byPage = {};
  final List<MaintainChange> _merges = [];

  void onToolCreated(
    String blockId,
    String name,
    Map<String, dynamic> input, {
    DateTime? at,
  }) {
    final op = kMaintainWriteTools[name];
    if (op == null) return;
    _pending[blockId] = _PendingWrite(
      op: op,
      input: input,
      at: (at ?? DateTime.now()).toUtc(),
    );
  }

  void onToolCompleted(String blockId, dynamic result, {DateTime? at}) {
    final p = _pending.remove(blockId);
    if (p == null) return;
    final doneAt = (at ?? DateTime.now()).toUtc();
    final res = result is Map ? result.cast<String, dynamic>() : null;
    final page = (res?['page'] as Map?)?.cast<String, dynamic>();
    switch (p.op) {
      case MaintainChangeOp.create:
        // result: {created, page:{id,title,version,body_md}}
        final id = page?['id']?.toString();
        if (id == null || id.isEmpty) return;
        _byPage[id] = MaintainChange(
          pageId: id,
          title: page?['title']?.toString() ?? '',
          op: MaintainChangeOp.create,
          firstWriteAt: p.at,
          lastWriteDoneAt: doneAt,
          afterBodyMd: page?['body_md']?.toString() ?? '',
        );
      case MaintainChangeOp.update:
        // input: {page_id, version(改前), body_md(新全文)}；result.page = 改后。
        final id =
            (p.input['page_id'] ?? page?['id'])?.toString();
        if (id == null || id.isEmpty) return;
        final afterBody = page?['body_md']?.toString() ??
            p.input['body_md']?.toString() ??
            '';
        final title = page?['title']?.toString() ?? '';
        final existing = _byPage[id];
        if (existing != null) {
          // 同页多次写：before 保持首条（含 create 后 update —— undo 仍是删页），
          // after 滚动到最新。
          existing.afterBodyMd = afterBody;
          existing.lastWriteDoneAt = doneAt;
          if (title.isNotEmpty) existing.title = title;
        } else {
          _byPage[id] = MaintainChange(
            pageId: id,
            title: title,
            op: MaintainChangeOp.update,
            firstWriteAt: p.at,
            lastWriteDoneAt: doneAt,
            beforeVersion: (p.input['version'] as num?)?.toInt(),
            afterBodyMd: afterBody,
          );
        }
      case MaintainChangeOp.merge:
        // input/result 含 canonical_id + duplicate_id；每个 merge 单独一行
        // （undo 本期禁用，不做跨行聚合）。
        final canonicalId =
            (res?['canonical_id'] ?? p.input['canonical_id'])?.toString();
        if (canonicalId == null || canonicalId.isEmpty) return;
        _merges.add(MaintainChange(
          pageId: canonicalId,
          title: page?['title']?.toString() ?? '',
          op: MaintainChangeOp.merge,
          firstWriteAt: p.at,
          lastWriteDoneAt: doneAt,
          afterBodyMd: page?['body_md']?.toString() ?? '',
          duplicateId:
              (res?['duplicate_id'] ?? p.input['duplicate_id'])?.toString(),
        ));
    }
  }

  /// 改动清单：按页聚合行（插入序）+ merge 行。
  List<MaintainChange> get changes => [..._byPage.values, ..._merges];
}

class _PendingWrite {
  _PendingWrite({required this.op, required this.input, required this.at});
  final MaintainChangeOp op;
  final Map<String, dynamic> input;
  final DateTime at;
}

/// 从版本列表（created_at 倒序）挑「本 run 写前态」快照：时间窗
/// [firstWriteAt - skew, lastWriteDoneAt + skew] 内最早一条 edit。
/// 找不到（>512KB 跳过快照 / 窗口外）返回 null —— 调用方标 diffUnavailable。
WikiPageRevision? pickBeforeRevision(
  List<WikiPageRevision> revisions, {
  required DateTime firstWriteAt,
  required DateTime lastWriteDoneAt,
  Duration skew = const Duration(seconds: 90),
}) {
  final lo = firstWriteAt.subtract(skew);
  final hi = lastWriteDoneAt.add(skew);
  WikiPageRevision? best;
  for (final r in revisions) {
    if (r.changeType != 'edit') continue;
    final t = r.createdAt.toUtc();
    if (t.isBefore(lo) || t.isAfter(hi)) continue;
    if (best == null || t.isBefore(best.createdAt.toUtc())) best = r;
  }
  return best;
}

/// 审计 UI 对后端的依赖面——widget 测试用 fake 实现隔掉网络。
abstract class MaintainAuditClient {
  Future<List<WikiPageRevision>> listRevisions(String projectId, String pageId);
  Future<WikiPageRevision> getRevision(
    String projectId,
    String pageId,
    String revisionId,
  );
  Future<void> restoreRevision(
    String projectId,
    String pageId,
    String revisionId,
  );
  Future<void> deletePage(String projectId, String pageId);
}

/// 生产实现：薄封装 [WikiClient]。
class WikiMaintainAuditClient implements MaintainAuditClient {
  WikiMaintainAuditClient(this._client);
  final WikiClient _client;

  @override
  Future<List<WikiPageRevision>> listRevisions(
    String projectId,
    String pageId,
  ) =>
      _client.listPageRevisions(projectId, pageId, limit: 50);

  @override
  Future<WikiPageRevision> getRevision(
    String projectId,
    String pageId,
    String revisionId,
  ) =>
      _client.getPageRevision(projectId, pageId, revisionId);

  @override
  Future<void> restoreRevision(
    String projectId,
    String pageId,
    String revisionId,
  ) =>
      _client.restorePageRevision(projectId, pageId, revisionId);

  @override
  Future<void> deletePage(String projectId, String pageId) =>
      _client.deletePage(projectId, pageId);
}
