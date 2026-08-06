// SidebarBadges — 设计 §10A.9 Badge 系统的客户端实现。
//
// 数据流:
//
//   sidebarLayoutProvider('desktop') 变化
//     ↓ 监听
//   BadgeController._reconcile()
//     ├─ 算出当前 pinned install_id set
//     ├─ 停掉已经不 pinned 的 install 的 timer
//     └─ 对新 pinned install:
//         读 manifestProvider(identifier) → manifest.sidebar.badge_action
//         有 action → 立即 tick 一次 + Timer.periodic(badge_refresh)
//
// 每次 tick 调 appsClient.invoke({action, input:{}}), 解析返回的
// {count, severity}, 写入 state map。UI (_NavRow) watch 这个 state
// 渲染对应 badge。
//
// 节流:
//   - badge_refresh 下限 60s (manifest validator 强制), 这里 clamp
//     到 [60, 3600] 防止意外 0 / 过长
//   - 仅 pinned + manifest 声明 badge_action 才启 timer
//   - app lifecycle paused 时停 timer (单独 task; 这里只起骨架)
//
// 静默失败: invoke 错误 (网络 / 权限 / Authz) 一律吞掉, 等下次 tick;
// UI 看到的还是上次成功的 badge 或没 badge, 不打扰用户。

import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api/apps_client.dart' show Installation;
import 'api/sidebar_client.dart';
import 'apps_providers.dart';
import 'sidebar_providers.dart';

/// 三档严重度 — 客户端按此选 badge 颜色 (info=蓝中性, warn=橙, error=红)。
enum BadgeSeverity { info, warn, error }

BadgeSeverity parseBadgeSeverity(String? s) => switch (s) {
      'warn' => BadgeSeverity.warn,
      'error' => BadgeSeverity.error,
      _ => BadgeSeverity.info,
    };

class BadgeData {
  final int count;
  final BadgeSeverity severity;
  final DateTime fetchedAt;

  const BadgeData({
    required this.count,
    required this.severity,
    required this.fetchedAt,
  });

  /// Badge 是否应该被渲染 — count<=0 视为"没有徽章"。
  bool get visible => count > 0;
}

