// CodeTaskArtifactsDao behavioural tests — in-memory drift, hermetic.

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/code/data/code_task_artifacts_dao.dart';
import 'package:biumind/features/code/domain/artifact.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late AppDb db;
  late CodeTaskArtifactsDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = CodeTaskArtifactsDao(db);
  });
  tearDown(() => db.close());

  Artifact mk({
    String id = 'art-1',
    String taskId = 'task-A',
    ArtifactKind kind = ArtifactKind.codeFile,
    String relPath = 'lib/main.dart',
    int sizeBytes = 1024,
    String sha256 = 'abc123',
    ArtifactOp op = ArtifactOp.modified,
  }) {
    return Artifact(
      id: id,
      taskId: taskId,
      kind: kind,
      relPath: relPath,
      sizeBytes: sizeBytes,
      sha256: sha256,
      op: op,
      createdAt: DateTime.utc(2026, 5, 27, 9, 0),
    );
  }

  test('upsert + listByTask round-trips all fields', () async {
    await dao.upsert(mk());
    final list = await dao.listByTask('task-A');
    expect(list, hasLength(1));
    final got = list.first;
    expect(got.id, 'art-1');
    expect(got.kind, ArtifactKind.codeFile);
    expect(got.relPath, 'lib/main.dart');
    expect(got.sha256, 'abc123');
    expect(got.op, ArtifactOp.modified);
    expect(got.sizeBytes, 1024);
  });

  test('listByTask filters by taskId', () async {
    await dao.upsert(mk(id: 'a', taskId: 'T1'));
    await dao.upsert(mk(id: 'b', taskId: 'T2'));
    await dao.upsert(mk(id: 'c', taskId: 'T1', relPath: 'README.md'));

    final t1 = await dao.listByTask('T1');
    expect(t1.map((a) => a.id).toSet(), {'a', 'c'});
    final t2 = await dao.listByTask('T2');
    expect(t2.map((a) => a.id).toList(), ['b']);
  });

  test('upsert is idempotent — same id replaces (preview patch)', () async {
    await dao.upsert(mk());
    final patched = mk().copyWith(previewSummary: '+12 -3');
    await dao.upsert(patched);
    final list = await dao.listByTask('task-A');
    expect(list, hasLength(1)); // still single row
    expect(list.first.previewSummary, '+12 -3');
  });

  test('previewLevel reflects whether L2 preview data is present', () async {
    final l1 = mk(id: 'l1');
    expect(l1.previewLevel, 1);

    final l2 = l1.copyWith(previewSummary: '+12 -3', previewDataB64: 'aGVsbG8=');
    expect(l2.previewLevel, 2);
  });

  test('deleteByTask removes only that task\'s artifacts', () async {
    await dao.upsert(mk(id: 'a', taskId: 'T1'));
    await dao.upsert(mk(id: 'b', taskId: 'T2'));
    await dao.deleteByTask('T1');
    expect(await dao.listByTask('T1'), isEmpty);
    expect((await dao.listByTask('T2')).map((a) => a.id), ['b']);
  });

}
