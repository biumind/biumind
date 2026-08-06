// CodeTasksDao — Drift 持久化层。任务元数据 + 流式事件 + workspace ref 全
// 序列化进 sqlite, 重启 app 后任务列表 + Agent stream 完整恢复。
//
// 设计:
//   - 流式事件每个 event 来都立即 upsert 整 row (含 eventsJson 全量)。
//     SQLite 批量写吞吐量充足 (千次/秒), 简单可靠。性能瓶颈出现时再加 200ms
//     debounce / 增量 outbox。
//   - watchAll() 暴露 Stream<List<CodeTask>>, controller hydrate 一次性
//     拉到内存 list, 不直接绑 reactive UI (event burst 时整列表 rebuild
//     太重)。
//   - LocalCodeTask ↔ CodeTask 在本文件转换 (避免 domain 依赖 drift)。

import 'dart:convert';

import 'package:drift/drift.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/local/db.dart' as drift;
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../domain/code_task.dart';
import '../domain/workspace.dart';

class CodeTasksDao {
  CodeTasksDao(this._db);
  final drift.AppDb _db;

  /// 监听全部任务, 按 createdAt 倒序 (最新在前)。
  Stream<List<CodeTask>> watchAll() {
    final q = _db.select(_db.codeTasks)
      ..orderBy([
        (t) => OrderingTerm(expression: t.createdAt, mode: OrderingMode.desc),
      ]);
    return q.watch().map((rows) => rows.map(_fromLocal).toList());
  }

  /// 一次性加载全部 (controller 启动 hydrate 用)。
  Future<List<CodeTask>> loadAll() async {
    final rows = await (_db.select(_db.codeTasks)
          ..orderBy([
            (t) => OrderingTerm(expression: t.createdAt, mode: OrderingMode.desc),
          ]))
        .get();
    return rows.map(_fromLocal).toList();
  }

  /// 插入或更新 — 流式事件每来一次都调一次。
  Future<void> upsert(CodeTask task) {
    return _db
        .into(_db.codeTasks)
        .insertOnConflictUpdate(_toLocal(task));
  }

  Future<void> deleteById(String id) {
    return (_db.delete(_db.codeTasks)..where((t) => t.id.equals(id))).go();
  }

  Future<void> clear() {
    return _db.delete(_db.codeTasks).go();
  }

  // ─── Mappers ──────────────────────────────────────────

  drift.CodeTasksCompanion _toLocal(CodeTask t) =>
      drift.CodeTasksCompanion.insert(
        id: t.id,
        title: t.title,
        prompt: t.prompt,
        agent: t.agent.name,
        mode: t.mode.name,
        status: t.status.name,
        eventsJson: Value(jsonEncode(
          t.events.map((e) => e.toJson()).toList(),
        )),
        costUsd: Value(t.cost.usd),
        inputTokens: Value(t.cost.inputTokens),
        outputTokens: Value(t.cost.outputTokens),
        createdAt: t.createdAt,
        completedAt: Value(t.completedAt),
        errorMessage: Value(t.errorMessage),
        workspaceJson: Value(
          t.workspace == null ? null : jsonEncode(t.workspace!.toJson()),
        ),
        compareGroupId: Value(t.compareGroupId),
        originDeviceId: Value(t.originDeviceId),
        originDeviceLabel: Value(t.originDeviceLabel),
        projectId: Value(t.projectId),
        updatedAt: Value(t.updatedAt),
        model: Value(t.model),
        starred: Value(t.starred),
      );

  CodeTask _fromLocal(drift.LocalCodeTask r) {
    final eventsRaw = jsonDecode(r.eventsJson);
    final events = <AgentEvent>[];
    if (eventsRaw is List) {
      for (final e in eventsRaw) {
        if (e is Map<String, dynamic>) {
          final parsed = AgentEvent.fromJson(e);
          if (parsed != null) events.add(parsed);
        }
      }
    }

    WorkspaceRef? workspace;
    if (r.workspaceJson != null && r.workspaceJson!.isNotEmpty) {
      try {
        final wj = jsonDecode(r.workspaceJson!);
        if (wj is Map<String, dynamic>) {
          workspace = WorkspaceRef.fromJson(wj);
        }
      } catch (_) {/* corrupted ref, ignore */}
    }

    return CodeTask(
      id: r.id,
      title: r.title,
      prompt: r.prompt,
      agent: _agentFromName(r.agent),
      mode: _modeFromName(r.mode),
      status: _statusFromName(r.status),
      events: events,
      cost: TaskCost(
        usd: r.costUsd,
        inputTokens: r.inputTokens,
        outputTokens: r.outputTokens,
      ),
      createdAt: r.createdAt,
      completedAt: r.completedAt,
      errorMessage: r.errorMessage,
      workspace: workspace,
      compareGroupId: r.compareGroupId,
      originDeviceId: r.originDeviceId,
      originDeviceLabel: r.originDeviceLabel,
      projectId: r.projectId,
      updatedAt: r.updatedAt,
      model: r.model,
      starred: r.starred,
    );
  }

  static AgentKind _agentFromName(String name) =>
      AgentKind.values.firstWhere(
        (k) => k.name == name,
        orElse: () => AgentKind.biu,
      );

  static PermissionMode _modeFromName(String name) =>
      PermissionMode.values.firstWhere(
        (m) => m.name == name,
        orElse: () => PermissionMode.ask,
      );

  static CodeTaskStatus _statusFromName(String name) =>
      CodeTaskStatus.values.firstWhere(
        (s) => s.name == name,
        orElse: () => CodeTaskStatus.failed,
      );
}

// ─── Provider ──────────────────────────────────────────
//
// 复用全局单例 appDbProvider(与 wiki/chat 一致),避免多 AppDb 实例共用同一 sqlite
// 文件触发 Drift 竞态/损坏告警。生命周期由 appDbProvider 管。

final codeTasksDaoProvider = Provider<CodeTasksDao>((ref) {
  return CodeTasksDao(ref.watch(appDbProvider));
});
