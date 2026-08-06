// TasksController 单测 — 不依赖真 RealtimeHub / 网络.
//
// 策略: TasksController 内部用 RealtimeHub, 但 SSE 主通道走真 endpoint 太重;
// 用 realtimeEndpoint=null 走兜底轮询路径, 用 fake AigcClient 打桩.
// 对纯状态机 (applyTask / replaceLocalId / removeTask) 直接测.

import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/sse/sse_cursors_dao.dart';
import 'package:biumind/features/creation/application/tasks_controller.dart';
import 'package:biumind/features/creation/data/aigc_client.dart';
import 'package:biumind/features/creation/domain/creation_task.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeAigcClient extends AigcClient {
  _FakeAigcClient()
      : super(
          baseUrl: Uri.parse('http://test.local'),
          bearerProvider: () => 'tok',
        );

  List<CreationTask> activeTasks = [];
  CreationTask? Function(String id)? getTaskHandler;
  AigcSubmitResult Function() submitHandler = () => throw UnimplementedError();
  bool throwOnFetch = false;

  @override
  Future<List<CreationTask>> fetchMyTasks({
    List<TaskStatus> statuses = const [],
    String? type,
    int limit = 50,
    int offset = 0,
  }) async {
    if (throwOnFetch) throw Exception('network down');
    return activeTasks;
  }

  @override
  Future<CreationTask> getTask(String id, {bool includeLineage = false}) async {
    final h = getTaskHandler;
    if (h != null) {
      final t = h(id);
      if (t != null) return t;
    }
    throw Exception('not found');
  }

  @override
  Future<AigcSubmitResult> submit({
    required String type,
    required String modelCode,
    required String prompt,
    String? negativePrompt,
    Map<String, dynamic> params = const {},
    bool isPublic = false,
    String? parentSha,
    String? lineageOp,
    String? idempotencyKey,
  }) async =>
      submitHandler();

  @override
  Future<void> cancelTask(String id) async {}

  @override
  Future<void> deleteTask(String id) async {}

  @override
  Future<void> setVisibility(String id, {required bool isPublic}) async {}
}

CreationTask _task(String id, {TaskStatus status = TaskStatus.pending, int progress = 0}) {
  final now = DateTime.now().toUtc();
  return CreationTask(
    id: id,
    userId: 'u1',
    type: 'image',
    modelCode: 'wanx-2.6-t2i',
    prompt: 'p',
    status: status,
    progress: progress,
    createdAt: now,
    updatedAt: now,
  );
}

TasksController _newController(_FakeAigcClient client) => TasksController(
      client: client,
      realtimeEndpoint: null, // 跳 SSE, 强制走兜底路径 (start 拉一次 + connectSse 退化)
      tokenProvider: () async => 'tok',
      userId: 'u1',
    );

