// DocprocTaskMirror 行为测试（W2，设计文档 §3.5）：
//   * start：建 processor=client 任务 + PATCH running；
//   * progress：节流（距上次 < interval 不发），发完整 {phase, percent}；
//   * done/failed/cancelled：PATCH 对应终态；终态后 progress 不再发；
//   * 镜像调用失败（服务端 500 / 网络断）一律吞掉不抛出 —— 镜像是
//     可见性/接管机制，不是正确性依赖。

import 'dart:convert';
import 'dart:io';

import 'package:biumind/data/api/wiki_client.dart';
import 'package:biumind/features/wiki/data/docproc_task_mirror.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeIngest {
  _FakeIngest._(this.server);
  final HttpServer server;
  final List<({String method, String path, Map<String, dynamic> body})> seen =
      [];
  var failAll = false;

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

    if (failAll) {
      status = 500;
      response = {'error': 'boom'};
    } else if (req.method == 'POST' &&
        parts.length == 5 &&
        parts[4] == 'ingest') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'POST', path: path, body: j));
      response = {'id': 'task-1', 'status': 'pending', 'processor': 'client'};
    } else if (req.method == 'PATCH' && parts.length == 7) {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'PATCH', path: path, body: j));
      response = {
        'id': parts[6],
        'status': j['status'] ?? 'running',
        'processor': 'client',
      };
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
  late _FakeIngest fake;
  late DocprocTaskMirror mirror;

  setUp(() async {
    fake = await _FakeIngest.start();
    mirror = DocprocTaskMirror(
      client: WikiClient(fake.url, 'tok'),
      projectId: 'p1',
      progressInterval: const Duration(milliseconds: 500),
    );
  });

  tearDown(() => fake.stop());

  List<Map<String, dynamic>> patches() => fake.seen
      .where((r) => r.method == 'PATCH')
      .map((r) => r.body)
      .toList();

  test('start：建 processor=client 任务 + PATCH running', () async {
    await mirror.start(sourceId: 'src-1', title: 'a.pdf');
    expect(mirror.taskId, 'task-1');

    final post = fake.seen.first;
    expect(post.method, 'POST');
    expect(post.body['processor'], 'client');
    expect(post.body['source_id'], 'src-1');

    expect(patches().single, {'status': 'running'});
  });

  test('progress 节流：interval 内只发第一次，发完整 {phase, percent}', () async {
    await mirror.start(sourceId: 'src-1', title: 'a.pdf');
    fake.seen.clear();

    final t0 = DateTime(2026, 8, 31, 12);
    await mirror.progress('load', 10, now: t0);
    await mirror.progress('extract', 40, now: t0.add(const Duration(milliseconds: 100)));
    await mirror.progress('extract', 80, now: t0.add(const Duration(milliseconds: 600)));

    expect(patches(), [
      {'progress': {'phase': 'load', 'percent': 10}},
      {'progress': {'phase': 'extract', 'percent': 80}},
    ]);
  });

  test('done / failed / cancelled 发对应终态；终态后 progress 不再发', () async {
    await mirror.start(sourceId: 'src-1', title: 'a.pdf');
    await mirror.done();
    await mirror.progress('extract', 99);
    expect(patches().last, {'status': 'done'});
    expect(patches().where((b) => b.containsKey('progress')), isEmpty);

    final m2 = DocprocTaskMirror(
      client: WikiClient(fake.url, 'tok'),
      projectId: 'p1',
    );
    await m2.start(sourceId: 'src-2', title: 'b.pdf');
    await m2.failed(StateError('PDF 已加密'));
    expect(patches().last['status'], 'failed');
    expect(patches().last['error'], contains('PDF 已加密'));

    final m3 = DocprocTaskMirror(
      client: WikiClient(fake.url, 'tok'),
      projectId: 'p1',
    );
    await m3.start(sourceId: 'src-3', title: 'c.pdf');
    await m3.cancelled();
    expect(patches().last, {'status': 'cancelled'});
  });

  test('建任务失败：taskId=null，后续 PATCH 全 no-op 且不抛', () async {
    fake.failAll = true;
    await mirror.start(sourceId: 'src-1', title: 'a.pdf');
    expect(mirror.taskId, isNull);
    // 不抛异常即通过。
    await mirror.progress('load', 10);
    await mirror.done();
    await mirror.failed(StateError('x'));
    await mirror.cancelled();
  });

  test('PATCH 失败（服务端 500）吞掉不抛', () async {
    await mirror.start(sourceId: 'src-1', title: 'a.pdf');
    fake.failAll = true;
    await mirror.progress('load', 10);
    await mirror.done();
  });
}
