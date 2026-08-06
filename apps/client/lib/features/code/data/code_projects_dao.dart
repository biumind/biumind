// CodeProjectsDao — 编码模块项目的 Drift 持久化层。M1 多项目。
//
// 零云同步:Drift 是唯一真相源(无 outbox / 无云端 pull)。LocalCodeProject ↔
// CodeProject 转换在本文件做,避免 domain 依赖 drift。

import 'package:drift/drift.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/local/db.dart' as drift;
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../domain/project.dart';

class CodeProjectsDao {
  CodeProjectsDao(this._db);
  final drift.AppDb _db;

  /// 排序:手动 sortIndex 升序优先,同值回退 lastOpenedAt 倒序(最近在前)。
  List<OrderingTerm Function(drift.$CodeProjectsTable)> get _ordering => [
        (p) => OrderingTerm(expression: p.sortIndex),
        (p) => OrderingTerm(expression: p.lastOpenedAt, mode: OrderingMode.desc),
      ];

  /// 监听全部项目。ProjectRail / WelcomePage 用。
  Stream<List<CodeProject>> watchAll() {
    final q = _db.select(_db.codeProjects)..orderBy(_ordering);
    return q.watch().map((rows) => rows.map(_fromLocal).toList());
  }

  /// 一次性加载全部(controller 启动 hydrate 用)。
  Future<List<CodeProject>> loadAll() async {
    final rows =
        await (_db.select(_db.codeProjects)..orderBy(_ordering)).get();
    return rows.map(_fromLocal).toList();
  }

  /// 按给定 id 顺序写回 sortIndex(拖拽排序持久化)。一个事务批量更新。
  Future<void> reorder(List<String> orderedIds) async {
    await _db.transaction(() async {
      for (var i = 0; i < orderedIds.length; i++) {
        await (_db.update(_db.codeProjects)
              ..where((p) => p.id.equals(orderedIds[i])))
            .write(drift.CodeProjectsCompanion(sortIndex: Value(i)));
      }
    });
  }

  Future<void> upsert(CodeProject p) {
    return _db.into(_db.codeProjects).insertOnConflictUpdate(_toLocal(p));
  }

  /// 更新最后打开时间(切到该项目时调)。
  Future<void> touch(String id, DateTime at) {
    return (_db.update(_db.codeProjects)..where((p) => p.id.equals(id)))
        .write(drift.CodeProjectsCompanion(
      lastOpenedAt: Value(at.millisecondsSinceEpoch),
    ));
  }

  /// 设置隐藏标记(不删,只从 Rail 隐藏)。
  Future<void> setHidden(String id, bool hidden) {
    return (_db.update(_db.codeProjects)..where((p) => p.id.equals(id)))
        .write(drift.CodeProjectsCompanion(hiddenFromRail: Value(hidden)));
  }

  Future<void> deleteById(String id) {
    return (_db.delete(_db.codeProjects)..where((p) => p.id.equals(id))).go();
  }

  // ─── Mappers ──────────────────────────────────────────

  drift.CodeProjectsCompanion _toLocal(CodeProject p) =>
      drift.CodeProjectsCompanion.insert(
        id: p.id,
        name: p.name,
        path: p.path,
        branch: Value(p.branch),
        lastOpenedAt: Value(p.lastOpenedAt.millisecondsSinceEpoch),
        hiddenFromRail: Value(p.hiddenFromRail),
        avatarColor: Value(p.avatarColor),
        sortIndex: Value(p.sortIndex),
      );

  CodeProject _fromLocal(drift.LocalCodeProject r) => CodeProject(
        id: r.id,
        name: r.name,
        path: r.path,
        branch: r.branch,
        lastOpenedAt: DateTime.fromMillisecondsSinceEpoch(r.lastOpenedAt),
        hiddenFromRail: r.hiddenFromRail,
        avatarColor: r.avatarColor,
        sortIndex: r.sortIndex,
      );
}

// ─── Provider ──────────────────────────────────────────
// 复用全局单例 appDbProvider(避免多个 AppDb 实例共用同一 sqlite 文件 → Drift
// 竞态/损坏告警)。生命周期由 appDbProvider 管,这里不再各自 new / close。

final codeProjectsDaoProvider = Provider<CodeProjectsDao>((ref) {
  return CodeProjectsDao(ref.watch(appDbProvider));
});