void main() {
  group('TasksState applyTask', () {
    test('插入新 task', () {
      final s = const TasksState().applyTask(_task('a'));
      expect(s.tasks.containsKey('a'), isTrue);
      expect(s.tasks['a']!.status, TaskStatus.pending);
    });

    test('已有 task: progress 单调递增, status 推进', () {
      var s = const TasksState().applyTask(_task('a', progress: 30));
      s = s.applyTask(_task('a',
          status: TaskStatus.running, progress: 60));
      expect(s.tasks['a']!.progress, 60);
      expect(s.tasks['a']!.status, TaskStatus.running);

      // 进度回退应该被拒
      s = s.applyTask(_task('a', status: TaskStatus.running, progress: 50));
      expect(s.tasks['a']!.progress, 60);

      // terminal 不能被回退到 running
      s = s.applyTask(_task('a',
          status: TaskStatus.completed, progress: 100));
      s = s.applyTask(_task('a', status: TaskStatus.running, progress: 100));
      expect(s.tasks['a']!.status, TaskStatus.completed);
    });

    test('outputs 非空时覆盖, 空时保留', () {
      var s = const TasksState().applyTask(_task('a'));
      final completed = _task('a', status: TaskStatus.completed, progress: 100);
      // 模拟 completed 时一次性带回 outputs
      final withOutputs = CreationTask.fromJson({
        ...completed.toJson(),
        'outputs': [
          {'idx': 0, 'kind': 'image', 'sha256': 'sha-1', 'url': 'cas:sha-1'}
        ],
      });
      s = s.applyTask(withOutputs);
      expect(s.tasks['a']!.outputs.length, 1);
      expect(s.tasks['a']!.outputs.first.sha256, 'sha-1');
    });
  });

  group('TasksState activeIds 排序', () {
    test('仅 active, 按 createdAt desc', () {
      final old = _task('old', status: TaskStatus.running);
      final newer = CreationTask(
        id: 'new', userId: 'u1', type: 'image',
        modelCode: 'm', prompt: 'p',
        status: TaskStatus.queued, progress: 0,
        createdAt: DateTime.now().toUtc().add(const Duration(seconds: 1)),
        updatedAt: DateTime.now().toUtc(),
      );
      final completed = _task('done', status: TaskStatus.completed);
      var s = const TasksState();
      s = s.applyTask(old).applyTask(newer).applyTask(completed);
      expect(s.activeIds, ['new', 'old']);
    });
  });

  group('TasksState replaceLocalId', () {
    test('local-tempId 替换为真 id, localTempId 链接保留', () {
      final temp = CreationTask.localSubmitting(
        tempId: 'local-1', userId: 'u1',
        type: 'image', modelCode: 'm', prompt: 'p',
      );
      final real = _task('real-id-xyz');
      var s = const TasksState().applyTask(temp);
      s = s.replaceLocalId('local-1', real);
      expect(s.tasks.containsKey('local-1'), isFalse);
      expect(s.tasks.containsKey('real-id-xyz'), isTrue);
      expect(s.tasks['real-id-xyz']!.localTempId, 'local-1');
    });

    test('找不到 tempId 时容错插入 realTask (UI 调用最终一定要看到真 task)', () {
      final s = const TasksState().replaceLocalId('xx', _task('y'));
      // tempId 不在不报错; realTask 仍被加入 state
      expect(s.tasks.containsKey('y'), isTrue);
      expect(s.tasks.containsKey('xx'), isFalse);
    });
  });

  group('Controller submit 乐观更新', () {
    test('成功: 占位 → 真 task', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);

      client.submitHandler = () => AigcSubmitResult(
            task: _task('real-1', status: TaskStatus.pending),
            estimatedSeconds: 30,
            costCredits: 30,
            balanceAfter: 70,
          );

      final task = await c.submit(
        type: 'image',
        modelCode: 'm',
        prompt: 'x',
        params: const {},
      );
      expect(task.id, 'real-1');
      // 占位已被替换, 真 task 在; 旧 local-* 不在
      final ids = c.state.tasks.keys.toList();
      expect(ids, contains('real-1'));
      expect(ids.where((k) => k.startsWith('local-')), isEmpty);
      expect(c.state.tasks['real-1']!.localTempId, isNotNull);
      c.dispose();
    });

    test('失败: 占位被移除, 异常抛出', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);

      client.submitHandler = () => throw Exception('insufficient credits');

      await expectLater(
        c.submit(
          type: 'image', modelCode: 'm', prompt: 'x', params: const {},
        ),
        throwsException,
      );
      // 占位应被移除
      expect(c.state.tasks.values.where((t) => t.localTempId != null), isEmpty);
      c.dispose();
    });
  });

  group('Controller delete', () {
    test('调 client.deleteTask + 本地移除', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);

      // 模拟先有一个 task
      final t = _task('to-del', status: TaskStatus.completed);
      c.state.applyTask(t); // not via setter, just use state directly via controller.submit
      // 用 controller 的 submit 路径插入真 task
      client.submitHandler = () => AigcSubmitResult(
            task: t, estimatedSeconds: 0, costCredits: 0, balanceAfter: 0,
          );
      await c.submit(
        type: 'image', modelCode: 'm', prompt: 'x', params: const {},
      );
      expect(c.state.tasks.containsKey('to-del'), isTrue);

      await c.delete('to-del');
      expect(c.state.tasks.containsKey('to-del'), isFalse);
      c.dispose();
    });
  });

  group('Controller _refreshActive (兜底轮询路径)', () {
    test('网络 ok: active tasks 入 state', () async {
      final client = _FakeAigcClient()
        ..activeTasks = [
          _task('p1', status: TaskStatus.pending),
          _task('r1', status: TaskStatus.running, progress: 50),
        ];
      final c = _newController(client);
      await c.start(); // 内部会调 _refreshActive (realtimeEndpoint=null → 立即走兜底)
      expect(c.state.tasks.length, 2);
      expect(c.state.tasks['r1']!.progress, 50);
      await c.stop();
    });

    test('fetchMyTasks 抛出: connection 状态切到 offline', () async {
      final client = _FakeAigcClient()..throwOnFetch = true;
      final c = _newController(client);
      await c.start();
      expect(c.state.connection, ConnectionState.offline);
      await c.stop();
    });
  });

  // ─── 通知流 (C-1: 失败/退款 toast) ──────────────────
  group('Controller notifications stream', () {
    test('failed + refunded_credits>0 → emit refunded', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      // 先插一个 running task
      c.state = c.state.applyTask(_task('t1',
          status: TaskStatus.running, progress: 50));

      final received = <TaskNotification>[];
      final sub = c.notifications.listen(received.add);

      // wire 推 failed + refunded
      c.debugApplyWire({
        'task_id': 't1',
        'status': 'failed',
        'refunded_credits': 200,
        'error_message': 'upstream timeout',
      });

      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(received.length, 1);
      expect(received.first.kind, TaskNotificationKind.refunded);
      expect(received.first.credits, 200);
      expect(received.first.errorMessage, 'upstream timeout');
      await sub.cancel();
      await c.stop();
    });

    test('failed 无 refund → emit failed (errorMessage)', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('t2', status: TaskStatus.running));

      final received = <TaskNotification>[];
      final sub = c.notifications.listen(received.add);
      c.debugApplyWire({
        'task_id': 't2',
        'status': 'failed',
        'error_message': 'content_blocked',
      });
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(received.length, 1);
      expect(received.first.kind, TaskNotificationKind.failed);
      expect(received.first.errorMessage, 'content_blocked');
      await sub.cancel();
      await c.stop();
    });

    test('completed → emit completed', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('t3', status: TaskStatus.running));

      final received = <TaskNotification>[];
      final sub = c.notifications.listen(received.add);
      c.debugApplyWire({
        'task_id': 't3',
        'status': 'completed',
        'progress': 100,
      });
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(received.length, 1);
      expect(received.first.kind, TaskNotificationKind.completed);
      await sub.cancel();
      await c.stop();
    });

    test('still active (progress 更新) → 不 emit', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('t4', status: TaskStatus.running, progress: 30));

      final received = <TaskNotification>[];
      final sub = c.notifications.listen(received.add);
      c.debugApplyWire({
        'task_id': 't4',
        'status': 'running',
        'progress': 70,
      });
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(received, isEmpty);
      await sub.cancel();
      await c.stop();
    });
  });

  // ─── v2-6 desync 4009 ─────────────────────────────
  group('Controller v2-6 desync 兜底', () {
    test('debugHandleDesync 清 sse cursor + refetch active', () async {
      final db = AppDb.memory();
      addTearDown(() async => db.close());
      final cursors = SseCursorsDao(db);
      // 预置 cursor (模拟 v2-4 已写过)
      await cursors.save(TasksController.sseScope, 'OLD-CURSOR-99');
      expect(await cursors.load(TasksController.sseScope), 'OLD-CURSOR-99');

      final client = _FakeAigcClient();
      // refetch 应回 1 条 active task
      client.activeTasks = [_task('refetched', status: TaskStatus.running)];

      final c = TasksController(
        client: client,
        realtimeEndpoint: null,
        tokenProvider: () async => 'tok',
        userId: 'u1',
        sseCursors: cursors,
      );
      // 预置一个老 task — refetch 后应仍存在 (state 不强制清, 只补正)
      c.state = c.state.applyTask(_task('old1', status: TaskStatus.running));

      await c.debugHandleDesync(4009, 'last_event_id_beyond_retention');

      // cursor 已被清
      expect(await cursors.load(TasksController.sseScope), isNull);
      // refetch 拿到的新 task 入了 state
      expect(c.state.tasks.containsKey('refetched'), isTrue);
      // initialFetchDone 在 _refreshActive 里被翻成 true
      expect(c.state.initialFetchDone, isTrue);
      await c.stop();
    });

    test('debugHandleDesync — refetch 失败不 throw', () async {
      final db = AppDb.memory();
      addTearDown(() async => db.close());
      final cursors = SseCursorsDao(db);
      await cursors.save(TasksController.sseScope, 'X');

      final client = _FakeAigcClient();
      client.throwOnFetch = true;

      final c = TasksController(
        client: client,
        realtimeEndpoint: null,
        tokenProvider: () async => 'tok',
        userId: 'u1',
        sseCursors: cursors,
      );

      // 不 throw
      await c.debugHandleDesync(4009, 'gap');

      // cursor 仍清掉了 (clear 在 refetch 之前)
      expect(await cursors.load(TasksController.sseScope), isNull);
      // refetch 失败 → connection 切 offline
      expect(c.state.connection, ConnectionState.offline);
      await c.stop();
    });
  });

  // ─── v2-5 progress 节流 (200ms coalesce) ─────────────
  group('Controller v2-5 progress 节流', () {
    test('纯 progress 心跳: 200ms 内多次 update, state 推迟到窗口末尾合并', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('p1',
          status: TaskStatus.running, progress: 10));

      // 30ms 内连发 3 次纯 progress (无状态切换 / outputs / error)
      c.debugApplyWire({'task_id': 'p1', 'status': 'running', 'progress': 30});
      c.debugApplyWire({'task_id': 'p1', 'status': 'running', 'progress': 50});
      c.debugApplyWire({'task_id': 'p1', 'status': 'running', 'progress': 80});

      // 立即查 state — 还没到 200ms, 仍是初始 10
      expect(c.state.tasks['p1']!.progress, 10,
          reason: '节流窗口内 setState 不应触发');

      // 等过 200ms 节流窗口
      await Future<void>.delayed(const Duration(milliseconds: 250));

      // 应合并到最新 80
      expect(c.state.tasks['p1']!.progress, 80);
      await c.stop();
    });

    test('终态切换不走节流, 立即 flush + 清同 id 待合并', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('p2',
          status: TaskStatus.running, progress: 10));

      // 先丢一帧 progress 进 throttle buffer
      c.debugApplyWire({'task_id': 'p2', 'status': 'running', 'progress': 60});
      // 立刻发完成 — 应 immediate flush, 不应被 throttle 残留覆盖回 60
      c.debugApplyWire({
        'task_id': 'p2',
        'status': 'completed',
        'progress': 100,
      });

      // 立即 (无延时) state 应已更新到 completed/100
      expect(c.state.tasks['p2']!.status, TaskStatus.completed);
      expect(c.state.tasks['p2']!.progress, 100);

      // 再等 250ms 确认 throttle 不会回退状态
      await Future<void>.delayed(const Duration(milliseconds: 250));
      expect(c.state.tasks['p2']!.status, TaskStatus.completed);
      expect(c.state.tasks['p2']!.progress, 100);
      await c.stop();
    });

    test('outputs 出现时立即 flush', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('p3',
          status: TaskStatus.running, progress: 50));

      c.debugApplyWire({
        'task_id': 'p3',
        'status': 'running',
        'progress': 99,
        'outputs': [
          {
            'index': 0,
            'kind': 'image',
            'url': 'cas:abc',
            'mime_type': 'image/png',
          },
        ],
      });

      // 含 outputs → immediate 路径; 立即可见
      expect(c.state.tasks['p3']!.outputs, hasLength(1));
      expect(c.state.tasks['p3']!.progress, 99);
      await c.stop();
    });

    test('stop() 主动 flush 节流 buffer, 不丢最近 progress', () async {
      final client = _FakeAigcClient();
      final c = _newController(client);
      c.state = c.state.applyTask(_task('p4',
          status: TaskStatus.running, progress: 10));

      c.debugApplyWire({'task_id': 'p4', 'status': 'running', 'progress': 77});
      // 还没到 200ms 立即 stop
      await c.stop();

      expect(c.state.tasks['p4']!.progress, 77,
          reason: 'stop 路径应 flush, 不能丢 77');
    });
  });
}
