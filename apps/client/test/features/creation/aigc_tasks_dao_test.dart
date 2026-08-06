// AigcTasksDao — drift 持久化层单测.
// 用 AppDb.memory() 跑 in-memory sqlite, 不依赖文件系统.

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/local/db.dart';
import 'package:biumind/features/creation/data/aigc_tasks_dao.dart';
import 'package:biumind/features/creation/domain/creation_task.dart';

CreationTask _task(String id, {
  String userId = 'u1',
  TaskStatus status = TaskStatus.pending,
  int progress = 0,
  String prompt = 'a cat',
  Map<String, dynamic> params = const {'aspect_ratio': '16:9'},
  List<TaskOutput> outputs = const [],
  int costCredits = 100,
  int refundedCredits = 0,
  String? localTempId,
}) {
  final now = DateTime.now();
  return CreationTask(
    id: id,
    userId: userId,
    type: 'image',
    modelCode: 'wanx-2.6-t2i',
    providerCode: 'dashscope',
    prompt: prompt,
    params: params,
    status: status,
    progress: progress,
    costCredits: costCredits,
    refundedCredits: refundedCredits,
    outputs: outputs,
    createdAt: now,
    updatedAt: now,
    localTempId: localTempId,
  );
}

void main() {
  late AppDb db;
  late AigcTasksDao dao;

  setUp(() {
    db = AppDb.memory();
    dao = AigcTasksDao(db);
  });

  tearDown(() async {
    await db.close();
  });

  test('upsert + loadRecent round-trip 保留全部字段', () async {
    final t = _task('t1',
        status: TaskStatus.completed, progress: 100,
        params: {'aspect_ratio': '16:9', 'seed': 42},
        outputs: [
          const TaskOutput(
            idx: 0, kind: 'image', sha256: 'abc', url: 'cas:abc',
            blurhash: 'L9AB...', width: 1024, height: 1024,
          ),
        ]);
    await dao.upsert(t);

    final got = await dao.loadRecent(limit: 10);
    expect(got, hasLength(1));
    final r = got.first;
    expect(r.id, 't1');
    expect(r.status, TaskStatus.completed);
    expect(r.progress, 100);
    expect(r.params['seed'], 42);
    expect(r.outputs, hasLength(1));
    expect(r.outputs.first.blurhash, 'L9AB...');
    expect(r.outputs.first.width, 1024);
  });

  test('upsert 同 id 覆盖旧值', () async {
    await dao.upsert(_task('t2', progress: 30));
    await dao.upsert(_task('t2', progress: 90, status: TaskStatus.running));

    final got = await dao.loadRecent();
    expect(got, hasLength(1));
    expect(got.first.progress, 90);
    expect(got.first.status, TaskStatus.running);
  });

  test('loadByUser 仅返指定 user', () async {
    await dao.upsert(_task('a1', userId: 'alice'));
    await dao.upsert(_task('b1', userId: 'bob'));
    await dao.upsert(_task('a2', userId: 'alice'));

    final aliceTasks = await dao.loadByUser('alice');
    expect(aliceTasks.map((t) => t.id), containsAll(['a1', 'a2']));
    expect(aliceTasks.any((t) => t.userId == 'bob'), isFalse);
  });

  test('upsertAll 批量 + 不冲突', () async {
    await dao.upsertAll([
      _task('t1'), _task('t2'), _task('t3', progress: 50),
    ]);
    final got = await dao.loadRecent();
    expect(got, hasLength(3));
    expect(got.firstWhere((t) => t.id == 't3').progress, 50);
  });

  test('renameLocalId 占位 → 真 id 单事务', () async {
    await dao.upsert(_task('temp-xyz',
        status: TaskStatus.submitting, localTempId: 'temp-xyz'));
    await dao.renameLocalId(
      tempId: 'temp-xyz',
      realTask: _task('real-001',
          status: TaskStatus.pending, localTempId: 'temp-xyz'),
    );
    final got = await dao.loadRecent();
    expect(got, hasLength(1));
    expect(got.first.id, 'real-001');
    expect(got.first.localTempId, 'temp-xyz');
  });

  test('deleteById 真物理删', () async {
    await dao.upsert(_task('t1'));
    await dao.deleteById('t1');
    final got = await dao.loadRecent();
    expect(got, isEmpty);
  });

  test('deleteAll 清表 (退出登录路径)', () async {
    await dao.upsertAll([_task('a'), _task('b'), _task('c')]);
    await dao.deleteAll();
    expect(await dao.loadRecent(), isEmpty);
  });

  test('loadRecent 按 createdAt 降序', () async {
    final now = DateTime.now();
    final older = CreationTask(
      id: 'older', userId: 'u1', type: 'image', modelCode: 'm',
      prompt: 'p', status: TaskStatus.completed,
      createdAt: now.subtract(const Duration(hours: 1)),
      updatedAt: now,
    );
    final newer = CreationTask(
      id: 'newer', userId: 'u1', type: 'image', modelCode: 'm',
      prompt: 'p', status: TaskStatus.completed,
      createdAt: now,
      updatedAt: now,
    );
    await dao.upsert(older);
    await dao.upsert(newer);

    final got = await dao.loadRecent();
    expect(got.first.id, 'newer');
    expect(got.last.id, 'older');
  });
}
