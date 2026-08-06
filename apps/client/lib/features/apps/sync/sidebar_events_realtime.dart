// SidebarEventsRealtime — listens on `sidebar:user:<userId>` and keeps
// the sidebar layout reactive across this user's devices (v1.5#3).
//
// Server side: services/app_center publishes
// `biumind.sidebar.layout_changed` whenever the sidebar_layouts row
// changes (PUT layout, reset to defaults, ...). The outbox poller
// (services/app_center/internal/outbox/poller.go) routes
// scope=user:<id> → topic sidebar:user:<id>.
//
// Behaviour:
//
//   * Frame arrives → ref.invalidate(sidebarLayoutProvider(scope)) so
//     the customize page + the sidebar widget itself re-fetch.
//   * Concurrently: a one-shot SidebarChangeNotice is pushed onto the
//     notices stream so the customize page can show a toast
//     "另一设备已修改侧边栏，已重新载入".
//
// Mirrors features/skills/sync/skill_events_realtime.dart in shape
// and lifecycle (started in AppShell once creds exist, stopped on
// dispose / credential rotation).

import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../../../data/sidebar_providers.dart';
import '../../../data/sse/realtime_hub.dart';
import '../../../services/auth_service.dart';

final _log = Logger('biumind.sidebar.realtime');

/// One observed remote sidebar mutation. Surfaced for toast UI; the
/// underlying provider invalidation runs synchronously in the listener.
class SidebarChangeNotice {
  /// e.g. biumind.sidebar.layout_changed
  final String kind;
  final String scope; // 'desktop' | 'mobile'
  final int version;
  final String? device; // diagnostic — "MacBook-Air"

  const SidebarChangeNotice({
    required this.kind,
    required this.scope,
    required this.version,
    this.device,
  });
}

class SidebarEventsListener {
  SidebarEventsListener(this._ref);
  final Ref _ref;

  RealtimeHub? _hub;
  StreamSubscription<RealtimeFrame>? _sub;
  String? _topic;
  final StreamController<SidebarChangeNotice> _ctrl =
      StreamController<SidebarChangeNotice>.broadcast();

  Stream<SidebarChangeNotice> get notices => _ctrl.stream;

  void start() {
    if (_topic != null) return;
    final creds = _ref.read(hubCredentialsProvider);
    if (creds == null) {
      _log.fine('no creds; skipping sidebar events listener');
      return;
    }
    final userId = _decodeUserId(creds.bearerToken);
    if (userId == null) {
      _log.warning('JWT missing sub; sidebar events listener disabled');
      return;
    }
    _topic = 'sidebar:user:$userId';

    _hub = RealtimeHub(RealtimeHubConfig(
      endpoint: creds.endpoint.replace(path: '/v1/realtime/stream'),
      auth: () async {
        final c = _ref.read(hubCredentialsProvider);
        return c?.bearerToken ?? '';
      },
    ));

    _sub = _hub!.subscribe(_topic!).listen(
          _onFrame,
          onError: (Object e) =>
              _log.warning('sidebar events stream error: $e'),
        );
    _log.info('sidebar events listener subscribed to $_topic');

    // 重连 / 启动时尝试 flush 离线编辑 (设计 §10A.12)。
    // 不 await — 后台跑, 失败时 outbox 保留等下一次 start。
    unawaited(_flushOutboxes());
  }

  /// 把所有 scope 的 pending edit 推回服务端。当前只有 desktop, 留个
  /// for-loop 给 mobile 解锁后扩。
  Future<void> _flushOutboxes() async {
    for (final scope in const ['desktop']) {
      try {
        final flushed = await flushSidebarOutbox(_ref, scope: scope);
        if (flushed) _log.info('sidebar outbox flushed: $scope');
      } catch (e) {
        _log.warning('sidebar outbox flush failed ($scope): $e');
      }
    }
  }

  Future<void> stop() async {
    await _sub?.cancel();
    _sub = null;
    await _hub?.dispose();
    _hub = null;
    _topic = null;
  }

  void _onFrame(RealtimeFrame frame) {
    final kind = frame.kind;
    if (kind != 'biumind.sidebar.layout_changed') {
      // Forward-compat: ignore unknown kinds rather than crashing.
      return;
    }
    final p = frame.payload;
    // The poller wraps payload as {event_id, event_type, data: {...}}.
    final inner = (p['data'] as Map?)?.cast<String, dynamic>() ?? p;
    final scope = inner['scope']?.toString() ?? 'desktop';
    final version = (inner['version'] as num?)?.toInt() ?? 0;
    final device = inner['device']?.toString();

    // Refresh the affected scope. Cheap; avoids per-field merge.
    _ref.invalidate(sidebarLayoutProvider(scope));

    if (!_ctrl.isClosed) {
      _ctrl.add(SidebarChangeNotice(
        kind: kind,
        scope: scope,
        version: version,
        device: device,
      ));
    }
  }

  String? _decodeUserId(String jwt) {
    try {
      final parts = jwt.split('.');
      if (parts.length != 3) return null;
      var payload = parts[1];
      while (payload.length % 4 != 0) {
        payload += '=';
      }
      final json = utf8.decode(base64Url.decode(payload));
      final m = jsonDecode(json) as Map<String, dynamic>;
      final v = m['sub'];
      if (v is String && v.isNotEmpty) return v;
      return null;
    } catch (_) {
      return null;
    }
  }
}

final sidebarEventsListenerProvider =
    Provider<SidebarEventsListener>((ref) {
  final l = SidebarEventsListener(ref);
  ref.listen<HubCredentials?>(hubCredentialsProvider, (prev, next) {
    if (prev?.bearerToken != next?.bearerToken) {
      l.stop().then((_) => l.start());
    }
  });
  ref.onDispose(() => l.stop());
  return l;
});

/// Toast-stream provider — watch with `ref.listen` to surface
/// "另一设备已修改侧边栏" SnackBars without rebuild churn.
final sidebarChangeNoticesProvider =
    StreamProvider<SidebarChangeNotice>((ref) {
  final listener = ref.watch(sidebarEventsListenerProvider);
  return listener.notices;
});
