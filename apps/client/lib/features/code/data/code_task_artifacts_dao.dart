// CodeTaskArtifactsDao — artifacts 元数据本地持久化层。
// 对应表 CodeTaskArtifacts (Drift schema v6+)。

import 'package:drift/drift.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/local/db.dart' as drift;
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../domain/artifact.dart';

class CodeTaskArtifactsDao {
  CodeTaskArtifactsDao(this._db);
  final drift.AppDb _db;

  /// 单条 upsert (按 id 主键)。controller 收集时一条条调。
  Future<void> upsert(Artifact a) {
    return _db
        .into(_db.codeTaskArtifacts)
        .insertOnConflictUpdate(_toLocal(a));
  }

  /// 列出某任务的全部 artifact, 按 createdAt 升序 (创建顺序展示)。
  Future<List<Artifact>> listByTask(String taskId) async {
    final rows = await (_db.select(_db.codeTaskArtifacts)
          ..where((t) => t.taskId.equals(taskId))
          ..orderBy([
            (t) => OrderingTerm(expression: t.createdAt, mode: OrderingMode.asc),
          ]))
        .get();
    return rows.map(_fromLocal).toList();
  }

  /// 监听某任务的 artifact 列表 (UI Files 面板用)。
  Stream<List<Artifact>> watchByTask(String taskId) {
    final q = _db.select(_db.codeTaskArtifacts)
      ..where((t) => t.taskId.equals(taskId))
      ..orderBy([
        (t) => OrderingTerm(expression: t.createdAt, mode: OrderingMode.asc),
      ]);
    return q.watch().map((rows) => rows.map(_fromLocal).toList());
  }

  /// 任务删除时一并清掉所有 artifact (SoftDelete 也清, 让 UI 干净)。
  Future<int> deleteByTask(String taskId) {
    return (_db.delete(_db.codeTaskArtifacts)
          ..where((t) => t.taskId.equals(taskId)))
        .go();
  }

  /// 全清 (设置页"清空本地数据"用)。
  Future<int> clear() => _db.delete(_db.codeTaskArtifacts).go();

  // ─── Mappers ──────────────────────────────────────────

  drift.CodeTaskArtifactsCompanion _toLocal(Artifact a) =>
      drift.CodeTaskArtifactsCompanion.insert(
        id: a.id,
        taskId: a.taskId,
        kind: a.kind.label,
        relPath: a.relPath,
        mimeType: Value(a.mimeType),
        sizeBytes: Value(a.sizeBytes),
        sha256: a.sha256,
        op: a.op.label,
        previewSummary: Value(a.previewSummary),
        previewDataB64: Value(a.previewDataB64),
        previewMimeType: Value(a.previewMimeType),
        createdAt: a.createdAt,
      );

  Artifact _fromLocal(drift.LocalCodeTaskArtifact r) => Artifact(
        id: r.id,
        taskId: r.taskId,
        kind: _kindFromLabel(r.kind),
        relPath: r.relPath,
        mimeType: r.mimeType,
        sizeBytes: r.sizeBytes,
        sha256: r.sha256,
        op: _opFromLabel(r.op),
        previewSummary: r.previewSummary,
        previewDataB64: r.previewDataB64,
        previewMimeType: r.previewMimeType,
        createdAt: r.createdAt,
      );

  static ArtifactKind _kindFromLabel(String s) => switch (s) {
        'code' => ArtifactKind.codeFile,
        'image' => ArtifactKind.image,
        'document' => ArtifactKind.document,
        'audio' => ArtifactKind.audio,
        'video' => ArtifactKind.video,
        'dataset' => ArtifactKind.dataset,
        _ => ArtifactKind.binary,
      };

  static ArtifactOp _opFromLabel(String s) => switch (s) {
        'create' => ArtifactOp.created,
        'modify' => ArtifactOp.modified,
        'delete' => ArtifactOp.deleted,
        _ => ArtifactOp.modified,
      };
}

// 复用全局单例 appDbProvider —— 不再各自 new AppDb(避免 Drift 多实例告警)。
final codeTaskArtifactsDaoProvider = Provider<CodeTaskArtifactsDao>((ref) {
  return CodeTaskArtifactsDao(ref.watch(appDbProvider));
});