class BadgeController extends Notifier<Map<String, BadgeData>>
    with WidgetsBindingObserver {
  final Map<String, Timer> _timers = {};
  /// 用 detached/inactive/paused 时挂起的 (installId, identifier, action,
  /// seconds) 信息, resumed 时拿来重启 timer + 立即 tick。
  final Map<String, _TimerSpec> _suspended = {};

  @override
  Map<String, BadgeData> build() {
    // 注册 lifecycle 观察 — paused/inactive 停 timer, resumed 重启;
    // 避免应用最小化 / 锁屏后还在轮询拉爆 invocations (设计 §10A.9
    // 节流)。
    WidgetsBinding.instance.addObserver(this);

    // listen layout changes — 任意 scope (这里只关心 desktop) 变化
    // 都 reconcile 一次。
    ref.listen<AsyncValue<SidebarLayout?>>(
      sidebarLayoutProvider('desktop'),
      (prev, next) {
        if (next.hasValue) unawaited(_reconcile());
      },
    );
    // installations 变了 (装 / 卸 / 重启) 也要 reconcile, 否则 manifest
    // 拿不到对的 identifier。
    ref.listen(installationsProvider('user'), (prev, next) {
      if (next.hasValue) unawaited(_reconcile());
    });
    ref.onDispose(() {
      WidgetsBinding.instance.removeObserver(this);
      for (final t in _timers.values) {
        t.cancel();
      }
      _timers.clear();
      _suspended.clear();
    });
    // 首次 reconcile (build 完后微任务跑)。
    Future.microtask(_reconcile);
    return const {};
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    switch (state) {
      case AppLifecycleState.paused:
      case AppLifecycleState.detached:
      case AppLifecycleState.hidden:
        _suspendAll();
      case AppLifecycleState.resumed:
        _resumeAll();
      case AppLifecycleState.inactive:
        // 短暂 inactive (e.g. 切 tab) 不动, 避免来回开关 timer 抖动。
        break;
    }
  }

  void _suspendAll() {
    for (final entry in _timers.entries) {
      entry.value.cancel();
    }
    // 保留 _suspended 中的 spec — _timers 清掉但 spec 留着等 resume。
    _timers.clear();
  }

  void _resumeAll() {
    if (_suspended.isEmpty) return;
    final specs = Map<String, _TimerSpec>.from(_suspended);
    for (final e in specs.entries) {
      _startTimer(e.key, e.value.identifier, e.value.action, e.value.seconds);
    }
  }

  Future<void> _reconcile() async {
    final layout = ref.read(sidebarLayoutProvider('desktop')).valueOrNull;
    final installs = ref.read(installationsProvider('user')).valueOrNull
        ?? const [];
    if (layout == null) return;

    final pinned = <String>{};
    for (final i in layout.items) {
      if (i.kind == 'app') pinned.add(i.ref);
    }

    // 停 timer + 清 state: 不再 pinned 的 install。
    final removed = <String>{};
    for (final id in _timers.keys) {
      if (!pinned.contains(id)) removed.add(id);
    }
    for (final id in _suspended.keys) {
      if (!pinned.contains(id)) removed.add(id);
    }
    for (final id in removed) {
      _timers.remove(id)?.cancel();
      _suspended.remove(id);
    }
    if (removed.isNotEmpty) {
      final next = {...state};
      for (final id in removed) {
        next.remove(id);
      }
      state = next;
    }

    // 启 timer: 新 pinned 且未启动的 install。
    for (final installId in pinned) {
      if (_timers.containsKey(installId)) continue;
      final install = _findInstall(installs, installId);
      if (install == null) continue;
      final manifest = await _loadManifest(install.identifier);
      if (manifest == null) continue;
      final sidebar = manifest['sidebar'];
      if (sidebar is! Map) continue;
      final action = sidebar['badge_action'];
      if (action is! String || action.isEmpty) continue;
      final refreshRaw = sidebar['badge_refresh'];
      final refresh = (refreshRaw is num ? refreshRaw.toInt() : 60).clamp(60, 3600);

      _startTimer(installId, install.identifier, action, refresh);
    }
  }

  Installation? _findInstall(List<Installation> rows, String id) {
    for (final r in rows) {
      if (r.id == id) return r;
    }
    return null;
  }

  Future<Map<String, dynamic>?> _loadManifest(String identifier) async {
    try {
      return await ref.read(manifestProvider(identifier).future);
    } catch (e) {
      debugPrint('BadgeController: manifest load failed for $identifier: $e');
      return null;
    }
  }

  void _startTimer(
      String installId, String identifier, String action, int seconds) {
    // 立即 tick 一次, 用户进 sidebar 不用等一个完整周期才看到 badge。
    Future.microtask(() => _tick(installId, identifier, action));
    _timers[installId] = Timer.periodic(
      Duration(seconds: seconds),
      (_) => _tick(installId, identifier, action),
    );
    // 记录 spec 让 lifecycle resume 能恢复。
    _suspended[installId] = _TimerSpec(
      identifier: identifier,
      action: action,
      seconds: seconds,
    );
  }

  Future<void> _tick(
      String installId, String identifier, String action) async {
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return;
    try {
      final resp = await client.invoke(
        identifier: identifier,
        action: action,
        token: token,
      );
      // 服务端把 App 返回值包在 'result' 里 (api.go:handleInvoke)。
      final result = resp['result'];
      if (result is! Map) return;
      final count = (result['count'] as num?)?.toInt() ?? 0;
      final sev = parseBadgeSeverity(result['severity'] as String?);
      state = {
        ...state,
        installId: BadgeData(
          count: count,
          severity: sev,
          fetchedAt: DateTime.now(),
        ),
      };
    } catch (e) {
      // 静默失败 — 401 / 403 / 网络抖动等。
      debugPrint('BadgeController: tick failed for $identifier: $e');
    }
  }

  /// 测试用: 强制立即重算一遍。生产路径靠 listen 自动触发。
  @visibleForTesting
  Future<void> reconcileNow() => _reconcile();
}

/// Timer 启动 spec — lifecycle resume 时按这个重建 timer。
class _TimerSpec {
  final String identifier;
  final String action;
  final int seconds;
  const _TimerSpec({
    required this.identifier,
    required this.action,
    required this.seconds,
  });
}

final badgeControllerProvider =
    NotifierProvider<BadgeController, Map<String, BadgeData>>(
  BadgeController.new,
);

/// 便捷 read — 给 _NavRow 用。返回 null 表示"无 badge", 调用方据此
/// 决定不渲染。
BadgeData? badgeFor(WidgetRef ref, String installId) {
  final m = ref.watch(badgeControllerProvider);
  final b = m[installId];
  if (b == null || !b.visible) return null;
  return b;
}
