// Cross-platform HTTP helpers built on package:http.
//
// Replaces dart:io HttpClient (which doesn't work on Flutter Web) with
// thin wrappers that match the request shape every existing client
// already used internally:
//
//   apiRequest(...) — single-shot REST call, returns parsed JSON or
//                      throws ApiError on 4xx/5xx.
//   sseStream(...)  — opens a streaming POST, yields raw SSE byte
//                      chunks for the caller to parse.
//
// Each client still owns its DTO classes + URL building; only the
// transport layer is centralized.
//
// 401 拦截：apiRequest / sseStream 收到 401 时调 [authErrorHandler]
// (module-level，main.dart 启动时设置)，拿到新 token 后 retry 一次。
// 调用方可通过 onAuthError 参数覆盖全局 handler（仅测试用）。

import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

/// Generic HTTP error surfaced by the helpers. Other clients (e.g.
/// IdentityApiError) wrap this when they need a specific subtype.
class ApiError implements Exception {
  final String path;
  final int status;
  final String body;
  const ApiError({required this.path, required this.status, required this.body});

  @override
  String toString() => 'ApiError $status $path: $body';
}

/// 全局 401 拦截 hook。返回新的 access_token 用于 retry，返回 null 则放弃。
///
/// 由 main.dart 在启动时绑定到 `tokenManager.handle401`，使所有走
/// apiRequest / sseStream 的 client（admin / wiki / memory / providers /
/// graph / chat 等）自动获得"401 → 自动刷新 + retry"能力。
typedef AuthErrorHandler = Future<String?> Function();
AuthErrorHandler? authErrorHandler;

/// 全局计费/配额拦截 hook。所有走 apiRequest / binaryRequest 的请求收到
/// 计费类错误时调它,让 UI 统一弹「充值」卡 / 配额提示,而不必每个调用点
/// 各自处理。由 main.dart 在启动时绑定。
///
/// 只在**明确的计费信号**上触发(精确 gate, 避免对登录限流等无关 429 误弹):
///   - status=402 且 code=="insufficient_credits"  → 余额不足(充值卡)
///   - status=429 且 relay 配额信号(channel_quota_exhausted / "rate limit
///     exceeded" 文本)                              → 配额/限流(轻提示)
///
/// 注意: 这是**通知**, 不改变错误流 —— 请求仍照常抛 ApiError, caller 的
/// 错误处理不受影响; hook 只负责把 UI 浮起来。
typedef BillingErrorHandler = void Function(int status, String code, String message);
BillingErrorHandler? billingErrorHandler;

/// 在 4xx 响应上按精确信号触发 billingErrorHandler(若绑定)。body 是原始
/// 响应体(可能是 JSON {"error":{"code","message"}} 或纯文本)。
void _maybeFireBillingError(int status, String body) {
  final handler = billingErrorHandler;
  if (handler == null) return;
  if (status != 402 && status != 429) return;

  String code = '';
  String message = '';
  try {
    final decoded = jsonDecode(body);
    if (decoded is Map<String, dynamic>) {
      final err = decoded['error'];
      if (err is Map<String, dynamic>) {
        code = (err['code'] as String?) ?? '';
        message = (err['message'] as String?) ?? '';
      }
    }
  } catch (_) {/* 纯文本 body(如 rpm 中间件的 "rate limit exceeded") */}

  if (status == 402 && code == 'insufficient_credits') {
    handler(402, code, message);
    return;
  }
  if (status == 429 &&
      (code == 'channel_quota_exhausted' ||
          body.contains('rate limit exceeded'))) {
    handler(429, code.isEmpty ? 'rate_limited' : code, message);
  }
}

/// One-shot REST. Encodes [body] as JSON when non-null. Returns the
/// parsed map (empty when [expectNoBody] or the response had no body).
Future<Map<String, dynamic>> apiRequest({
  required String method,
  required Uri url,
  required String? bearerToken,
  Map<String, String>? extraHeaders,
  Object? body,
  bool expectNoBody = false,
  AuthErrorHandler? onAuthError,
}) async {
  // 30s hard timeout — Dart http 包默认无超时,服务端 mid-request 崩溃 +
  // TCP keepalive 没触发的话 future 会无限挂(实测一次 brain 重启把
  // NewThreadDialog 的 listEnvironments 锁死在 AsyncLoading,UI 永远转圈)。
  // 30s 足够覆盖正常请求 + 一轮 token 刷新 retry,超过基本是网络/服务异常,
  // 让 ApiError 抛出来上层显示「加载失败」+ 刷新按钮比一直 spinner 友好。
  const httpTimeout = Duration(seconds: 30);
  Future<http.Response> doRequest(String? token) async {
    final headers = <String, String>{
      'accept': 'application/json',
      if (token != null && token.isNotEmpty) 'authorization': 'Bearer $token',
      if (extraHeaders != null) ...extraHeaders,
    };
    final encoded = body == null ? null : jsonEncode(body);
    if (encoded != null) headers['content-type'] = 'application/json';
    final Future<http.Response> raw = switch (method) {
      'GET' => http.get(url, headers: headers),
      'DELETE' => http.delete(url, headers: headers),
      'POST' => http.post(url, headers: headers, body: encoded),
      'PATCH' => http.patch(url, headers: headers, body: encoded),
      'PUT' => http.put(url, headers: headers, body: encoded),
      _ => throw ArgumentError('unsupported method $method'),
    };
    return raw.timeout(httpTimeout, onTimeout: () {
      throw ApiError(
        path: url.path,
        status: 0, // 0 表示客户端 timeout, 没拿到 HTTP status
        body: 'request timed out after ${httpTimeout.inSeconds}s',
      );
    });
  }

  http.Response resp = await doRequest(bearerToken);

  // 401 → 触发 token 刷新 + retry 一次（仅在 caller 提供了 token 时）
  if (resp.statusCode == 401 && bearerToken != null && bearerToken.isNotEmpty) {
    final handler = onAuthError ?? authErrorHandler;
    if (handler != null) {
      final newToken = await handler();
      if (newToken != null && newToken.isNotEmpty && newToken != bearerToken) {
        resp = await doRequest(newToken);
      }
    }
  }

  if (resp.statusCode >= 400) {
    _maybeFireBillingError(resp.statusCode, resp.body);
    throw ApiError(path: url.path, status: resp.statusCode, body: resp.body);
  }
  if (expectNoBody || resp.body.isEmpty) return const {};
  final decoded = jsonDecode(resp.body);
  if (decoded is Map<String, dynamic>) return decoded;
  // For endpoints that return a JSON array at the top level, wrap so
  // callers can destructure under a known key.
  return {'data': decoded};
}

