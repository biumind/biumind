// TasksController — 全局单例 (keepAlive) AIGC 任务进度追踪.
//
// 详细设计: docs/BiuMind-AIGC-Client-Progress-Design.md
//
// 主通道: Realtime SSE (订阅 topic=aigc.user.{userId}.tasks).
//        services/aigc orchestrator 转发 worker 的 aigc.task.update 事件
//        到 services/realtime, 客户端经此 topic 收到所有任务进度.
//
// 兜底:   主通道连续 3 次断 (含初始连接失败) 后启用 30s 短轮询; 主通道恢复
//        后停止. App 进入后台时暂停 SSE; 回前台先 fetchMyTasks 一次补漏再 reconnect.
//
// 持久化 / 节流 / last_event_id 续传 P4b-5 阶段加. MVP 先把"在线场景下进度
// 实时收到 + 离线兜底拉一次"跑通.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../../../data/sse/realtime_hub.dart';
import '../../../data/sse/sse_cursors_dao.dart';
import '../../../services/auth_service.dart';
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../../chat/data/chat_scope.dart' show accountIdFromEndpoint;
import '../data/aigc_client.dart';
import '../data/aigc_tasks_dao.dart';
import '../domain/creation_task.dart';
import 'aigc_providers.dart';

final _log = Logger('biumind.creation.tasks');

// ─── State ────────────────────────────────────────────

class TasksState {
  /// id → task. 同时含 active 和最近完成的若干 (UI 列表显示用).
  final Map<String, CreationTask> tasks;

  /// 主通道当前是否健康 (SSE 在线 / 兜底轮询启用 / 完全断网).
  final ConnectionState connection;

  /// 是否已完成首次 active 拉取 (start → _refreshActive). UI 用来区分
  /// "首次加载中" vs "真的没作品" 两个空态.
  final bool initialFetchDone;

  const TasksState({
    this.tasks = const {},
    this.connection = ConnectionState.idle,
    this.initialFetchDone = false,
  });

  /// 仅 active (pending/queued/running/submitting) 的 id 列表, 按 createdAt desc.
  List<String> get activeIds {
    final list = tasks.values.where((t) => t.status.isActive).toList()
      ..sort((a, b) => b.createdAt.compareTo(a.createdAt));
    return list.map((t) => t.id).toList();
  }

  /// 全部任务按 createdAt desc; UI 瀑布流默认顺序.
  List<CreationTask> sortedDesc() {
    final list = tasks.values.toList()
      ..sort((a, b) => b.createdAt.compareTo(a.createdAt));
    return list;
  }

  TasksState copyWith({
    Map<String, CreationTask>? tasks,
    ConnectionState? connection,
    bool? initialFetchDone,
  }) =>
      TasksState(
        tasks: tasks ?? this.tasks,
        connection: connection ?? this.connection,
        initialFetchDone: initialFetchDone ?? this.initialFetchDone,
      );

  /// merge 一条更新; 不存在时插入, 存在时按状态机推进.
  TasksState applyTask(CreationTask t) {
    final cur = tasks[t.id];
    final next = Map<String, CreationTask>.from(tasks);
    if (cur == null) {
      next[t.id] = t;
    } else {
      next[t.id] = cur.merge(
        status: t.status,
        progress: t.progress,
        outputs: t.outputs.isNotEmpty ? t.outputs : null,
        errorCode: t.errorCode,
        errorMessage: t.errorMessage,
        refundedCredits: t.refundedCredits,
        queuedAt: t.queuedAt,
        startedAt: t.startedAt,
        completedAt: t.completedAt,
        updatedAt: t.updatedAt,
      );
    }
    return copyWith(tasks: next);
  }

  TasksState replaceLocalId(String tempId, CreationTask realTask) {
    final next = Map<String, CreationTask>.from(tasks);
    next.remove(tempId);
    next[realTask.id] = realTask.copyWith(localTempId: tempId);
    return copyWith(tasks: next);
  }

  TasksState removeTask(String id) {
    if (!tasks.containsKey(id)) return this;
    final next = Map<String, CreationTask>.from(tasks)..remove(id);
    return copyWith(tasks: next);
  }
}

enum ConnectionState {
  idle, // 还没开始 (未登录或未 start)
  sseLive, // SSE 健康
  pollingFallback, // SSE 断, 30s 轮询兜底
  offline, // 全断
}

