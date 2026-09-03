// DocprocQueue 测试（P2 W1，设计文档 §3.5）：
//   * 本机全流程：占位 source → 镜像任务 → parse → upsert + PATCH done；
//   * parse 失败回退云端；云端也失败 → failed；
//   * cancel：parsing → 引擎 cancel + PATCH cancelled；queued → 直接移除；
//   * retry：failed/cancelled 重新入队；
//   * 背压：parsing/uploading 总字节超上限时 queued 等待；
//   * 并发上限 = 流水线重叠（A 解析时 B 可上传），parse 本身串行；
//   * 格式 gate：docproc-web 不支持的格式跳过本机直接云端（无空转/镜像闪烁）；
//   * 未配置后端 → failed。
//
// fake HttpServer + fake DocprocEngine + fake CloudUploadFn，不碰真 WebView。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:biumind/core/docproc/docproc_bridge_controller.dart';
import 'package:biumind/core/docproc/docproc_bridge_protocol.dart';
import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/data/api/wiki_client.dart';
import 'package:biumind/features/wiki/application/docproc_preferences.dart';
import 'package:biumind/features/wiki/data/docproc_queue_controller.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

// ─── fakes ─────────────────────────────────────────────────

class _FakeBrain {
  _FakeBrain._(this.server);
  final HttpServer server;
  final List<({String method, String path, Map<String, dynamic> body})> seen =
      [];
  int _nextSrc = 0;

  static Future<_FakeBrain> start() async {
    final s = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final fake = _FakeBrain._(s);
    s.listen(fake._handle);
    return fake;
  }

  Uri get url => Uri.parse('http://${server.address.host}:${server.port}');

  Future<void> stop() => server.close(force: true);

  List<Map<String, dynamic>> bodies(String method, String contains) => seen
      .where((r) => r.method == method && r.path.contains(contains))
      .map((r) => r.body)
      .toList();

  Future<void> _handle(HttpRequest req) async {
    final body = await utf8.decoder.bind(req).join();
    final path = req.uri.path;
    final parts = path.split('/').where((s) => s.isNotEmpty).toList();
    Map<String, dynamic>? response;
    var status = 200;

    if (req.method == 'POST' &&
        parts.length == 5 &&
        parts[4] == 'sources') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'POST', path: path, body: j));
      response = {
        'id': 'src-${++_nextSrc}',
        'project_id': parts[3],
        'rel_path': j['rel_path'],
        'filename': j['filename'] ?? '',
        'byte_size': j['byte_size'] ?? 0,
        'parse_status': j['parse_status'] ?? 'queued',
        'created_at': DateTime.now().toUtc().toIso8601String(),
        'updated_at': DateTime.now().toUtc().toIso8601String(),
      };
    } else if (req.method == 'POST' &&
        parts.length == 5 &&
        parts[4] == 'ingest') {
      final j = jsonDecode(body) as Map<String, dynamic>;
      seen.add((method: 'POST', path: path, body: j));
      response = {
        'id': 'task-${j['source_id']}',
        'status': 'pending',
        'processor': j['processor'] ?? 'server',
      };
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
      response = {'error': 'unknown_route', 'path': path};
    }

    req.response.statusCode = status;
    req.response.headers.contentType = ContentType.json;
    req.response.write(jsonEncode(response));
    await req.response.close();
  }
}

class _FakeEngine implements DocprocEngine {
  @override
  void Function(String id, String phase, int percent)? onProgress;

  /// requestId → 手动闸门：未 complete 前 parse 挂着（模拟在途解析）。
  final Map<String, Completer<DocprocResult>> gates = {};
  final List<String> parseCalls = [];
  final List<String> cancelCalls = [];
  Object? errorForNext;

  /// 非 null 时 parse 返回该文本（模拟空文本结果）。
  String? textOverride;

  @override
  Future<DocprocResult> parse({
    required String fileName,
    required Uint8List bytes,
    String? mimeHint,
    String? requestId,
  }) {
    final id = requestId!;
    parseCalls.add(id);
    final err = errorForNext;
    if (err != null) {
      errorForNext = null;
      return Future.error(err);
    }
    final gate = gates[id];
    if (gate != null) return gate.future;
    final override = textOverride;
    if (override != null) {
      textOverride = null;
      return Future.value(DocprocResult(
        text: override,
        format: 'txt',
        parserVersion: 'fake-engine@1',
      ));
    }
    return Future.value(DocprocResult(
      text: 'text of $fileName',
      format: 'txt',
      parserVersion: 'fake-engine@1',
    ));
  }

  @override
  void cancel(String requestId) {
    cancelCalls.add(requestId);
    final gate = gates.remove(requestId);
    if (gate != null && !gate.isCompleted) {
      gate.completeError(
        const DocprocException(code: 'cancelled', message: '已取消'),
      );
    }
  }
}

