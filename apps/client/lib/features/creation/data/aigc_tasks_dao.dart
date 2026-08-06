// AigcTasksDao — 创作任务本地持久化 DAO. v2-1.
//
// 架构:
//   tasks_controller (in-memory state) ←→ AigcTasksDao (drift)
//   - start: load 一次本地最近 100 条 → state, 再起 SSE/poll 增量更新
//   - 每次 state.tasks[id] 写入/更新 → 异步写本地 (best-effort, 失败不阻塞 UI)
//   - delete (用户删作品) → 清本地行 + 调 server delete
//
// 设计选择:
//   - 不存 outputs 的 url (cas:sha 是云端引用, 重启后只要还登录就能拉)
//   - outputs_json + params_json 整体 JSON, 不拆表 (访问总是整 task 取出)
//   - 不做"软删除" — 用户主动 delete 直接物理 DELETE

import 'dart:convert';

import 'package:drift/drift.dart';

import '../../../data/local/db.dart';
import '../domain/creation_task.dart';

part 'aigc_tasks_dao.g.dart';

@DriftAccessor(tables: [AigcTasks])
class AigcTasksDao extends DatabaseAccessor<AppDb> with _$AigcTasksDaoMixin {
  AigcTasksDao(super.db);

  /// loadRecent — 启动时拿最近 N 条任务. 默认 100, 跟 SSE 合并去重.
  Future<List<CreationTask>> loadRecent({int limit = 100}) async {
    final rows = await (select(aigcTasks)
          ..orderBy([(t) => OrderingTerm.desc(t.createdAt)])
          ..limit(limit))
        .get();
    return rows.map(_rowToTask).toList(growable: false);
  }

  /// loadByUser — 仅指定 user 的任务. 多账号切换时用.
  Future<List<CreationTask>> loadByUser(String userId, {int limit = 100}) async {
    final rows = await (select(aigcTasks)
          ..where((t) => t.userId.equals(userId))
          ..orderBy([(t) => OrderingTerm.desc(t.createdAt)])
          ..limit(limit))
        .get();
    return rows.map(_rowToTask).toList(growable: false);
  }

  /// upsert — 写入/更新单条. 用于 SSE 增量更新 + 用户提交占位.
  Future<void> upsert(CreationTask t) async {
    await into(aigcTasks).insertOnConflictUpdate(_taskToCompanion(t));
  }

  /// upsertAll — 批量, 启动 _refreshActive 拉到一批 active 时一次写完.
  Future<void> upsertAll(Iterable<CreationTask> tasks) async {
    await batch((b) {
      b.insertAllOnConflictUpdate(
        aigcTasks,
        tasks.map(_taskToCompanion).toList(growable: false),
      );
    });
  }

  /// deleteById — 用户删作品后清本地. 不存在静默 noop.
  Future<void> deleteById(String id) async {
    await (delete(aigcTasks)..where((t) => t.id.equals(id))).go();
  }

  /// renameLocalId — 占位 task 拿到真 id 后, 用真 id 重新插入 + 删占位.
  /// 单事务保证不会出现两条同 prompt 任务并存的中间态.
  Future<void> renameLocalId({
    required String tempId,
    required CreationTask realTask,
  }) async {
    await transaction(() async {
      await deleteById(tempId);
      await upsert(realTask);
    });
  }

  /// deleteAll — 退出登录时清空本地 (避免下个用户看到上家任务).
  Future<void> deleteAll() async {
    await delete(aigcTasks).go();
  }

  // ─── helpers ─────────────────────────────────

  CreationTask _rowToTask(LocalAigcTask r) {
    final outputs = (jsonDecode(r.outputsJson) as List)
        .whereType<Map<String, dynamic>>()
        .map(TaskOutput.fromJson)
        .toList();
    final params = (jsonDecode(r.paramsJson) as Map<String, dynamic>);
    return CreationTask(
      id: r.id,
      userId: r.userId,
      type: r.type,
      modelCode: r.modelCode,
      providerCode: r.providerCode,
      prompt: r.prompt,
      negativePrompt: r.negativePrompt,
      params: params,
      status: TaskStatus.fromWire(r.status),
      progress: r.progress,
      errorCode: r.errorCode,
      errorMessage: r.errorMessage,
      costCredits: r.costCredits,
      refundedCredits: r.refundedCredits,
      isPublic: r.isPublic,
      outputs: outputs,
      createdAt: r.createdAt,
      queuedAt: r.queuedAt,
      startedAt: r.startedAt,
      completedAt: r.completedAt,
      updatedAt: r.updatedAt,
      localTempId: r.localTempId,
    );
  }

  AigcTasksCompanion _taskToCompanion(CreationTask t) {
    return AigcTasksCompanion(
      id: Value(t.id),
      userId: Value(t.userId),
      type: Value(t.type),
      modelCode: Value(t.modelCode),
      providerCode: Value(t.providerCode),
      status: Value(t.status.wire),
      progress: Value(t.progress),
      prompt: Value(t.prompt),
      negativePrompt: Value(t.negativePrompt),
      paramsJson: Value(jsonEncode(t.params)),
      outputsJson: Value(jsonEncode(t.outputs.map((o) => o.toJson()).toList())),
      costCredits: Value(t.costCredits),
      refundedCredits: Value(t.refundedCredits),
      isPublic: Value(t.isPublic),
      errorCode: Value(t.errorCode),
      errorMessage: Value(t.errorMessage),
      localTempId: Value(t.localTempId),
      createdAt: Value(t.createdAt),
      queuedAt: Value(t.queuedAt),
      startedAt: Value(t.startedAt),
      completedAt: Value(t.completedAt),
      updatedAt: Value(t.updatedAt),
    );
  }
}
