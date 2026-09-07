// maintain_changes — wiki maintain agent 的「本 run 改动清单」纯客户端聚合
// + run 历史模型（BiuMind-Agent-Experience-Design §1.2 P1 聚合 / P2 run 关联+OCC）。
//
// maintain agent 走 SSE（BlockEmitter v2 事件），tool.created 带完整 input、
// tool.completed 带 result，此前 maintain_dialog 显式丢弃。本文件把它们聚成
// 逐页改动清单，供 run 结束后的审计 UI（diff + 逐条 undo）消费。
//
// 关键语义（诚实降级，不许猜）：
//   * run 级 before = 该页第一条写操作对应的 edit 快照。P2 起快照带 run_id，
//     有 runId 时按 run_id 精确匹配（最早一条）；旧 run（无 run_id 快照）
//     回退时间窗匹配：快照 created_at ∈ [firstWriteAt - skew,
//     lastWriteDoneAt + skew] 的最早一条 edit。
//   * 快照可能不存在（blocks_json >512KB 服务端整条跳过）→ diffUnavailable，
//     undo 同步禁用——宁可标不可用也不猜。
//   * undo OCC（P2）：update 的 undo 传 if_match_version = 本 run 最后写完的
//     version（result.page.version），run 之后页面又被改过 → 409，客户端
//     展示当前态 vs 目标态 diff 由用户确认后才无 if_match 重试。
//   * create 无快照 → undo = 删页；merge 的 undo 本期不做（需 un-delete
//     duplicate，服务端还不支持）。

import '../../../data/api/wiki_client.dart';
import '../../../data/api/_http_helpers.dart' show ApiError;

/// 409 判定：restore 带 if_match_version 被服务端拒绝（run 之后有新修改）。
bool isVersionConflict(Object e) => e is ApiError && e.status == 409;

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

  /// 本 run 最后写完的 page version（result.page.version，undo 的
  /// if_match_version 用）；null = 未取得（旧事件流/异常结果）。
  int? afterVersion;

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
        )..afterVersion = (page?['version'] as num?)?.toInt();
      case MaintainChangeOp.update:
        // input: {page_id, version(改前), body_md(新全文)}；result.page = 改后。
        final id =
            (p.input['page_id'] ?? page?['id'])?.toString();
        if (id == null || id.isEmpty) return;
        final afterBody = page?['body_md']?.toString() ??
            p.input['body_md']?.toString() ??
            '';
        final title = page?['title']?.toString() ?? '';
        final afterVer = (page?['version'] as num?)?.toInt();
        final existing = _byPage[id];
        if (existing != null) {
          // 同页多次写：before 保持首条（含 create 后 update —— undo 仍是删页），
          // after 滚动到最新。
          existing.afterBodyMd = afterBody;
          existing.lastWriteDoneAt = doneAt;
          if (afterVer != null) existing.afterVersion = afterVer;
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
          )..afterVersion = afterVer;
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
        )..afterVersion = (page?['version'] as num?)?.toInt());
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

/// 从版本列表（created_at 倒序）挑「本 run 写前态」快照。
///
/// P2 起快照带 run_id：[runId] 非空时优先精确匹配——run_id == runId 的
/// 最早一条 edit（run 内同页多次写经 5min 窗口合并只留首条写前快照）。
/// 无 run_id 快照可匹配（旧 run / 服务端旧版本）时回退时间窗匹配：
/// [firstWriteAt - skew, lastWriteDoneAt + skew] 内最早一条 edit。
/// 找不到（>512KB 跳过快照 / 窗口外）返回 null —— 调用方标 diffUnavailable。
WikiPageRevision? pickBeforeRevision(
  List<WikiPageRevision> revisions, {
  required DateTime firstWriteAt,
  required DateTime lastWriteDoneAt,
  String? runId,
  Duration skew = const Duration(seconds: 90),
}) {
  if (runId != null && runId.isNotEmpty) {
    WikiPageRevision? best;
    for (final r in revisions) {
      if (r.changeType != 'edit' || r.runId != runId) continue;
      if (best == null || r.createdAt.isBefore(best.createdAt)) best = r;
    }
    if (best != null) return best;
    // 该 run 在此页没有带 run_id 的快照 → 不写前态可指认，回退时间窗
    // 也不安全（可能命中人工快照）——但对旧 run（服务端未落 run_id）必须
    // 回退。区分不了两者时按「列表里完全没有 run_id 快照」判旧 run。
    final hasAnyRunTagged = revisions.any((r) => r.runId != null);
    if (hasAnyRunTagged) return null;
  }
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

  /// [ifMatchVersion]（P2 undo OCC）：传本 run 最后写完的 version；
  /// run 之后页面被改过 → 服务端 409。null = 不校验（确认后重试用）。
  Future<void> restoreRevision(
    String projectId,
    String pageId,
    String revisionId, {
    int? ifMatchVersion,
  });
  Future<void> deletePage(String projectId, String pageId);

  /// 409 确认 diff 用：取页面当前态（body_md + version）。
  Future<WikiPage> getPage(String projectId, String pageId);

  /// P2 run 历史：列表（含改动页数）+ 详情（改动页清单）。
  Future<List<WikiAgentRun>> listAgentRuns(String projectId);
  Future<(WikiAgentRun, List<WikiAgentRunChange>)> getAgentRun(
    String projectId,
    String runId,
  );
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
    String revisionId, {
    int? ifMatchVersion,
  }) =>
      _client.restorePageRevision(projectId, pageId, revisionId,
          ifMatchVersion: ifMatchVersion);

  @override
  Future<void> deletePage(String projectId, String pageId) =>
      _client.deletePage(projectId, pageId);

  @override
  Future<WikiPage> getPage(String projectId, String pageId) =>
      _client.getPage(projectId, pageId);

  @override
  Future<List<WikiAgentRun>> listAgentRuns(String projectId) =>
      _client.listAgentRuns(projectId);

  @override
  Future<(WikiAgentRun, List<WikiAgentRunChange>)> getAgentRun(
    String projectId,
    String runId,
  ) =>
      _client.getAgentRun(projectId, runId);
}