/// One-shot REST that returns a **binary** response body (e.g. TTS audio
/// from model-relay `/v1/audio/speech`, which streams chunked
/// `audio/mpeg`). Unlike [apiRequest], the success body is raw bytes, not
/// JSON; only 4xx/5xx error bodies are JSON and surface via [ApiError].
///
/// Shares the same 401 → refresh + retry hook and hard timeout as
/// [apiRequest]. [timeout] defaults to 120s because audio synthesis of a
/// long message can take tens of seconds upstream.
Future<Uint8List> binaryRequest({
  required String method,
  required Uri url,
  required String? bearerToken,
  Map<String, String>? extraHeaders,
  Object? body,
  String accept = 'application/octet-stream',
  Duration timeout = const Duration(seconds: 120),
  AuthErrorHandler? onAuthError,
}) async {
  Future<http.Response> doRequest(String? token) async {
    final headers = <String, String>{
      'accept': accept,
      if (token != null && token.isNotEmpty) 'authorization': 'Bearer $token',
      if (extraHeaders != null) ...extraHeaders,
    };
    final encoded = body == null ? null : jsonEncode(body);
    if (encoded != null) headers['content-type'] = 'application/json';
    final Future<http.Response> raw = switch (method) {
      'GET' => http.get(url, headers: headers),
      'POST' => http.post(url, headers: headers, body: encoded),
      _ => throw ArgumentError('unsupported method $method'),
    };
    return raw.timeout(timeout, onTimeout: () {
      throw ApiError(
        path: url.path,
        status: 0,
        body: 'request timed out after ${timeout.inSeconds}s',
      );
    });
  }

  http.Response resp = await doRequest(bearerToken);

  if (resp.statusCode == 401 && bearerToken != null && bearerToken.isNotEmpty) {
    final handler = onAuthError ?? authErrorHandler;
    if (handler != null) {
      final newToken = await handler();
      if (newToken != null && newToken.isNotEmpty && newToken != bearerToken) {
        resp = await doRequest(newToken);
      }
    }
  }

  if (resp.statusCode >= 400) {
    // 错误体是 JSON (writeJSONErr); 直接把原文塞进 ApiError.body 供上层展示.
    final errBody = utf8.decode(resp.bodyBytes, allowMalformed: true);
    _maybeFireBillingError(resp.statusCode, errBody);
    throw ApiError(path: url.path, status: resp.statusCode, body: errBody);
  }
  return resp.bodyBytes;
}

/// Open a streaming POST and yield decoded SSE lines. The caller is
/// responsible for parsing the event/data framing — same protocol on
/// every Brain SSE endpoint.
///
/// 401 时通过 [authErrorHandler] / [onAuthError] retry 一次（带新 token
/// 重新打开流）。流一旦进入 200 OK + body，就交由 caller 全程消费。
///
/// Throws [ApiError] on 4xx/5xx (consumes the body once for the error
/// message).
Stream<String> sseStream({
  required Uri url,
  required String? bearerToken,
  Object? body,
  Map<String, String>? extraHeaders,
  AuthErrorHandler? onAuthError,
}) async* {
  Future<({http.Client client, http.StreamedResponse resp})> open(
    String? token,
  ) async {
    final client = http.Client();
    final req = http.Request('POST', url);
    req.headers['accept'] = 'text/event-stream';
    if (token != null && token.isNotEmpty) {
      req.headers['authorization'] = 'Bearer $token';
    }
    if (body != null) {
      req.headers['content-type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    if (extraHeaders != null) req.headers.addAll(extraHeaders);
    return (client: client, resp: await client.send(req));
  }

  var (:client, :resp) = await open(bearerToken);
  try {
    if (resp.statusCode == 401 && bearerToken != null && bearerToken.isNotEmpty) {
      final handler = onAuthError ?? authErrorHandler;
      if (handler != null) {
        // 401 时丢弃旧 client、refresh、用新 token 重开流
        await resp.stream.drain<void>();
        client.close();
        final newToken = await handler();
        if (newToken != null && newToken.isNotEmpty && newToken != bearerToken) {
          final retry = await open(newToken);
          client = retry.client;
          resp = retry.resp;
        }
      }
    }
    if (resp.statusCode >= 400) {
      final raw = await resp.stream.bytesToString();
      throw ApiError(path: url.path, status: resp.statusCode, body: raw);
    }
    yield* resp.stream
        .transform(utf8.decoder)
        .transform(const LineSplitter());
  } finally {
    client.close();
  }
}
