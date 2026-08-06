// 验证 _http_helpers 的全局计费拦截 hook (billingErrorHandler) 的**精确
// gate**: 只在明确的计费信号上触发, 不对无关 402/429 误弹。
//
// 起一个 in-process HttpServer 按 path 返回不同错误体, 用 apiRequest 打它,
// 断言 billingErrorHandler 是否被调 + 参数。

import 'dart:io';

import 'package:biumind/data/api/_http_helpers.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  late HttpServer server;
  late Uri base;
  List<(int, String, String)> fired = [];

  setUp(() async {
    fired = [];
    billingErrorHandler = (status, code, message) =>
        fired.add((status, code, message));
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    base = Uri.parse('http://${server.address.host}:${server.port}');
    server.listen((req) async {
      final p = req.uri.path;
      switch (p) {
        case '/insufficient':
          req.response.statusCode = 402;
          req.response.headers.contentType = ContentType.json;
          req.response.write(
              '{"error":{"code":"insufficient_credits","message":"余额不足"}}');
        case '/quota-json':
          req.response.statusCode = 429;
          req.response.write('{"error":{"code":"channel_quota_exhausted","message":"配额耗尽"}}');
        case '/quota-text':
          req.response.statusCode = 429;
          req.response.write('rate limit exceeded (rpm)');
        case '/other-402':
          req.response.statusCode = 402;
          req.response.write('{"error":{"code":"some_other_code"}}');
        case '/login-429':
          req.response.statusCode = 429;
          req.response.write('{"error":{"code":"rate_limited","message":"登录太频繁"}}');
        default:
          req.response.statusCode = 200;
          req.response.write('{}');
      }
      await req.response.close();
    });
  });

  tearDown(() async {
    billingErrorHandler = null;
    await server.close(force: true);
  });

  Future<void> hit(String path) async {
    try {
      await apiRequest(method: 'GET', url: base.replace(path: path), bearerToken: null);
    } on ApiError {
      /* 预期抛错; 我们只关心 hook 是否触发 */
    }
  }

  test('402 insufficient_credits → 触发充值信号', () async {
    await hit('/insufficient');
    expect(fired.length, 1);
    expect(fired.first.$1, 402);
    expect(fired.first.$2, 'insufficient_credits');
  });

  test('429 channel_quota_exhausted (JSON) → 触发配额信号', () async {
    await hit('/quota-json');
    expect(fired.length, 1);
    expect(fired.first.$1, 429);
    expect(fired.first.$2, 'channel_quota_exhausted');
  });

  test('429 纯文本 rate limit exceeded → 触发(code 兜底 rate_limited)', () async {
    await hit('/quota-text');
    expect(fired.length, 1);
    expect(fired.first.$1, 429);
    expect(fired.first.$2, 'rate_limited');
  });

  test('402 其它 code → 不触发 (精确 gate)', () async {
    await hit('/other-402');
    expect(fired, isEmpty);
  });

  test('429 登录限流 (rate_limited code, 非 relay 配额信号) → 不触发', () async {
    await hit('/login-429');
    expect(fired, isEmpty);
  });

  test('handler 为 null 时不崩', () async {
    billingErrorHandler = null;
    await hit('/insufficient'); // 不应抛非 ApiError 异常
    expect(fired, isEmpty);
  });
}