// ─── Controller ────────────────────────────────────────

/// TaskNotificationKind — controller 主动暴露的"业务事件"种类.
/// UI 层 (ProviderScope 顶层) 订阅 `notifications` 流后弹 SnackBar.
///
/// 单纯的 SSE 进度更新走 state, 但"积分退还""任务失败原因"这类需要
/// 用户感知 + 一次性提示的, 走这个事件流避免在 controller 里直接持
/// 有 BuildContext.
enum TaskNotificationKind { refunded, failed, completed }

class TaskNotification {
  final String taskId;
  final TaskNotificationKind kind;
  final int credits; // refunded > 0 时填
  final String? errorMessage;

  const TaskNotification({
    required this.taskId,
    required this.kind,
    this.credits = 0,
    this.errorMessage,
  });
}

class TasksController extends StateNotifier<TasksState> {
  TasksController({
    required this.client,
    required this.realtimeEndpoint,
    required this.tokenProvider,
    required this.userId,
    this.dao,
    this.sseCursors,
    this.ownerKey,
    Duration pollInterval = const Duration(seconds: 30),
  })  : _pollInterval = pollInterval,
        super(const TasksState());

  /// SSE cursor topic 段 — RealtimeHub 多实例共用 sse_cursors 表, 用 scope
  /// 区分. AIGC tasks 用此值; 改名会丢续传 cursor.
  static const sseScope = 'aigc.tasks';

  final AigcClient client;
  final Uri? realtimeEndpoint;
  final Future<String> Function() tokenProvider;
  final String userId;
  /// v2-1: 创作任务本地持久化 DAO. nil 时跳过 (单测 / 无 drift 场景).
  final AigcTasksDao? dao;
  /// v2-4: SSE last-event-id 续传 DAO. nil 时跳过.
  final SseCursorsDao? sseCursors;
  /// P2 多账号: 当前登录态的 ownerKey (= Drift 数据隔离键)。非 null 时
  /// cursor scope = 'ownerKey:aigc.tasks', 切账号互不污染; null (测试 /
  /// 占位 controller) 时退化为裸 topic。
  final String? ownerKey;
  final Duration _pollInterval;

  /// notifications — 业务事件流 (任务终态切换 + 积分退还).
  /// UI 用 `ref.listen` 在顶层 listen 弹 SnackBar.
  final _notificationsCtrl = StreamController<TaskNotification>.broadcast();
  Stream<TaskNotification> get notifications => _notificationsCtrl.stream;

  RealtimeHub? _hub;
  StreamSubscription<RealtimeFrame>? _sub;
  Timer? _pollTimer;
  int _sseFailCount = 0;
  bool _started = false;

  /// v2-5 progress 节流 — 纯进度更新 (status 不变, 无 outputs / error) 200ms 内
  /// 合并 flush, 避免 100 帧/s 的 setState 风暴. 关键状态切换 (终态 / outputs /
  /// error) 仍走 immediate path.
  static const _throttleWindow = Duration(milliseconds: 200);
  Timer? _throttleTimer;
  /// task_id → 待合并的 merged task (同 id 多次写入, last-write-wins).
  final Map<String, CreationTask> _pendingThrottle = {};

  String get _topic => 'aigc.user.$userId.tasks';

  /// 完整 cursor scope: 有 ownerKey 时 'ownerKey:aigc.tasks' (P2 多账号
  /// 隔离), 否则裸 topic (测试 / 占位)。
  String get _cursorScope {
    final k = ownerKey;
    return k == null || k.isEmpty ? sseScope : '$k:$sseScope';
  }

  /// 启动: 先从本地 (dao) 秒回最近 N 条 → 再拉 active → 起 SSE.
  /// 失败时退化到轮询.
  Future<void> start() async {
    if (_started) return;
    _started = true;
    await _hydrateFromLocal();
    await _refreshActive();
    _connectSse();
  }

  /// _hydrateFromLocal — drift 拿最近 100 条本地 task, 填进 state.
  /// 本地数据可能过时 (后台运行的任务 SSE 错过), _refreshActive 之后会校正.
  Future<void> _hydrateFromLocal() async {
    if (dao == null) return;
    try {
      final local = await dao!.loadByUser(userId, limit: 100);
      if (local.isEmpty) return;
      var next = state;
      for (final t in local) {
        next = next.applyTask(t);
      }
      state = next;
    } catch (e) {
      _log.warning('hydrate from local dao failed: $e');
    }
  }

