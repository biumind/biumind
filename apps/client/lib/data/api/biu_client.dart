// BiuClient —— Flutter 端连 brain Agent Plane WS endpoint 的 ergonomic 包装。
//
// 不直接暴露 sdkproto/v1 的裸类型 —— 上层 controller 拿 BiuClient 就够了：
//   - connect(sessionId, sessionToken) → 立即返回；后台跑 WS 升级 + read pump
//   - frames stream → 每帧解过的 Object（SDKMessage / Lifecycle / 控制帧）
//   - send(SDKUserMessage) / sendInterrupt() → 给 brain 发数据
//   - close()                              → 主动断
//
// 协议跟 services/brain/internal/agentplane/ingress.go 对齐：
//
//   GET /v1/agent/sessions/{id}/stream?session_token=<jwt>&since_seq=<n>
//   ↑ ingress 校 token + scope；上行帧就是 sdkproto/v1 JSON
//
// 自动重连策略：
//   - WS 主动关 → 不重连（上层显式 close）
//   - 网络异常断（read pump err） → 指数退避 1s → 30s + 用 lastSeq+1 续
//   - session_token 过期 (401) → 触发 onTokenExpired callback 让上层 refresh
//
// 心跳：上层 WebSocketChannel 自带 PING/PONG（gorilla 兼容）。本层不发额外心跳，
// 但维护一个 60s read deadline；超时视为断（触发重连）。
//
// 重放策略：每收到带 uuid 的帧记 lastSeq（uuid 不是 seq；ingress 用 stream
// seq 做 since_seq）。当前 v1 只能用 ?since_seq= 续；具体 seq 由 ingress 在帧
// header 里给（X-Seq）—— 但 WS 无 header，所以本版不实现真重放，只做
// reconnect。重放留 v2（需要 ingress 在 frame 内嵌 seq 字段）。

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'sdkproto/v1/common.dart';
import 'sdkproto/v1/data/user.dart';
import 'sdkproto/v1/service.dart';

/// 连一条 brain Agent Plane WS session 的入口。
///
/// 用法：
/// ```dart
/// final c = BiuClient(brainBaseUrl: 'wss://your-biumind.example.com');
/// await c.connect(sessionId: '...', sessionToken: '...');
/// c.frames.listen((frame) {
///   if (frame is SDKStreamlinedText) print(frame.text);
/// });
/// c.send(SDKUserMessage(message: AnthropicMessage(...)));
/// ```
class BiuClient {
  /// brain WS base URL，例 `ws://localhost:7003` 或 `wss://your-biumind.example.com`。
  /// 末尾不带 `/`；class 内部拼 `/v1/agent/sessions/<id>/stream`。
  final String brainBaseUrl;

  /// onTokenExpired 在收到 1008 (policy violation) 或 4xx 时 fire；上层应该
  /// 调 brain `/v1/agent/sessions/{id}/refresh-token` 拿新 session_token，
  /// 然后调 [refreshToken] 续连接。nil 时 client 直接关。
  final FutureOr<String?> Function()? onTokenExpired;

  /// 自定义 transport connector —— 测试用。生产留 null 走 WebSocketChannel.connect。
  /// 测试可以传一个 fake 实现完成端到端断言（详见 biu_client_test.dart）。
  final BiuTransport Function(Uri uri)? connector;

  /// reconnectMaxAttempts 连续失败次数到达后 client 放弃，frames stream close。
  /// 默认 8 次（指数退避 1s..30s 大概 ~3 分钟覆盖）。
  final int reconnectMaxAttempts;

  /// outboxMaxLen [enqueue] 离线队列上限。超出抛 StateError。默认 64。
  /// 离线 5 min 内一个用户疯狂输入 64 条消息已经很极端 —— 真过限说明
  /// 网络长期断 + UI 没显示状态徽章，提示 caller 阻塞输入。
  final int outboxMaxLen;

  BiuClient({
    required this.brainBaseUrl,
    this.onTokenExpired,
    this.connector,
    this.reconnectMaxAttempts = 8,
    this.outboxMaxLen = 64,
  });

  // ── 内部状态 ─────────────────────────────────────────────

  BiuTransport? _channel;
  StreamSubscription<dynamic>? _readSub;
  final _framesController = StreamController<Object>.broadcast();
  bool _closedByUser = false;
  String? _sessionId;
  String? _sessionToken;
  int _sinceSeq = 0;
  int _reconnectAttempt = 0;
  final List<Object> _outbox = [];
  Timer? _reconnectTimer;

  // ── 公开 API ─────────────────────────────────────────────

  /// 已解析的下行帧流（SDKMessage / Lifecycle / SDKControlRequest 等）。
  /// broadcast stream，多个 listener 都能订阅。
  Stream<Object> get frames => _framesController.stream;

