// NotesClient.searchNotes HTTP-layer tests —— 形态对齐 notes_client_test.dart：
// 真实 loopback HttpServer 充当 brain，query 参数 / DTO 解析 / 错误面全走真路。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/notes_client.dart';
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
    Map<String, dynamic> response;
    var status = 200;

    if (req.method == 'GET' && req.uri.path == '/v1/notes/search') {
      final q = req.uri.queryParameters['q'] ?? '';
      if (q.isEmpty) {
        // 服务端契约：q 空 → 400。
        status = 400;
        response = {
          'error': {'code': 'invalid_argument', 'message': 'q required'}
        };
      } else {
        response = {
          'results': [
            {
              'id': 'n1',
              'notebook_id': null,
              'title': '购物清单',
              'is_todo': true,
              // todo_completed_at 可缺席 —— 故意不给这个 key。
              'updated_at': '2026-07-29T01:00:00Z',
              'snippet': '买点 <mark>牛奶</mark> 和鸡蛋 &lt;今天&gt;',
              'rank': 0.85,
            },
            {
              'id': 'n2',
              'notebook_id': 'nb1',
              'title': '另一篇',
              'is_todo': false,
              'todo_completed_at': null,
              'updated_at': '2026-07-28T01:00:00Z',
              'snippet': '<mark>牛奶</mark> 的另一种写法',
              'rank': 0.4,
            },
          ]
        };
      }
    } else {
      status = 404;
      response = {'error': 'unknown_route'};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

void main() {
  late _FakeSearch fake;
  late NotesClient client;

  setUp(() async {
    fake = await _FakeSearch.start();
    client = NotesClient(fake.url, 'tok-123');
  });

  tearDown(() => fake.stop());

  test('searchNotes 发送 q/limit query 并解析结果 DTO', () async {
    final results = await client.searchNotes('牛奶 鸡蛋', limit: 20);
    // queryParameters 已 urldecode —— 客户端只需保证原始值正确传递。
    expect(fake.lastQuery['q'], '牛奶 鸡蛋');
    expect(fake.lastQuery['limit'], '20');

    expect(results, hasLength(2));
    final first = results.first;
    expect(first.id, 'n1');
    expect(first.notebookId, isNull, reason: 'notebook_id 可为 null');
    expect(first.isTodo, isTrue);
    expect(first.todoCompletedAt, isNull, reason: 'todo_completed_at 可缺席');
    expect(first.snippet, contains('<mark>牛奶</mark>'));
    expect(first.rank, 0.85);
    expect(first.updatedAt, DateTime.utc(2026, 7, 29, 1));

    final second = results[1];
    expect(second.notebookId, 'nb1');
    expect(second.rank, 0.4);
  });

  test('searchNotes limit 默认值 20，自定义值透传', () async {
    await client.searchNotes('x');
    expect(fake.lastQuery['limit'], '20');
    await client.searchNotes('x', limit: 50);
    expect(fake.lastQuery['limit'], '50');
  });

  test('q 空时服务端 400 → NotesApiError', () async {
    try {
      await client.searchNotes('');
      fail('should have thrown');
    } on NotesApiError catch (e) {
      expect(e.status, 400);
      expect(e.body, contains('invalid_argument'));
    }
  });
}