  /// _persist — fire-and-forget 写 dao. 失败只 log, 不阻塞 UI.
  void _persist(CreationTask t) {
    if (dao == null) return;
    Future<void>.microtask(() async {
      try {
        await dao!.upsert(t);
      } catch (e) {
        _log.warning('persist task ${t.id}: $e');
      }
    });
  }

  /// 停止 (logout / dispose). 取消订阅 + 停轮询.
  ///
  /// stop 由 ref.onDispose(controller.stop) 触发,常见 race: provider dispose
  /// 时 StateNotifier 自己也立刻 dispose,但 `_sub.cancel()`/`_hub.dispose()`
  /// 是 await 异步,等回到这里时 StateNotifier.mounted=false,直接给 state
  /// 赋值会抛 "Tried to use TasksController after dispose was called"。
  /// 加 mounted 守门即可:dispose 路径下我们其实不在乎最终 state,只想清流。
  Future<void> stop() async {
    _started = false;
    // 主动 flush 节流 buffer, 避免 stop → 立刻 dispose 路径丢最近 200ms 的 progress.
    _throttleTimer?.cancel();
    _throttleTimer = null;
    if (_pendingThrottle.isNotEmpty) {
      _flushThrottled();
    }
    await _sub?.cancel();
    _sub = null;
    await _hub?.dispose();
    _hub = null;
    _pollTimer?.cancel();
    _pollTimer = null;
    if (mounted) {
      state = state.copyWith(connection: ConnectionState.idle);
    }
  }

  @override
  void dispose() {
    _throttleTimer?.cancel();
    _throttleTimer = null;
    _pendingThrottle.clear();
    _notificationsCtrl.close();
    super.dispose();
  }

  // ─── 主通道: SSE ─────────────────────────────────

  void _connectSse() {
    if (realtimeEndpoint == null) {
      _log.warning('realtime endpoint not configured — falling back to poll');
      _startPolling();
      return;
    }
    _hub?.dispose();
    _hub = RealtimeHub(
      RealtimeHubConfig(
        endpoint: realtimeEndpoint!,
        auth: tokenProvider,
      ),
      loadLastEventId: sseCursors == null
          ? null
          : () => sseCursors!.load(_cursorScope),
      saveLastEventId: sseCursors == null
          ? null
          : (id) => sseCursors!.save(_cursorScope, id),
      onDesync: _handleDesync,
    );
    _sub = _hub!.subscribe(_topic).listen(
          _onFrame,
          onError: _onSseError,
          onDone: _onSseError,
        );
    state = state.copyWith(connection: ConnectionState.sseLive);
    _sseFailCount = 0;
    _stopPolling();
    _log.fine('TasksController SSE connected topic=$_topic');
  }

  /// v2-6 desync 4009 兜底 — 服务端判定 client 的 last-event-id 已超出
  /// ledger retention (典型: 后台 1h+ 没连, ring 已 evict). hub 已清内存
  /// cursor; 这里清 dao 持久化游标 + 全量 refetch active 任务, 让 state 跟
  /// 服务端真实状态对齐. 失败只 log, 不 throw.
  @visibleForTesting
  Future<void> debugHandleDesync(int code, String reason) =>
      _handleDesync(code, reason);

  Future<void> _handleDesync(int code, String reason) async {
    _log.warning('TasksController desync code=$code reason=$reason — '
        'clearing cursor + full refetch');
    if (sseCursors != null) {
      try {
        await sseCursors!.clear(_cursorScope);
      } catch (e) {
        _log.warning('clear sse cursor on desync: $e');
      }
    }
    // 拉一次 active, 让 in-memory state 跟服务端对齐. 不阻塞 hub.
    try {
      await _refreshActive();
    } catch (e) {
      _log.warning('refetch on desync failed: $e');
    }
  }

  void _onSseError([Object? error]) {
    _sseFailCount++;
    _log.warning('SSE error count=$_sseFailCount: $error');
    if (_sseFailCount < 3) {
      // 指数退避 1s/2s/4s 重连 (RealtimeHub 自己也会 retry, 这里做最外层兜底)
      Timer(Duration(seconds: 1 << _sseFailCount), () {
        if (_started) _connectSse();
      });
    } else {
      // 三次失败 → 启用兜底轮询
      _startPolling();
      state = state.copyWith(connection: ConnectionState.pollingFallback);
    }
  }