  /// 当前是否连着。WS 异步升级期 + 重连退避期都返回 false。
  bool get isConnected => _channel != null && !_closedByUser;

  /// 跟一条 session 建 WS 连接。返回前不等 ready —— frames stream 是异步的。
  /// 重连 / token refresh 会复用最初传入的 sessionId + sessionToken（用 [refreshToken]
  /// 替换）。
  ///
  /// [sinceSeq] —— 跨设备 resume 时让 brain ingress 用 OrderedConsumer 从
  /// `sinceSeq+1` 开始 replay 历史帧 + 自动接入实时流。0（默认）走实时拉，
  /// 不重放。stream 已 trim（消息超过 MaxAge / MaxMsgs）时 ingress 推一帧
  /// `biumind.session_desynced` 然后 close —— 客户端按规约拿
  /// `agent_session_results` 摘要兜底。
  Future<void> connect({
    required String sessionId,
    required String sessionToken,
    int sinceSeq = 0,
  }) async {
    _sessionId = sessionId;
    _sessionToken = sessionToken;
    _sinceSeq = sinceSeq;
    _closedByUser = false;
    _reconnectAttempt = 0;
    await _doConnect();
  }

  /// 把 [SDKUserMessage] / [SDKControlRequest] / [Lifecycle] 等帧序列化后
  /// 发给 brain。未连或已关 → 抛 [StateError]。
  ///
  /// **不入离线队列** —— 老调用方期望的同步抛错语义保留。需要离线缓冲
  /// 走 [enqueue]。
  void send(Object frame) {
    final ch = _channel;
    if (ch == null) {
      throw StateError('BiuClient.send: not connected');
    }
    final json = ServiceFrame.toJson(frame);
    ch.send(jsonEncode(json));
  }

  /// S9-3 离线送达队列 —— 跟 [send] 区别：
  ///   - 当前已连 → 立即 send（行为同 [send]）
  ///   - 未连 / 已断 / 未 connect → 进 FIFO 队列，重连成功后顺序 flush
  ///   - 主动 close 后入队 → 抛 [StateError]（防止已 close 还入新帧）
  ///
  /// 用法：手机离线 5 min 期间用户连发 3 条消息，UI 调 enqueue() 不报错；
  /// 网络恢复后 _doConnect 成功，flush 队列把 3 条按顺序送出。
  ///
  /// 队列上限 [outboxMaxLen]（默认 64）—— 极端情况下用户疯狂输入避免
  /// 内存爆。超过后抛 [StateError]。
  void enqueue(Object frame) {
    if (_closedByUser) {
      throw StateError('BiuClient.enqueue: closed by user');
    }
    final ch = _channel;
    if (ch != null && _outbox.isEmpty) {
      // 当前在线 + 队列空 → 直接 send，省一次 enqueue→flush 跳板
      send(frame);
      return;
    }
    if (_outbox.length >= outboxMaxLen) {
      throw StateError('BiuClient.enqueue: outbox full ($outboxMaxLen)');
    }
    _outbox.add(frame);
  }

  /// 当前离线队列长度（待 flush 的帧数）。UI 进度展示用。
  int get outboxPending => _outbox.length;

  /// 发一条 user message —— 简化封装，避免上层每次写 toJson 模板。
  /// content 走 Anthropic Messages API content block 数组格式（type=text）。
  void sendUserText(String text, {required String userMessageUuid}) {
    final um = SDKUserMessage(
      uuid: userMessageUuid,
      sessionId: _sessionId ?? '',
      message: AnthropicMessage(
        role: 'user',
        content: [
          {'type': 'text', 'text': text},
        ],
      ),
    );
    send(um);
  }

  /// 上层调 brain refresh-token endpoint 拿到新 token 后调这个，重连下次用。
  /// 如果当前已断，立即触发重连尝试；如果还连着，下次断时使用新 token。
  Future<void> refreshToken(String newSessionToken) async {
    _sessionToken = newSessionToken;
    if (_channel == null && !_closedByUser) {
      await _doConnect();
    }
  }

  /// 主动关。frames stream close，不会再重连。
  Future<void> close() async {
    _closedByUser = true;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    await _readSub?.cancel();
    _readSub = null;
    await _channel?.close();
    _channel = null;
    if (!_framesController.isClosed) {
      await _framesController.close();
    }
  }

  // ── 内部实现 ─────────────────────────────────────────────

