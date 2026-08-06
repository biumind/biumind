// RealtimeHub: single long-lived SSE connection to /v1/realtime/stream,
// multiplexes events by topic per invariant I8 (one SSE per device).
//
// Wire format (matches services/realtime/internal/api/sse.go):
//
//   id: <ulid>
//   event: message
//   data: {"topic":"<topic>","kind":"<kind>","payload":{...}}
//
// Heartbeat lines (`: heartbeat <ts>`) are dropped silently.
// Reconnect uses exponential backoff with `Last-Event-ID` resume.

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:logging/logging.dart';
import 'package:meta/meta.dart';

/// One frame received from Realtime. Owners call `data.cast()` to extract
/// strongly-typed payloads (typically [AgUiEvent.parseEnvelope] for AG-UI
/// streams).
class RealtimeFrame {
  final String id;     // SSE id (ULID-ish, monotonic)
  final String topic;  // matches subscribed topic
  final String kind;   // event kind (e.g. AG-UI type, or biumind.* for CUSTOM)
  final Map<String, dynamic> payload;
  final String? traceId;

  const RealtimeFrame({
    required this.id,
    required this.topic,
    required this.kind,
    required this.payload,
    this.traceId,
  });
}

typedef AuthTokenProvider = Future<String> Function();

class RealtimeHubConfig {
  final Uri endpoint;            // e.g. https://api.biu.app/v1/realtime/stream
  final AuthTokenProvider auth;  // returns current bearer token (refreshable)
  final Duration initialBackoff;
  final Duration maxBackoff;
  final Duration heartbeatTimeout;
  final String? deviceId;        // stable id for re-register

  const RealtimeHubConfig({
    required this.endpoint,
    required this.auth,
    this.initialBackoff = const Duration(milliseconds: 500),
    this.maxBackoff = const Duration(seconds: 30),
    this.heartbeatTimeout = const Duration(minutes: 2),
    this.deviceId,
  });
}

class RealtimeHub {
  /// [clientFactory] — 测试可注入 MockClient (返 SSE chunked stream).
  /// 生产传 nil 走默认 http.Client.new.
  ///
  /// [loadLastEventId] / [saveLastEventId] — v2-4 重启续传钩子. 首次
  /// _connect 前 await load 注入 cursor, 收 frame 后 100ms debounce 写回.
  /// 任一为空则跳过持久化 (内存 only). 失败不影响 hub 工作.
  RealtimeHub(
    this._config, {
    http.Client Function()? clientFactory,
    Future<String?> Function()? loadLastEventId,
    Future<void> Function(String eventId)? saveLastEventId,
    Future<void> Function(int code, String reason)? onDesync,
  })  : _clientFactory = clientFactory ?? http.Client.new,
        _loadLastEventId = loadLastEventId,
        _saveLastEventId = saveLastEventId,
        _onDesync = onDesync,
        _log = Logger('biumind.realtime');

  final RealtimeHubConfig _config;
  final http.Client Function() _clientFactory;
  final Future<String?> Function()? _loadLastEventId;
  final Future<void> Function(String)? _saveLastEventId;
  /// v2-6 desync 4009 钩子. 收到 system kind=desync 帧 (服务端判定 client
  /// 的 last-event-id 已超出 ledger retention) 时调用. 实现方应清 cursor +
  /// 全量 refetch state. 失败只 log, 不影响 hub 后续工作.
  final Future<void> Function(int code, String reason)? _onDesync;
  final Logger _log;

  final Set<String> _topics = {};
  final Map<String, StreamController<RealtimeFrame>> _topicCtrls = {};
  http.Client? _client;
  StreamSubscription<List<int>>? _sub;
  String? _lastEventId;
  bool _hydrated = false; // load() 是否跑过, 避免重连重复 load 覆盖最新 _lastEventId
  Timer? _saveDebounce;
  String? _pendingSaveId;
  bool _disposed = false;
  Duration _backoff = const Duration(milliseconds: 500);
  Timer? _reconnectTimer;
  Timer? _heartbeatWatchdog;

  /// debounce 间隔 — 高频 progress 事件下避免每帧打 sqlite. 10 帧/s 时
  /// 合并到 ~10/s 写盘, sqlite WAL 完全够用; 如果中途崩溃最多丢 100ms
  /// 内的最新 cursor (下次重连还能从更老的 id replay 拿到).
  static const _saveDebounceWindow = Duration(milliseconds: 100);