  void _onFrame(RealtimeFrame f) {
    // wire format (services/aigc/internal/orchestrator.fanout):
    //   {topic, kind, payload: {task_id, status, progress, outputs, ...}}
    if (f.kind != 'aigc.task.update') return;
    final p = f.payload;
    _applyUpdateFromWire(p);
  }

  // ─── 兜底: 30s 轮询 ─────────────────────────────

  void _startPolling() {
    if (_pollTimer != null) return;
    _pollTimer = Timer.periodic(_pollInterval, (_) async {
      await _refreshActive();
      // 顺便重试 SSE
      if (_started) _connectSse();
    });
    _log.info('TasksController polling fallback started @ ${_pollInterval.inSeconds}s');
  }

  void _stopPolling() {
    _pollTimer?.cancel();
    _pollTimer = null;
  }

  Future<void> _refreshActive() async {
    try {
      final tasks = await client.fetchMyTasks(statuses: const [
        TaskStatus.pending,
        TaskStatus.queued,
        TaskStatus.running,
      ]);
      var next = state;
      for (final t in tasks) {
        next = next.applyTask(t);
      }
      state = next.copyWith(initialFetchDone: true);
      // 批量持久化拉到的 active 任务, 重启秒回时数据更新.
      if (dao != null && tasks.isNotEmpty) {
        Future<void>.microtask(() async {
          try {
            await dao!.upsertAll(tasks);
          } catch (e) {
            _log.warning('persist active batch: $e');
          }
        });
      }
    } catch (e) {
      _log.warning('fetchMyTasks failed: $e');
      // 即使失败也标记 initialFetchDone, 避免 UI 永远转圈; offline banner 已提示用户.
      state = state.copyWith(
        connection: ConnectionState.offline,
        initialFetchDone: true,
      );
    }
  }

  // ─── 应用更新 ─────────────────────────────────────

  /// debugApplyWire — 仅供测试暴露 _applyUpdateFromWire, 走完整通知路径.
  @visibleForTesting
  void debugApplyWire(Map<String, dynamic> p) => _applyUpdateFromWire(p);

  void _applyUpdateFromWire(Map<String, dynamic> p) {
    final id = p['task_id'] as String?;
    if (id == null || id.isEmpty) return;

    // 当前 task 不存在: 拉一次完整详情 (wire payload 没含 user_id / model_code 等)
    final cur = state.tasks[id];
    if (cur == null) {
      // 异步补全 (不阻塞 frame 处理)
      Future.microtask(() async {
        try {
          final full = await client.getTask(id);
          state = state.applyTask(full);
          _persist(full);
        } catch (e) {
          _log.warning('getTask($id) on first frame failed: $e');
        }
      });
      return;
    }

    final outsRaw = p['outputs'];
    final outputs = outsRaw is List
        ? outsRaw
            .whereType<Map<String, dynamic>>()
            .map(TaskOutput.fromJson)
            .toList()
        : <TaskOutput>[];

    final nextStatus = TaskStatus.fromWire(p['status'] as String?);
    final errorCode = p['error_code'] as String?;
    final merged = cur.merge(
      status: nextStatus,
      progress: (p['progress'] as num?)?.toInt(),
      outputs: outputs.isNotEmpty ? outputs : null,
      errorCode: errorCode,
      errorMessage: p['error_message'] as String?,
      refundedCredits: (p['refunded_credits'] as num?)?.toInt(),
      updatedAt: _parseDate(p['updated_at']),
    );

    // 分流:
    // - 状态切换 / 含 outputs / 出错 → immediate flush (用户感知关键)
    // - 纯 progress 心跳 → coalesce 到 _pendingThrottle, 200ms 内合并写一次
    final isStateChange = nextStatus != cur.status;
    final hasOutputs = outputs.isNotEmpty;
    final hasError = errorCode != null && errorCode.isNotEmpty;
    if (isStateChange || hasOutputs || hasError) {
      _flushImmediate(cur, merged);
    } else {
      _pendingThrottle[id] = merged;
      _scheduleThrottleFlush();
    }
  }