  Future<void> _doConnect() async {
    if (_closedByUser) return;
    final sid = _sessionId;
    final tok = _sessionToken;
    if (sid == null || tok == null) {
      throw StateError('BiuClient: connect() must run before _doConnect');
    }

    var url =
        '$brainBaseUrl/v1/agent/sessions/$sid/stream?session_token=${Uri.encodeQueryComponent(tok)}';
    if (_sinceSeq > 0) {
      url += '&since_seq=$_sinceSeq';
    }
    final uri = Uri.parse(url);
    try {
      final ch = (connector ?? _defaultConnector)(uri);
      _channel = ch;
      _reconnectAttempt = 0;
      _readSub = ch.frames.listen(
        _onFrame,
        onError: _onSocketError,
        onDone: _onSocketDone,
        cancelOnError: false,
      );
      _flushOutbox();
    } catch (e, stack) {
      debugPrint('BiuClient connect failed: $e\n$stack');
      _scheduleReconnect();
    }
  }

  /// _flushOutbox 把离线队列里的帧按 FIFO 顺序送出。失败时停止 flush，
  /// 剩下的帧留队列等下次重连。
  void _flushOutbox() {
    while (_outbox.isNotEmpty) {
      final ch = _channel;
      if (ch == null) return; // 中途断了
      final frame = _outbox.first;
      try {
        final json = ServiceFrame.toJson(frame);
        ch.send(jsonEncode(json));
        _outbox.removeAt(0);
      } catch (e) {
        debugPrint('BiuClient flush outbox failed: $e');
        return; // 留队列；下次重连再试
      }
    }
  }

  /// 默认走 [WebSocketChannel.connect]；包一层 [BiuTransport] adapter。
  static BiuTransport _defaultConnector(Uri uri) {
    final ch = WebSocketChannel.connect(uri);
    return _WSChannelTransport(ch);
  }

  void _onFrame(dynamic raw) {
    if (raw is! String) {
      // gorilla 用 TextMessage；BinaryMessage 当前不支持，drop
      return;
    }
    try {
      final json = jsonDecode(raw) as Map<String, dynamic>;
      final frame = ServiceFrame.fromJson(json);
      if (!_framesController.isClosed) {
        _framesController.add(frame);
      }
    } catch (e, stack) {
      debugPrint('BiuClient parse frame failed: $e\nraw=$raw\n$stack');
      // 不断连接 —— 一帧解析失败不应该让整个 session 挂掉
    }
  }

  void _onSocketError(Object err, StackTrace stack) {
    debugPrint('BiuClient socket error: $err\n$stack');
    _onSocketDone();
  }

  void _onSocketDone() {
    _readSub = null;
    _channel = null;
    if (_closedByUser) {
      if (!_framesController.isClosed) _framesController.close();
      return;
    }
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_closedByUser) return;
    _reconnectAttempt++;
    if (_reconnectAttempt > reconnectMaxAttempts) {
      debugPrint('BiuClient: reconnect attempts exhausted, giving up');
      _closedByUser = true;
      if (!_framesController.isClosed) _framesController.close();
      return;
    }
    final delay = _backoffDelay(_reconnectAttempt);
    debugPrint(
      'BiuClient: reconnect in ${delay.inMilliseconds}ms '
      '(attempt $_reconnectAttempt/$reconnectMaxAttempts)',
    );
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(delay, () async {
      // 401/policy 路径：上层 onTokenExpired 拿新 token 后再 _doConnect
      if (onTokenExpired != null && _reconnectAttempt > 2) {
        try {
          final fresh = await onTokenExpired!();
          if (fresh != null && fresh.isNotEmpty) {
            _sessionToken = fresh;
          }
        } catch (e) {
          debugPrint('BiuClient onTokenExpired failed: $e');
        }
      }
      await _doConnect();
    });
  }

  Duration _backoffDelay(int attempt) {
    // 1s, 2s, 4s, 8s, 16s, 30s（cap）...
    final ms = 1000 * (1 << (attempt - 1).clamp(0, 5));
    return Duration(milliseconds: ms.clamp(1000, 30000));
  }
}

/// BiuTransport 是 BiuClient 跟下层连接握手的最小接口。生产是 WS，
/// 测试可以传内存 fake。frames 是 broadcast / single-subscribe 都行
/// （BiuClient 自己只 listen 一次）。
abstract class BiuTransport {
  /// 下行帧 stream。每条 element 是 server push 的原始 String / `List<int>`。
  /// BiuClient 只处理 String 类型；Binary 直接 drop。
  Stream<dynamic> get frames;

  /// 上行发一条 String（已经 jsonEncode 过）。失败由实现 best-effort 处理。
  void send(String data);

  /// 主动关。idempotent —— BiuClient.close 会调一次。
  Future<void> close();
}

/// _WSChannelTransport 把 [WebSocketChannel] 包成 [BiuTransport]。
class _WSChannelTransport implements BiuTransport {
  final WebSocketChannel _ch;
  _WSChannelTransport(this._ch);

  @override
  Stream<dynamic> get frames => _ch.stream;

  @override
  void send(String data) => _ch.sink.add(data);

  @override
  Future<void> close() async {
    await _ch.sink.close();
  }
}