class _Harness {
  _Harness(this.brain, this.engine, this.queue, this.cloudCalls);

  final _FakeBrain brain;
  final _FakeEngine engine;
  final DocprocQueue queue;
  final List<String> cloudCalls;

  static Future<_Harness> create({
    bool withWiki = true,
    int maxBytes = 200 * 1024 * 1024,
    int concurrency = 3,
    DocprocProcessLocation location = DocprocProcessLocation.auto,
  }) async {
    final brain = await _FakeBrain.start();
    final engine = _FakeEngine();
    final cloudCalls = <String>[];
    final caps = PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: true,
      hasRepoAppRunner: false,
      hasLocalDocproc: true,
      docprocQueueMaxBytes: maxBytes,
      docprocQueueConcurrency: concurrency,
    );
    final deps = DocprocQueueDeps(
      caps: caps,
      engine: engine,
      location: location,
      wikiClient: withWiki ? WikiClient(brain.url, 'tok') : null,
      uploadToCloud: ({
        required projectId,
        required filename,
        required bytes,
        required contentType,
        externalId,
      }) async {
        cloudCalls.add(filename);
        return (fileId: 'file-$filename', mimeType: contentType, sizeBytes: bytes.length);
      },
    );
    return _Harness(brain, engine, DocprocQueue(resolveDeps: () => deps), cloudCalls);
  }

  Future<void> dispose() => brain.stop();
}

DocprocQueueItem item(
  String id, {
  int byteSize = 10,
  String? filename,
  String? mime,
}) =>
    DocprocQueueItem(
      id: id,
      projectId: 'p1',
      filename: filename ?? '$id.txt',
      bytes: Uint8List(byteSize),
      mime: mime ?? 'text/plain',
    );

Future<void> pump([int rounds = 10]) async {
  for (var i = 0; i < rounds; i++) {
    await Future<void>.delayed(const Duration(milliseconds: 2));
  }
}