  void _flushImmediate(CreationTask prev, CreationTask next) {
    // 写关键帧前先 flush 同 task 的待合并 progress, 防止
    // "进度 80% (节流) → 完成 (immediate)" 状态短暂回退到 80%.
    _pendingThrottle.remove(next.id);
    state = state.copyWith(tasks: {...state.tasks, next.id: next});
    _persist(next);
    _maybeEmitNotification(prev, next);
  }

  void _scheduleThrottleFlush() {
    if (_throttleTimer != null && _throttleTimer!.isActive) return;
    _throttleTimer = Timer(_throttleWindow, _flushThrottled);
  }

  void _flushThrottled() {
    _throttleTimer = null;
    if (_pendingThrottle.isEmpty) return;
    if (!mounted) {
      _pendingThrottle.clear();
      return;
    }
    final pending = Map<String, CreationTask>.from(_pendingThrottle);
    _pendingThrottle.clear();
    final next = Map<String, CreationTask>.from(state.tasks);
    next.addAll(pending);
    state = state.copyWith(tasks: next);
    // 批量 persist (microtask), 不阻塞当前 frame.
    if (dao != null) {
      Future<void>.microtask(() async {
        try {
          await dao!.upsertAll(pending.values.toList());
        } catch (e) {
          _log.warning('persist throttled batch: $e');
        }
      });
    }
  }

  /// _maybeEmitNotification — 检测状态终态切换, 发 SnackBar 事件.
  ///
  /// 只在 prev.status != next.status 且 next 是终态时 emit, 同终态多次更新
  /// 不重复弹 (e.g. running progress 99% → 100% 不算终态切换).
  void _maybeEmitNotification(CreationTask prev, CreationTask next) {
    if (prev.status == next.status) return;
    if (next.status.isActive) return; // 仍在跑

    final id = next.id;
    if (next.status == TaskStatus.completed) {
      _notificationsCtrl.add(TaskNotification(
        taskId: id, kind: TaskNotificationKind.completed,
      ));
      return;
    }
    // failed / cancelled / blocked 等终态
    final refundDelta =
        (next.refundedCredits) - (prev.refundedCredits);
    if (refundDelta > 0) {
      _notificationsCtrl.add(TaskNotification(
        taskId: id, kind: TaskNotificationKind.refunded,
        credits: refundDelta,
        errorMessage: next.errorMessage,
      ));
    } else {
      _notificationsCtrl.add(TaskNotification(
        taskId: id, kind: TaskNotificationKind.failed,
        errorMessage: next.errorMessage,
      ));
    }
  }

  // ─── 提交 / 取消 (UI 调用) ─────────────────────

  /// 提交生成. 乐观更新: 立即插一个 submitting 占位卡片, AigcClient.submit 返回
  /// 后替换为真 task. 失败时移除占位.
  Future<CreationTask> submit({
    required String type,
    required String modelCode,
    required String prompt,
    required Map<String, dynamic> params,
    String? negativePrompt,
    bool isPublic = false,
    String? parentSha,
    String? lineageOp,
    String? idempotencyKey,
  }) async {
    final tempId = 'local-${DateTime.now().microsecondsSinceEpoch}';
    final placeholder = CreationTask.localSubmitting(
      tempId: tempId,
      userId: userId,
      type: type,
      modelCode: modelCode,
      prompt: prompt,
      params: params,
    );
    state = state.applyTask(placeholder);
    _persist(placeholder);

    try {
      final res = await client.submit(
        type: type,
        modelCode: modelCode,
        prompt: prompt,
        negativePrompt: negativePrompt,
        params: params,
        isPublic: isPublic,
        parentSha: parentSha,
        lineageOp: lineageOp,
        idempotencyKey: idempotencyKey ?? tempId,
      );
      state = state.replaceLocalId(tempId, res.task);
      // 占位 → 真 id, 单事务交换 dao 行
      if (dao != null) {
        Future<void>.microtask(() async {
          try {
            await dao!.renameLocalId(
              tempId: tempId,
              realTask: res.task.copyWith(localTempId: tempId),
            );
          } catch (e) {
            _log.warning('renameLocalId persist: $e');
          }
        });
      }
      return res.task;
    } catch (e) {
      // 移除占位, 把异常抛给上层 (UI 显示 toast / 错误 banner)
      state = state.removeTask(tempId);
      if (dao != null) {
        Future<void>.microtask(() => dao!.deleteById(tempId));
      }
      rethrow;
    }
  }

