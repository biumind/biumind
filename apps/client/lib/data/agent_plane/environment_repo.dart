// EnvironmentRepo —— 客户端管理"我有哪些 worker environment"的视图。
//
// 行为：
//   - list() / online() / byId(id) → 同步从 cache 返回；首次访问触发后台 fetch
//   - refresh() → 强制重新拉一次 brain
//   - watch() → broadcast Stream<List<AgentEnvironment>>，UI 订阅做 reactive
//   - stale-while-revalidate：cache 数据立即返回，后台并发刷新，新结果再 emit
//
// 不持久化到 SQLite —— environment 列表来自 brain 权威，每次启动 fetch 一次
// 即可；offline 模式 UI 用 cached snapshot 渲染。
//
// 跟 EnvironmentRepo 关系最近的是 BiuDaemonManager（S6-3 桌面端 spawn 本机
// daemon）—— EnvironmentRepo 只看 brain 那侧，daemon 起没起得看
// IsOnline。本地探测 daemon 由 daemon_manager.dart 单独负责。

import 'dart:async';

import 'agent_plane_client.dart';
import 'environment.dart';

class EnvironmentRepo {
  final AgentPlaneClient client;

  /// 后台刷新间隔。0 = 不自动刷新（手动调 refresh / list 才拉）。默认
  /// 30s 跟 brain 端 janitor 标 offline 阈值（90s）成 3:1 给状态 UI 留点
  /// 缓冲不抖动。
  final Duration refreshInterval;

  EnvironmentRepo({
    required this.client,
    this.refreshInterval = const Duration(seconds: 30),
  });

  // ── State ────────────────────────────────────────────────

  List<AgentEnvironment> _cache = const [];
  DateTime? _lastFetched;
  Future<List<AgentEnvironment>>? _inFlight;
  Timer? _refreshTimer;
  final _ctrl = StreamController<List<AgentEnvironment>>.broadcast();

  bool _started = false;

  // ── Public ────────────────────────────────────────────────

  /// broadcast stream —— UI 通过 watch().listen 拿 reactive 更新。
  Stream<List<AgentEnvironment>> watch() => _ctrl.stream;

  /// 当前缓存的 snapshot。首次调用如果还没拉过 → 后台触发 fetch，立即
  /// 返空数组让 UI 先画 loading skeleton。后续刷新通过 watch 推。
  List<AgentEnvironment> list() {
    _kickFetchIfStale();
    return List.unmodifiable(_cache);
  }

  /// 仅在线的 worker —— UI 创 thread 时选 environment 用。
  List<AgentEnvironment> online() {
    return list().where((e) => e.isOnline).toList(growable: false);
  }

  /// 按 id 查；cache miss 返 null（不会单独发请求 —— 调用方应该已经
  /// list 过；id 来自 list 的结果）。
  AgentEnvironment? byId(String id) {
    for (final e in _cache) {
      if (e.environmentId == id) return e;
    }
    return null;
  }

  /// 主动刷新。返回最新列表；并发调用合并到同一 in-flight Future。
  Future<List<AgentEnvironment>> refresh() {
    final inFlight = _inFlight;
    if (inFlight != null) return inFlight;

    final fut = client.listEnvironments().then((envs) {
      _cache = envs;
      _lastFetched = DateTime.now();
      _inFlight = null;
      if (!_ctrl.isClosed) _ctrl.add(_cache);
      return envs;
    }, onError: (Object err, StackTrace stack) {
      _inFlight = null;
      // 不更新 cache —— 让 UI 继续用旧数据；err 由调用方决定怎么显示
      throw err;
    });
    _inFlight = fut;
    return fut;
  }

  /// 启动后台定时刷新。idempotent —— 调多次只起一个 timer。
  void start() {
    if (_started) return;
    _started = true;
    if (refreshInterval > Duration.zero) {
      _refreshTimer = Timer.periodic(refreshInterval, (_) {
        // 静默刷新；失败不冒泡（调用方 refresh() 才显式处理 err）
        refresh().catchError((_) => <AgentEnvironment>[]);
      });
    }
    _kickFetchIfStale();
  }

  /// 停定时器 + 关流。可以再调 start 重启。
  Future<void> dispose() async {
    _refreshTimer?.cancel();
    _refreshTimer = null;
    _started = false;
    if (!_ctrl.isClosed) await _ctrl.close();
  }

  // ── Internal ──────────────────────────────────────────────

  void _kickFetchIfStale() {
    if (_inFlight != null) return;
    final last = _lastFetched;
    final now = DateTime.now();
    final isStale = last == null ||
        (refreshInterval > Duration.zero && now.difference(last) > refreshInterval);
    if (!isStale) return;
    // fire-and-forget；refresh().catchError 防 unhandled。
    refresh().catchError((_) => <AgentEnvironment>[]);
  }
}