Future<DocprocQueueItem> settle(DocprocQueue q, String id) async {
  final deadline = DateTime.now().add(const Duration(seconds: 5));
  while (DateTime.now().isBefore(deadline)) {
    final it = q.itemById(id);
    if (it == null) throw StateError('item $id 被移除');
    if (it.status == DocprocItemStatus.done ||
        it.status == DocprocItemStatus.failed ||
        it.status == DocprocItemStatus.cancelled) {
      return it;
    }
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  throw TimeoutException('item $id 未到终态');
}

void main() {
  late _Harness h;
  tearDown(() => h.dispose());

  test('本机全流程：占位 → 镜像 → parse → upsert + PATCH done', () async {
    h = await _Harness.create();
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done);
    expect(it.mirrorTaskId, 'task-src-1');

    final sources = h.brain.bodies('POST', 'sources');
    expect(sources[0]['parse_status'], 'processing'); // 占位
    expect(sources[1]['raw_text'], 'text of a.txt'); // upsert
    expect(sources[1]['parse_status'], 'done');
    expect(sources[1]['parse_meta']['parser'], 'docproc-web');
    expect(sources[1]['content_hash'], isNotEmpty);

    final ingest = h.brain.bodies('POST', 'ingest').single;
    expect(ingest['processor'], 'client');
    expect(ingest['source_id'], 'src-1');

    final patches = h.brain.bodies('PATCH', 'tasks');
    expect(patches.first, {'status': 'running'});
    expect(patches.last, {'status': 'done'});
    expect(h.cloudCalls, isEmpty); // 未走云端
  });

  test('parse 失败：PATCH failed + 回退云端（createSource 带 file_id）', () async {
    h = await _Harness.create();
    h.engine.errorForNext =
        const DocprocException(code: 'corrupt', message: '坏文件');
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done);
    expect(h.cloudCalls, ['a.txt']);
    final patches = h.brain.bodies('PATCH', 'tasks');
    expect(patches.any((p) => p['status'] == 'failed'), isTrue);
    final sources = h.brain.bodies('POST', 'sources');
    expect(sources.last['file_id'], 'file-a.txt');
  });

  test('空文本守卫：引擎返回空 → 不上传 done 空文本，回退云端', () async {
    // 回归 2026-09-01 线上事故：解析器空文本成功返回 → 客户端标
    // parse_status=done 上传空 source → ingest 永远 "no content"。
    h = await _Harness.create();
    h.engine.textOverride = '   ';
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done); // 云端兜底成功
    expect(h.cloudCalls, ['a.txt']);
    final sources = h.brain.bodies('POST', 'sources');
    // 只有占位 + 云端 file_id upsert，绝不存在 done+空文本的中间态。
    expect(sources.length, 2);
    expect(sources[0]['parse_status'], 'processing');
    expect(sources[1]['file_id'], 'file-a.txt');
    expect(sources.any((s) => s['parse_status'] == 'done'), isFalse);
    final patches = h.brain.bodies('PATCH', 'tasks');
    expect(patches.any((p) => p['status'] == 'failed'), isTrue);
  });

  test('parse 失败 + 云端也失败 → failed + error', () async {
    h = await _Harness.create();
    h.engine.errorForNext =
        const DocprocException(code: 'corrupt', message: '坏文件');
    // 覆盖 deps：云端上传也抛错
    final brain = h.brain;
    final engine = h.engine;
    final deps = DocprocQueueDeps(
      caps: PlatformCaps(
        hasLocalPty: true,
        hasFileSystem: true,
        hasNotifications: true,
        supportsBackgroundIsolates: true,
        hasPersistentSqlite: true,
        hasEmbeddedWebView: true,
        hasRepoAppRunner: false,
        hasLocalDocproc: true,
      ),
      engine: engine,
      wikiClient: WikiClient(brain.url, 'tok'),
      uploadToCloud: ({
        required projectId,
        required filename,
        required bytes,
        required contentType,
        externalId,
      }) =>
          throw Exception('网络断开'),
    );
    h = _Harness(brain, engine, DocprocQueue(resolveDeps: () => deps), []);
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.failed);
    expect(it.error, contains('网络断开'));
  });

  test('cancel parsing：引擎 cancel + PATCH cancelled，不回退云端', () async {
    h = await _Harness.create();
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([item('a')]);
    await pump();
    expect(h.queue.itemById('a')!.status, DocprocItemStatus.parsing);

    h.queue.cancel('a');
    final it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.cancelled);
    expect(h.engine.cancelCalls, ['a']);
    expect(h.cloudCalls, isEmpty);
    final patches = h.brain.bodies('PATCH', 'tasks');
    expect(patches.last, {'status': 'cancelled'});
  });

  test('cancel queued：直接移除（镜像任务未建，无 PATCH）', () async {
    h = await _Harness.create(concurrency: 1);
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([item('a'), item('b')]);
    await pump();
    expect(h.queue.itemById('b')!.status, DocprocItemStatus.queued);

    h.queue.cancel('b');
    expect(h.queue.itemById('b'), isNull);

    // 收尾：放行 a
    h.engine.gates.remove('a')!.complete(
          const DocprocResult(
              text: 'x', format: 'txt', parserVersion: 'fake'),
        );
    await settle(h.queue, 'a');
  });

  test('retry：failed 重新入队并成功', () async {
    h = await _Harness.create();
    h.engine.errorForNext =
        const DocprocException(code: 'corrupt', message: '坏文件');
    // 云端也不可用 → failed
    final brain = h.brain;
    final engine = h.engine;
    var cloudOk = false;
    final deps0 = DocprocQueueDeps(
      caps: PlatformCaps(
        hasLocalPty: true,
        hasFileSystem: true,
        hasNotifications: true,
        supportsBackgroundIsolates: true,
        hasPersistentSqlite: true,
        hasEmbeddedWebView: true,
        hasRepoAppRunner: false,
        hasLocalDocproc: true,
      ),
      engine: engine,
      wikiClient: WikiClient(brain.url, 'tok'),
      uploadToCloud: ({
        required projectId,
        required filename,
        required bytes,
        required contentType,
        externalId,
      }) async {
        if (!cloudOk) throw Exception('网络断开');
        return (fileId: 'f1', mimeType: contentType, sizeBytes: bytes.length);
      },
    );
    h = _Harness(brain, engine, DocprocQueue(resolveDeps: () => deps0), []);
    h.queue.enqueue([item('a')]);
    var it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.failed);

    // 恢复云端后 retry
    cloudOk = true;
    h.queue.retry('a');
    it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.done);
    expect(it.error, isNull);
  });

  test('背压：未完成总字节超上限时 queued 等待，窗口释放后启动', () async {
    h = await _Harness.create(maxBytes: 100);
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([item('a', byteSize: 80), item('b', byteSize: 80)]);
    await pump();

    // a 在解析（80B），b 80B 进不了窗口（80+80 > 100）→ 留在 queued
    expect(h.queue.itemById('a')!.status, DocprocItemStatus.parsing);
    expect(h.queue.itemById('b')!.status, DocprocItemStatus.queued);

    h.engine.gates.remove('a')!.complete(
          const DocprocResult(
              text: 'x', format: 'txt', parserVersion: 'fake'),
        );
    await settle(h.queue, 'a');
    final b = await settle(h.queue, 'b');
    expect(b.status, DocprocItemStatus.done);
  });

  test('并发上限 = 流水线重叠：A 解析时 B 可完成云端上传', () async {
    h = await _Harness.create(concurrency: 3);
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([
      item('a'), // 本机解析（挂闸门）
      item('b', byteSize: 60 * 1024 * 1024), // 超 auto 桌面 50MB → 云端
    ]);
    // B 应该在 A 还挂着的时候就完成（重叠），不用等 A 的闸门放行
    final b = await settle(h.queue, 'b');
    expect(b.status, DocprocItemStatus.done);
    expect(h.queue.itemById('a')!.status, DocprocItemStatus.parsing);

    h.engine.gates.remove('a')!.complete(
          const DocprocResult(
              text: 'x', format: 'txt', parserVersion: 'fake'),
        );
    await settle(h.queue, 'a');
  });

  test('并发上限 1（移动档）：B 等 A 终态后才启动', () async {
    h = await _Harness.create(concurrency: 1);
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([item('a'), item('b', byteSize: 60 * 1024 * 1024)]);
    await pump();
    expect(h.queue.itemById('a')!.status, DocprocItemStatus.parsing);
    expect(h.queue.itemById('b')!.status, DocprocItemStatus.queued);

    h.engine.gates.remove('a')!.complete(
          const DocprocResult(
              text: 'x', format: 'txt', parserVersion: 'fake'),
        );
    await settle(h.queue, 'a');
    final b = await settle(h.queue, 'b');
    expect(b.status, DocprocItemStatus.done);
  });

  test('未配置后端凭证：item 直接 failed', () async {
    h = await _Harness.create(withWiki: false);
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.failed);
    expect(it.error, contains('未配置后端凭证'));
  });

  test('W3 设置优先云端：小文件也直接走云端，不调引擎', () async {
    h = await _Harness.create(location: DocprocProcessLocation.preferCloud);
    h.queue.enqueue([item('a')]);
    final it = await settle(h.queue, 'a');
    expect(it.status, DocprocItemStatus.done);
    expect(h.engine.parseCalls, isEmpty);
    expect(h.cloudCalls, ['a.txt']);
    final sources = h.brain.bodies('POST', 'sources');
    expect(sources.single['file_id'], 'file-a.txt');
  });

  test('W3 设置快照不追溯：入队时盖章 auto，item 保留该设置', () async {
    h = await _Harness.create();
    h.engine.gates['a'] = Completer<DocprocResult>();
    h.queue.enqueue([item('a'), item('b')]);
    await pump();
    // enqueue 时刻快照 auto（item.location 由队列盖章）。
    expect(h.queue.itemById('b')!.location, DocprocProcessLocation.auto);
    h.engine.gates.remove('a')!.complete(
          const DocprocResult(
              text: 'x', format: 'txt', parserVersion: 'fake'),
        );
    await settle(h.queue, 'a');
    final b = await settle(h.queue, 'b');
    expect(b.status, DocprocItemStatus.done);
    expect(h.engine.parseCalls, containsAll(['a', 'b'])); // 都走了本机
  });

  test('格式 gate：docproc-web 不支持的 xlsx 跳过本机直接云端'
      '（无 parse 空转、无 failed 镜像闪烁）', () async {
    h = await _Harness.create();
    h.queue.enqueue([
      item('a',
          filename: 'book.xlsx',
          mime: 'application/vnd.openxmlformats-officedocument'
              '.spreadsheetml.sheet'),
    ]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done);
    expect(h.engine.parseCalls, isEmpty); // 没空转本机 parse
    expect(h.cloudCalls, ['book.xlsx']);
    final sources = h.brain.bodies('POST', 'sources');
    expect(sources.single['file_id'], 'file-book.xlsx'); // 无占位 source
    expect(h.brain.bodies('PATCH', 'tasks'), isEmpty); // 无镜像任务
  });

  test('格式 gate 不改三态语义：preferLocal 对不支持格式同样转云端', () async {
    h = await _Harness.create(location: DocprocProcessLocation.preferLocal);
    h.queue.enqueue([
      item('a',
          filename: 'deck.pptx',
          mime: 'application/vnd.openxmlformats-officedocument'
              '.presentationml.presentation'),
    ]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done);
    expect(h.engine.parseCalls, isEmpty);
    expect(h.cloudCalls, ['deck.pptx']);
  });

  test('格式 gate：mime 兜底 —— 扩展名不认识但 mime 支持仍走本机', () async {
    h = await _Harness.create();
    h.queue.enqueue([item('a', filename: 'notes.weird', mime: 'text/markdown')]);
    final it = await settle(h.queue, 'a');

    expect(it.status, DocprocItemStatus.done);
    expect(h.engine.parseCalls, ['a']); // 与 detect.ts 的 mimeHint 兜底对齐
    expect(h.cloudCalls, isEmpty);
  });
}