  /// 用户主动取消. 服务端会推 status=cancelled 进来; 这里不本地变更.
  Future<void> cancel(String id) => client.cancelTask(id);

  /// 删除. 本地立即移除; 服务端软删 30d 后 GC.
  Future<void> delete(String id) async {
    await client.deleteTask(id);
    state = state.removeTask(id);
    if (dao != null) {
      Future<void>.microtask(() => dao!.deleteById(id));
    }
  }

  /// 设可见性 (PATCH).
  Future<void> setVisibility(String id, bool isPublic) async {
    await client.setVisibility(id, isPublic: isPublic);
    final cur = state.tasks[id];
    if (cur == null) return;
    final next = Map<String, CreationTask>.from(state.tasks);
    next[id] = CreationTask.fromJson({
      ...cur.toJson(),
      'is_public': isPublic,
    });
    state = state.copyWith(tasks: next);
  }
}

DateTime? _parseDate(dynamic v) {
  if (v is String && v.isNotEmpty) {
    try {
      return DateTime.parse(v);
    } catch (_) {
      return null;
    }
  }
  return null;
}

// ─── Riverpod wiring ──────────────────────────────────

/// keepAlive: 跨页面常驻 (用户从「创作」切到 chat 再切回, 任务进度不丢).
final tasksControllerProvider =
    StateNotifierProvider<TasksController, TasksState>((ref) {
  // select 稳定切片: token 轮换不重建 StateNotifier (否则掉 SSE + 内存任务态).
  // key = endpoint + userId (sub claim), 跨轮换稳定; P2 多账号: 换人
  // (switchAccount) 时 key 变化 → 重建 (旧 controller stop → 新 SSE topic /
  // 新 cursor scope), 否则旧账号的 aigc.user.<uid>.tasks 订阅会漏给新账号。
  // token 走 tokenProvider + aigcClient bearerProvider 现读.
  ref.watch(aigcClientProvider.select((c) => c?.baseUrl));
  final credsKey = ref.watch(hubCredentialsProvider.select((c) {
    if (c == null) return null;
    return '${c.endpoint}|${_decodeJwtSub(c.bearerToken) ?? ''}';
  }));
  final client = ref.read(aigcClientProvider);
  final creds = ref.read(hubCredentialsProvider);
  if (client == null || creds == null || credsKey == null) {
    // 未登录 → 占位 controller (start 不会真起 SSE)
    return TasksController(
      client: AigcClient(
          baseUrl: Uri.parse('http://localhost'),
          bearerProvider: () => null),
      realtimeEndpoint: null,
      tokenProvider: () async => '',
      userId: '',
    );
  }
  // 解 user_id (sub claim from JWT)
  final userId = _decodeJwtSub(creds.bearerToken) ?? '';
  // P2 多账号: cursor scope 前缀 (= Drift 数据隔离键)。解不出 (token 非
  // JWT) 时不传 sseCursors —— 不落无账号归属的 cursor, 也不阻塞 SSE。
  final ownerKey = accountIdFromEndpoint(creds.endpoint, creds.bearerToken);
  // realtime endpoint = identity 同源 :7008 (与 code_tasks_realtime 同套)
  // 但 dev 通常 7001 (model-relay); 这里复用 creds.endpoint 作 base.
  final controller = TasksController(
    client: client,
    realtimeEndpoint: creds.endpoint.replace(path: '/v1/realtime/stream'),
    tokenProvider: () async {
      final c = ref.read(hubCredentialsProvider);
      return c?.bearerToken ?? '';
    },
    userId: userId,
    dao: AigcTasksDao(ref.read(appDbProvider)),
    sseCursors:
        ownerKey == null ? null : SseCursorsDao(ref.read(appDbProvider)),
    ownerKey: ownerKey,
  );
  // 自动 start; 释放时 stop.
  controller.start();
  ref.onDispose(controller.stop);
  return controller;
});

String? _decodeJwtSub(String jwt) {
  try {
    final parts = jwt.split('.');
    if (parts.length != 3) return null;
    var payload = parts[1];
    while (payload.length % 4 != 0) {
      payload += '=';
    }
    final json = utf8.decode(base64Url.decode(payload));
    final m = jsonDecode(json) as Map<String, dynamic>;
    return m['sub']?.toString();
  } catch (_) {
    return null;
  }
}
