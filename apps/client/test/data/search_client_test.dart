// SearchClient HTTP-layer tests (N3 include_notes) — 形态对齐
// notes_client_test.dart：真实 loopback HttpServer 充当 brain，校验
// 请求 body 的 include_notes 开关与响应 notes 分组的解析。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/search_client.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeSearch {
  _FakeSearch._(this.server);
  final HttpServer server;
  String lastBody = '';

  static Future<_FakeSearch> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeSearch._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    lastBody = await utf8.decoder.bind(req).join();
    final sent = jsonDecode(lastBody) as Map<String, dynamic>;
    final includeNotes = sent['include_notes'] == true;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode({
      'query': sent['query'],
      'scope': sent['scope'],
      'fused': [
        {
          'id': 'wiki:page:p1',
          'score': 0.9,
          'meta': {'source': 'wiki', 'page_id': 'p1', 'title': '页面甲'},
        },
        if (includeNotes)
          {
            'id': 'note:n1',
            'score': 0.5,
            'meta': {'kind': 'note', 'title': '笔记甲'},
          },
      ],
      'images': const [],
      if (includeNotes)
        'notes': [
          {
            'id': 'n1',
            'title': '笔记甲',
            'snippet': '包含关键词的一段',
            'score': 0.8,
            'updated_at': '2026-07-29T01:00:00Z',
          }
        ],
    }));
    await req.response.close();
  }
}

void main() {
  late _FakeSearch fake;
  late SearchClient client;

  setUp(() async {
    fake = await _FakeSearch.start();
    client = SearchClient(fake.url, 'tok-123');
  });

  tearDown(() => fake.stop());

  test('默认不带 include_notes，响应 notes 为空', () async {
    final resp = await client.search(query: '关键词');
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent.containsKey('include_notes'), isFalse,
        reason: '默认 false 不应出现在 body 里');
    expect(resp.notes, isEmpty);
    expect(resp.fused, hasLength(1));
  });

  test('includeNotes=true 带 include_notes 并解析 notes 分组 + kind',
      () async {
    final resp = await client.search(query: '关键词', includeNotes: true);
    final sent = jsonDecode(fake.lastBody) as Map<String, dynamic>;
    expect(sent['include_notes'], isTrue);

    expect(resp.notes, hasLength(1));
    final hit = resp.notes.single;
    expect(hit.id, 'n1');
    expect(hit.title, '笔记甲');
    expect(hit.snippet, '包含关键词的一段');
    expect(hit.score, 0.8);
    expect(hit.updatedAt, DateTime.utc(2026, 7, 29, 1));

    // fused 里 kind='note' 的条目也能识别（UI 用它把笔记从页面分组剔除）。
    expect(resp.fused, hasLength(2));
    expect(resp.fused.last.kind, 'note');
    expect(resp.fused.first.kind, '');
  });
}
