// N2 全文搜索：notesSearchResultsProvider 接线 + snippet <mark> 解析测试。
//
// provider 测试用真实 loopback HttpServer 充当 brain（形态对齐
// notes_client_test.dart）；snippet 解析是纯函数（notes_home_page 的
// searchSnippetSpans），直接单测。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart' as api;
import 'package:biumind/data/local/db.dart';
import 'package:biumind/data/local/notes_dao.dart';
import 'package:biumind/data/notes_providers.dart';
import 'package:biumind/data/notes_repository.dart';
import 'package:biumind/features/notes/application/notes_ui_providers.dart';
import 'package:biumind/features/notes/presentation/notes_home_page.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeSearch {
  _FakeSearch._(this.server);
  final HttpServer server;
  Map<String, String> lastQuery = {};

  static Future<_FakeSearch> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeSearch._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');
  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    lastQuery = req.uri.queryParameters;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode({
      'results': [
        {
          'id': 'n1',
          'notebook_id': null,
          'title': '购物清单',
          'is_todo': false,
          'updated_at': '2026-07-29T01:00:00Z',
          'snippet': '买点 <mark>牛奶</mark>',
          'rank': 0.9,
        }
      ]
    }));
    await req.response.close();
  }
}

void main() {
  group('notesSearchResultsProvider', () {
    late _FakeSearch fake;
    late AppDb db;
    late ProviderContainer container;

    setUp(() async {
      fake = await _FakeSearch.start();
      db = AppDb.memory();
      final repo = NotesRepository(
        dao: NotesDao(db, scope: 'test-scope'),
        client: api.NotesClient(fake.url, 'tok'),
      );
      container = ProviderContainer(overrides: <Override>[
        notesRepositoryProvider.overrideWithValue(repo),
      ]);
    });

    tearDown(() async {
      container.dispose();
      await db.close();
      await fake.stop();
    });

    test('空查询不发请求，直接返回空列表', () async {
      final results = await container.read(notesSearchResultsProvider.future);
      expect(results, isEmpty);
      expect(fake.lastQuery, isEmpty, reason: '空 q 不应触发 HTTP');
    });

    test('写入关键词后请求 /v1/notes/search 并解析结果', () async {
      container.read(notesSearchQueryProvider.notifier).state = ' 牛奶 ';
      final results = await container.read(notesSearchResultsProvider.future);
      expect(fake.lastQuery['q'], '牛奶', reason: 'query 先 trim 再发');
      expect(results.single.id, 'n1');
      expect(results.single.snippet, contains('<mark>'));
    });
  });

  group('searchSnippetSpans', () {
    test('mark 段高亮、其余段原样，标签不泄漏到文本', () {
      const markStyle = TextStyle(fontWeight: FontWeight.w600);
      final spans =
          searchSnippetSpans('买点 <mark>牛奶</mark> 和鸡蛋', markStyle: markStyle);
      expect(spans.map((s) => s.text).toList(), ['买点 ', '牛奶', ' 和鸡蛋']);
      expect(spans[0].style, isNull);
      expect(spans[1].style?.fontWeight, FontWeight.w600);
      expect(spans[2].style, isNull);
      expect(spans.map((s) => s.text).join(), '买点 牛奶 和鸡蛋',
          reason: '尖括号标签不应泄漏到文本');
    });

    test('HTML 实体解码，&amp; 最后解避免二次解码', () {
      final spans =
          searchSnippetSpans('a &lt;b&gt; &amp;amp; c &quot;d&quot;');
      expect(spans.single.text, 'a <b> &amp; c "d"');
    });

    test('<mark> 以外的残留标签被剥离', () {
      final spans = searchSnippetSpans('x <b>bold</b> <mark>y</mark>');
      expect(spans.map((s) => s.text).toList(), ['x bold ', 'y']);
    });

    test('无 mark 的 snippet 单段返回', () {
      final spans = searchSnippetSpans('plain text');
      expect(spans.single.text, 'plain text');
    });
  });
}
