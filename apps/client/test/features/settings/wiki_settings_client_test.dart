// WikiSettingsClient + IngestModelNotifier 契约测试（B2）。
//
// 用 loopback HttpServer 假 identity 服务（先例：test/data/wiki_client_test.dart），
// 覆盖：GET 解析（含空串 → null）、PUT body（含 null → 空串清除）、
// Bearer 头、notifier 初始拉取 / setModel 成功与失败回滚。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/features/settings/application/wiki_settings_providers.dart';
import 'package:biumind/features/settings/data/wiki_settings_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeIdentity {
  _FakeIdentity._(this.server);
  final HttpServer server;

  String model = '';
  final List<String> seenAuth = [];
  final List<Map<String, dynamic>> seenPutBodies = [];
  int putStatus = 200;

  static Future<_FakeIdentity> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeIdentity._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');

  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    seenAuth.add(req.headers.value(HttpHeaders.authorizationHeader) ?? '');
    final body = await utf8.decoder.bind(req).join();
    if (req.uri.path != '/v1/identity/me/settings/ingest-model') {
      req.response.statusCode = 404;
      await req.response.close();
      return;
    }
    if (req.method == 'GET') {
      req.response.headers.contentType = ContentType.json;
      req.response.write(jsonEncode({'model': model}));
    } else if (req.method == 'PUT') {
      seenPutBodies.add(jsonDecode(body) as Map<String, dynamic>);
      if (putStatus != 200) {
        req.response.statusCode = putStatus;
        req.response.write('{"error":{"message":"boom"}}');
      } else {
        model = (seenPutBodies.last['model'] as String?) ?? '';
        req.response.headers.contentType = ContentType.json;
        req.response.write(jsonEncode({'model': model}));
      }
    } else {
      req.response.statusCode = 405;
    }
    await req.response.close();
  }
}

void main() {
  late _FakeIdentity fake;
  late WikiSettingsClient client;

  setUp(() async {
    fake = await _FakeIdentity.start();
    client = WikiSettingsClient(baseUrl: fake.url, bearerProvider: () => 'tok');
  });

  tearDown(() => fake.stop());

  group('WikiSettingsClient', () {
    test('GET 返回已设置模型', () async {
      fake.model = 'claude-sonnet-4';
      expect(await client.getIngestModel(), 'claude-sonnet-4');
      expect(fake.seenAuth.single, 'Bearer tok');
    });

    test('GET 空串 / 缺字段 → null（未设置）', () async {
      expect(await client.getIngestModel(), isNull); // 默认 model=''
    });

    test('PUT 设置模型', () async {
      await client.putIngestModel('gpt-5');
      expect(fake.seenPutBodies.single, {'model': 'gpt-5'});
    });

    test('PUT null → 空串清除', () async {
      await client.putIngestModel(null);
      expect(fake.seenPutBodies.single, {'model': ''});
    });

    test('4xx 抛 ApiError', () async {
      fake.putStatus = 400;
      expect(
        () => client.putIngestModel('x'),
        throwsA(anything),
      );
    });
  });

  group('IngestModelNotifier', () {
    /// 等 notifier 的初始 _load 完成（真实 HTTP 走 loopback，轮询状态）。
    Future<IngestModelNotifier> settled(IngestModelNotifier n) async {
      for (var i = 0; i < 200 && n.state.isLoading; i++) {
        await Future<void>.delayed(const Duration(milliseconds: 5));
      }
      return n;
    }

    test('初始拉取：已设置模型 → AsyncData(model)', () async {
      fake.model = 'deepseek-v3';
      final n = await settled(IngestModelNotifier(() => client));
      expect(n.state, const AsyncValue<String?>.data('deepseek-v3'));
    });

    test('初始拉取：未设置 → AsyncData(null)', () async {
      final n = await settled(IngestModelNotifier(() => client));
      expect(n.state, const AsyncValue<String?>.data(null));
    });

    test('client null（未登录）→ AsyncData(null)，不发请求', () async {
      final n = await settled(IngestModelNotifier(() => null));
      expect(n.state, const AsyncValue<String?>.data(null));
      expect(fake.seenAuth, isEmpty);
    });

    test('setModel 成功：PUT + 状态更新', () async {
      final n = await settled(IngestModelNotifier(() => client));
      await n.setModel('kimi-k2');
      expect(fake.seenPutBodies.single, {'model': 'kimi-k2'});
      expect(n.state, const AsyncValue<String?>.data('kimi-k2'));

      // 清除回默认。
      await n.setModel(null);
      expect(fake.seenPutBodies.last, {'model': ''});
      expect(n.state, const AsyncValue<String?>.data(null));
    });

    test('setModel 失败：rethrow 且状态不变', () async {
      fake.model = 'keep-me';
      final n = await settled(IngestModelNotifier(() => client));
      fake.putStatus = 500;
      await expectLater(n.setModel('other'), throwsA(anything));
      expect(n.state, const AsyncValue<String?>.data('keep-me'));
    });

    test('GET 失败 → AsyncError', () async {
      await fake.stop();
      final n = IngestModelNotifier(() => client);
      await settled(n);
      expect(n.state.hasError, isTrue);
      // 重新起一个假服务供 tearDown 关闭。
      fake = await _FakeIdentity.start();
    });
  });
}