  /// Subscribe to a topic. Returned stream is broadcast: multiple listeners
  /// receive the same frames. Calling subscribe with the same topic again
  /// returns the existing stream.
  Stream<RealtimeFrame> subscribe(String topic) {
    if (_disposed) {
      throw StateError('RealtimeHub disposed');
    }
    final ctrl = _topicCtrls.putIfAbsent(
      topic,
      () => StreamController<RealtimeFrame>.broadcast(),
    );
    if (!_topics.contains(topic)) {
      _topics.add(topic);
      // First topic → establish connection. Subsequent topics get added on next reconnect
      // (the wire protocol supports POST /v1/realtime/subscribe but for MVP we accept
      // a brief gap at first add).
      if (_client == null) {
        unawaited(_connect());
      } else {
        // Already connected → re-establish to include the new topic.
        // (Cleaner path: POST /subscribe; landed in P3.6 with auth tokens.)
        _reconnect();
      }
    }
    return ctrl.stream;
  }

  /// Stop receiving a topic. The associated stream is closed.
  void unsubscribe(String topic) {
    _topics.remove(topic);
    final ctrl = _topicCtrls.remove(topic);
    ctrl?.close();
    if (_topics.isEmpty) {
      _shutdown();
    }
  }

  /// Force a reconnect (e.g. after auth token refresh).
  void reconnect() {
    if (_disposed) return;
    _reconnect();
  }

  Future<void> dispose() async {
    if (_disposed) return;
    // 标记 disposed 之前 flush 一次最新 cursor — debounce 窗口里最新 id
    // 还没落盘, dispose 路径 (用户切账号 / 关 app) 后等于丢了 100ms 的进度.
    _saveDebounce?.cancel();
    _saveDebounce = null;
    final pending = _pendingSaveId;
    final saver = _saveLastEventId;
    _pendingSaveId = null;
    _disposed = true;
    _shutdown();
    for (final c in _topicCtrls.values) {
      await c.close();
    }
    _topicCtrls.clear();
    _topics.clear();
    if (pending != null && saver != null) {
      try {
        await saver(pending);
      } catch (e) {
        _log.warning('flush save on dispose failed: $e');
      }
    }
  }

  // ─── Internal ─────────────────────────────────────────

