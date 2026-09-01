// WikiClient ingest 镜像端点测试（W2 契约）：
//   * createIngestTask(processor: 'client') 把 processor 传进 body，
//     响应的 processor 字段被解析；
//   * patchIngestTask 发 PATCH /ingest/tasks/{tid}，body 序列化
//     {status, progress, error}，progress 是完整对象整体替换；
//   * 缺省 processor 时 body 不带该字段（服务端默认 server）。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/wiki_client.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeIngest {
  _FakeIngest._(this.server);
  final HttpServer server;

  /// 收到的请求记录：(method, path, decodedBody)。
  final List<({String method, String path, Map<String, dynamic> body})> seen =
      [];

  static Future<_FakeIngest> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeIngest._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');

  Future<void> stop() => server.close(force: true);

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    final parts = path.split('/').where((s) => s.isNotEmpty).toList();
    Map<String, dynamic>? response;
    var status = 200;

    if (req.method == 'POST' &&
        parts.length == 5 &&
        parts[4] == 'ingest') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'POST', path: path, body: j));
      response = {
        'id': 'task-1',
        'status': 'pending',
        'processor': j['processor'] ?? 'server',
      };
    } else if (req.method == 'PATCH' &&
        parts.length == 7 &&
        parts[4] == 'ingest' &&
        parts[5] == 'tasks') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'PATCH', path: path, body: j));
      response = {
        'id': parts[6],
        'status': j['status'] ?? 'running',
        'processor': 'client',
      };
    } else {
      status = 404;
      response = {'error': 'unknown_route', 'path': path};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

void main() {
  test('createIngestTask: processor=client 进 body，响应解析 processor', () async {
    final fake = await _FakeIngest.start();
    addTearDown(fake.stop);
    final c = WikiClient(fake.url, 'tok');

    final task = await c.createIngestTask(
      'p1',
      sourceId: 'src-1',
      title: 'a.pdf',
      processor: 'client',
    );
    expect(task.taskId, 'task-1');
    expect(task.processor, 'client');

    final req = fake.seen.single;
    expect(req.body['processor'], 'client');
    expect(req.body['source_id'], 'src-1');
    expect(req.body['title'], 'a.pdf');
  });

  test('createIngestTask: 缺省不带 processor 字段，响应缺省解析 server',
      () async {
    final fake = await _FakeIngest.start();
    addTearDown(fake.stop);
    final c = WikiClient(fake.url, 'tok');

    final task = await c.createIngestTask('p1', sourceId: 'src-1');
    expect(task.processor, 'server');
    expect(fake.seen.single.body.containsKey('processor'), isFalse);
  });

  test('patchIngestTask: PATCH 路径 + {status, progress, error} 序列化',
      () async {
    final fake = await _FakeIngest.start();
    addTearDown(fake.stop);
    final c = WikiClient(fake.url, 'tok');

    final updated = await c.patchIngestTask(
      'p1',
      'task-1',
      status: 'running',
      progress: {'phase': 'extract', 'percent': 42},
    );
    expect(updated.taskId, 'task-1');
    expect(updated.status, 'running');
    expect(updated.processor, 'client');

    final req = fake.seen.single;
    expect(req.method, 'PATCH');
    expect(req.path, '/v1/wiki/projects/p1/ingest/tasks/task-1');
    expect(req.body['status'], 'running');
    expect(req.body['progress'], {'phase': 'extract', 'percent': 42});
    expect(req.body.containsKey('error'), isFalse);
  });

  test('patchIngestTask: failed 带 error；仅 progress 时不带 status', () async {
    final fake = await _FakeIngest.start();
    addTearDown(fake.stop);
    final c = WikiClient(fake.url, 'tok');

    await c.patchIngestTask('p1', 'task-1',
        status: 'failed', error: 'PDF 已加密');
    await c.patchIngestTask('p1', 'task-1',
        progress: {'phase': 'load', 'percent': 10});

    final failed = fake.seen[0].body;
    expect(failed['status'], 'failed');
    expect(failed['error'], 'PDF 已加密');
    expect(failed.containsKey('progress'), isFalse);

    final prog = fake.seen[1].body;
    expect(prog.containsKey('status'), isFalse);
    expect(prog['progress'], {'phase': 'load', 'percent': 10});
  });
}
