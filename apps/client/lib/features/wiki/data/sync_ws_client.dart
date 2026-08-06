/// WebSocket sync client.
///
/// 连接 brain `GET /v1/wiki/projects/{pid}/sync?token=…&since=…`
/// 端点协议格式：
///
///   - {type: "catchup", events: [...]}
///   - {type: "ready", since: <event_id>}
///   - {type: "live", event: {...}}
///   - {type: "ping"}
///   - {type: "error", reason: "..."}
///
/// 重连策略由调用方负责 — 流断开 stream 自动 close；调用方决定是否重连
/// 以及从哪个 since 续传。
library;

import 'dart:async';
import 'dart:convert';

import 'package:web_socket_channel/web_socket_channel.dart';

class SyncMessage {
  const SyncMessage({required this.type, required this.payload});

  /// "catchup" | "ready" | "live" | "ping" | "error"
  final String type;
  final Map<String, Object?> payload;
}

/// 打开 sync WebSocket 并 yield 解码后的消息。
///
/// [baseUrl] 是 model-relay 的 http(s):// origin（内部会改写成 ws/wss）。
/// [projectId] 是项目 UUID。[token] 走 query param，因为浏览器 WS
/// handshake 不能加 header。[since] 0 表示首次连接，>0 表示从某个
/// catchup checkpoint 续传。
Stream<SyncMessage> connectSync({
  required Uri baseUrl,
  required String projectId,
  required String token,
  int since = 0,
}) async* {
  final uri = _buildUri(baseUrl, projectId, token, since);
  final channel = WebSocketChannel.connect(uri);
  try {
    await for (final raw in channel.stream) {
      if (raw is! String) continue;
      Object? decoded;
      try {
        decoded = jsonDecode(raw);
      } on FormatException {
        continue;
      }
      if (decoded is! Map<String, Object?>) continue;
      final type = decoded['type'];
      if (type is! String) continue;
      yield SyncMessage(type: type, payload: decoded);
    }
  } finally {
    await channel.sink.close();
  }
}

Uri _buildUri(Uri origin, String projectId, String token, int since) {
  final scheme = origin.scheme == 'https' ? 'wss' : 'ws';
  final port = origin.hasPort
      ? origin.port
      : (origin.scheme == 'https' ? 443 : 80);
  return origin.replace(
    scheme: scheme,
    port: port,
    path: '/v1/wiki/projects/$projectId/sync',
    queryParameters: <String, String>{
      'token': token,
      'since': since.toString(),
    },
  );
}