  void _shutdown() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _heartbeatWatchdog?.cancel();
    _heartbeatWatchdog = null;
    _sub?.cancel();
    _sub = null;
    _client?.close();
    _client = null;
  }

  void _reconnect() {
    _shutdown();
    unawaited(_connect());
  }

  Future<void> _connect() async {
    if (_disposed || _topics.isEmpty) return;

    // v2-4: 首次 connect 前从 dao 读 cursor (一次性, 后续重连用 in-memory
    // _lastEventId, 它在 _dispatch 路径已实时更新, 比 dao 还新).
    if (!_hydrated && _loadLastEventId != null && _lastEventId == null) {
      try {
        final loaded = await _loadLastEventId();
        if (loaded != null && loaded.isNotEmpty) {
          _lastEventId = loaded;
          _log.fine('hydrated last-event-id from dao: $loaded');
        }
      } catch (e) {
        _log.warning('hydrate last-event-id failed: $e');
      }
      _hydrated = true;
    }

    final token = await _config.auth();

    final query = <String, String>{
      'topics': _topics.join(','),
    };
    if (_config.deviceId != null) {
      query['device_id'] = _config.deviceId!;
    }
    final url = _config.endpoint.replace(queryParameters: {
      ..._config.endpoint.queryParameters,
      ...query,
    });

    _log.fine('connecting to $url');
    try {
      _client = _clientFactory();
      final req = http.Request('GET', url)
        ..headers['accept'] = 'text/event-stream'
        ..headers['authorization'] = 'Bearer $token';
      if (_lastEventId != null) {
        req.headers['last-event-id'] = _lastEventId!;
      }
      final resp = await _client!.send(req);

      if (resp.statusCode != 200) {
        _log.warning('connect failed: HTTP ${resp.statusCode}');
        // Drain
        try {
          await resp.stream.drain<void>();
        } catch (_) {}
        _scheduleReconnect();
        return;
      }
      _backoff = _config.initialBackoff;
      _armHeartbeatWatchdog();

      _sub = resp.stream.listen(
        _onChunk,
        onError: (Object e, StackTrace st) {
          _log.warning('SSE error: $e');
          _scheduleReconnect();
        },
        onDone: () {
          _log.fine('SSE stream closed by server');
          if (!_disposed) _scheduleReconnect();
        },
        cancelOnError: true,
      );
    } catch (e) {
      _log.warning('connect threw: $e');
      _scheduleReconnect();
    }
  }

  // SSE frame parser state.
  final StringBuffer _lineBuf = StringBuffer();
  String _currentEvent = 'message';
  String? _currentId;
  final StringBuffer _currentData = StringBuffer();

  void _onChunk(List<int> chunk) {
    _armHeartbeatWatchdog();
    final s = utf8.decode(chunk, allowMalformed: true);
    _lineBuf.write(s);
    String buf = _lineBuf.toString();
    int idx;
    while ((idx = buf.indexOf('\n')) >= 0) {
      final line = buf.substring(0, idx).trimRight();
      buf = buf.substring(idx + 1);
      _onLine(line);
    }
    _lineBuf
      ..clear()
      ..write(buf);
  }

  void _onLine(String line) {
    if (line.isEmpty) {
      // Dispatch frame
      if (_currentData.isNotEmpty) {
        _dispatch(_currentEvent, _currentId, _currentData.toString());
      }
      _currentEvent = 'message';
      _currentId = null;
      _currentData.clear();
      return;
    }
    if (line.startsWith(':')) {
      // Comment — used for heartbeats. Ignore content; keep watchdog armed.
      return;
    }
    if (line.startsWith('id: ')) {
      _currentId = line.substring(4);
    } else if (line.startsWith('event: ')) {
      _currentEvent = line.substring(7);
    } else if (line.startsWith('data: ')) {
      if (_currentData.isNotEmpty) _currentData.write('\n');
      _currentData.write(line.substring(6));
    }
  }

  void _dispatch(String event, String? id, String data) {
    if (event != 'message') return; // server uses single event name per spec
    Map<String, dynamic> envelope;
    try {
      envelope = jsonDecode(data) as Map<String, dynamic>;
    } catch (e) {
      _log.warning('bad SSE data: $e');
      return;
    }
    final topic = envelope['topic'] as String? ?? '';
    final kind = envelope['kind'] as String? ?? '';
    final payload = (envelope['payload'] as Map?)?.cast<String, dynamic>() ?? <String, dynamic>{};
    final traceId = envelope['trace_id'] as String?;

    // v2-6: desync 帧到 → 触发回调让上层清 cursor + 全量 refetch.
    // 关键: desync 帧的 id 不写入 _lastEventId / dao, 否则下次重连还带这个
    // 已"消化掉"的 cursor 重新触发 desync; 也不写 save, 让上层 onDesync 自己
    // 决定 cursor 状态 (典型: dao.clear).
    if (topic == 'system' && kind == 'desync') {
      final code = (payload['code'] as num?)?.toInt() ?? 4009;
      final reason = payload['reason'] as String? ?? '';
      _log.warning('desync received: code=$code reason=$reason');
      // 内存 cursor 清掉, 防止下次 connect 仍带老 id
      _lastEventId = null;
      _pendingSaveId = null;
      _saveDebounce?.cancel();
      _saveDebounce = null;
      // 同步给上层 (异步, 失败只 log)
      final cb = _onDesync;
      if (cb != null) {
        cb(code, reason).catchError((Object e) {
          _log.warning('onDesync handler failed: $e');
        });
      }
      // desync 也通过 system topic 广播给所有订阅者, 便于 UI 显示状态.
      final frame = RealtimeFrame(
        id: id ?? '',
        topic: topic,
        kind: kind,
        payload: payload,
        traceId: traceId,
      );
      for (final c in _topicCtrls.values) {
        c.add(frame);
      }
      return;
    }

    if (id != null) {
      _lastEventId = id;
      _scheduleSave(id);
    }

    final frame = RealtimeFrame(
      id: id ?? '',
      topic: topic,
      kind: kind,
      payload: payload,
      traceId: traceId,
    );
    final ctrl = _topicCtrls[topic];
    ctrl?.add(frame);
    // System "open" / "close" frames may have topic="system"; expose them
    // on every subscriber for visibility.
    if (topic == 'system') {
      for (final c in _topicCtrls.values) {
        if (!identical(c, ctrl)) c.add(frame);
      }
    }
  }

  void _scheduleSave(String id) {
    if (_saveLastEventId == null || _disposed) return;
    _pendingSaveId = id;
    _saveDebounce?.cancel();
    _saveDebounce = Timer(_saveDebounceWindow, _flushSave);
  }

  void _flushSave() {
    final id = _pendingSaveId;
    final saver = _saveLastEventId;
    if (id == null || saver == null) return;
    _pendingSaveId = null;
    // fire-and-forget — 失败不影响 hub.
    saver(id).catchError((Object e) {
      _log.warning('save last-event-id failed: $e');
    });
  }

  void _armHeartbeatWatchdog() {
    _heartbeatWatchdog?.cancel();
    _heartbeatWatchdog = Timer(_config.heartbeatTimeout, () {
      _log.warning('heartbeat timeout — reconnecting');
      _reconnect();
    });
  }

  void _scheduleReconnect() {
    _shutdown();
    if (_disposed || _topics.isEmpty) return;
    final delay = _backoff;
    _backoff = Duration(milliseconds: (_backoff.inMilliseconds * 2).clamp(
      _config.initialBackoff.inMilliseconds,
      _config.maxBackoff.inMilliseconds,
    ));
    _reconnectTimer = Timer(delay, _connect);
  }

  // Test helpers
  @visibleForTesting
  String? get debugLastEventId => _lastEventId;
  @visibleForTesting
  Set<String> get debugTopics => Set.unmodifiable(_topics);
}
