// RealtimeHub 单测 — 覆盖 v2-3 last_event_id 续传行为.
//
// 用 MockClient 模拟 SSE chunked stream, 不起真 http server. 重点验证:
//   1. 收到帧 id 后 _lastEventId 更新
//   2. reconnect (token expired / 网络抖) 时 last-event-id header 回带

import 'dart:async';
import 'dart:convert';

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart' show MockClient, MockClientHandler;

import 'package:biumind/data/sse/realtime_hub.dart';

/// _StreamingMockClient — http.testing 的 MockClient 不支持 streamed response,
/// 自己写一个最小 client 让 send() 返流式 stream.
class _StreamingMockClient extends http.BaseClient {
  _StreamingMockClient(this._handler);
  final Future<http.StreamedResponse> Function(http.BaseRequest req) _handler;

  final List<http.BaseRequest> requests = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    requests.add(request);
    return _handler(request);
  }
}

http.StreamedResponse _sseResponse(Stream<List<int>> chunks) {
  return http.StreamedResponse(
    chunks, 200,
    headers: {'content-type': 'text/event-stream'},
  );
}

/// _frame — 拼一帧 SSE wire.
String _frame(String id, String topic, String kind, Map<String, dynamic> payload) {
  final data = jsonEncode({'topic': topic, 'kind': kind, 'payload': payload});
  return 'id: $id\nevent: message\ndata: $data\n\n';
}

void main() {
  group('RealtimeHub last_event_id', () {
    test('收到帧后 _lastEventId 更新', () async {
      final ctrl = StreamController<List<int>>();
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () => _StreamingMockClient((req) async {
          return _sseResponse(ctrl.stream);
        }),
      );

      // 启 stream + 推 1 帧
      final sub = hub.subscribe('t1');
      final frames = <RealtimeFrame>[];
      final fSub = sub.listen(frames.add);
      // wait connect kicked off
      await Future<void>.delayed(const Duration(milliseconds: 50));

      ctrl.add(utf8.encode(_frame('01H', 't1', 'msg', {'v': 1})));
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(frames, hasLength(1));
      expect(frames.first.id, '01H');
      expect(hub.debugLastEventId, '01H');

      ctrl.add(utf8.encode(_frame('02H', 't1', 'msg', {'v': 2})));
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(hub.debugLastEventId, '02H');

      await fSub.cancel();
      await ctrl.close();
      await hub.dispose();
    });

    test('reconnect 时回带 last-event-id header', () async {
      final firstChunks = StreamController<List<int>>();
      final secondChunks = StreamController<List<int>>();
      var connectCount = 0;

      late _StreamingMockClient mockClient;
      mockClient = _StreamingMockClient((req) async {
        connectCount++;
        if (connectCount == 1) {
          return _sseResponse(firstChunks.stream);
        }
        return _sseResponse(secondChunks.stream);
      });

      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
          initialBackoff: const Duration(milliseconds: 10),
          maxBackoff: const Duration(milliseconds: 10),
        ),
        clientFactory: () => mockClient,
      );

      final sub = hub.subscribe('t1');
      final fSub = sub.listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // 第一连接收一帧
      firstChunks.add(utf8.encode(_frame('A1', 't1', 'msg', {'k': 1})));
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(hub.debugLastEventId, 'A1');

      // 第一次连接的 request 不带 last-event-id
      expect(mockClient.requests[0].headers['last-event-id'], isNull);

      // 模拟服务器关连接 → 触发 reconnect
      await firstChunks.close();
      await Future<void>.delayed(const Duration(milliseconds: 100));

      // 应该至少发起第二次连接, 且带 last-event-id: A1
      expect(mockClient.requests.length, greaterThanOrEqualTo(2));
      final secondReq = mockClient.requests[1];
      expect(secondReq.headers['last-event-id'], 'A1');

      await secondChunks.close();
      await fSub.cancel();
      await hub.dispose();
    });

    test('初次连接没有 _lastEventId 时不带 header', () async {
      final ctrl = StreamController<List<int>>();
      late _StreamingMockClient mockClient;
      mockClient = _StreamingMockClient((req) async => _sseResponse(ctrl.stream));

      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () => mockClient,
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(mockClient.requests, hasLength(1));
      expect(mockClient.requests.first.headers['last-event-id'], isNull);

      await ctrl.close();
      await hub.dispose();
    });

    test('id 缺失的帧不污染 _lastEventId', () async {
      final ctrl = StreamController<List<int>>();
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () => _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // 一帧含 id, 一帧不含
      ctrl.add(utf8.encode(_frame('Z1', 't1', 'msg', {'a': 1})));
      await Future<void>.delayed(const Duration(milliseconds: 30));
      ctrl.add(utf8.encode(
          'event: message\ndata: ${jsonEncode({'topic': 't1', 'kind': 'msg', 'payload': {'a': 2}})}\n\n'));
      await Future<void>.delayed(const Duration(milliseconds: 30));

      // 第二帧没 id → _lastEventId 应保持 Z1 (不被覆盖成 null)
      expect(hub.debugLastEventId, 'Z1');

      await ctrl.close();
      await hub.dispose();
    });
  });

  group('RealtimeHub v2-4 持久化重启续传', () {
    test('首次 connect 前 await load 注入 cursor + 带 header', () async {
      final ctrl = StreamController<List<int>>();
      late _StreamingMockClient mockClient;
      mockClient =
          _StreamingMockClient((_) async => _sseResponse(ctrl.stream));

      var loadCalls = 0;
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () => mockClient,
        loadLastEventId: () async {
          loadCalls++;
          return 'PERSISTED-99';
        },
        saveLastEventId: (_) async {},
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(loadCalls, 1);
      expect(mockClient.requests, hasLength(1));
      expect(mockClient.requests.first.headers['last-event-id'], 'PERSISTED-99');
      expect(hub.debugLastEventId, 'PERSISTED-99');

      await ctrl.close();
      await hub.dispose();
    });

    test('load 返 null (第一次启动) — 不带 header, 不阻塞', () async {
      final ctrl = StreamController<List<int>>();
      late _StreamingMockClient mockClient;
      mockClient =
          _StreamingMockClient((_) async => _sseResponse(ctrl.stream));

      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () => mockClient,
        loadLastEventId: () async => null,
        saveLastEventId: (_) async {},
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(mockClient.requests.first.headers['last-event-id'], isNull);
      expect(hub.debugLastEventId, isNull);

      await ctrl.close();
      await hub.dispose();
    });

    test('收帧后 debounce 100ms 触发 save', () async {
      final ctrl = StreamController<List<int>>();
      final saved = <String>[];
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () =>
            _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
        loadLastEventId: () async => null,
        saveLastEventId: (id) async {
          saved.add(id);
        },
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // 连发 3 帧 (10ms 间隔), debounce 应只写一次最新
      ctrl.add(utf8.encode(_frame('S1', 't1', 'msg', {})));
      await Future<void>.delayed(const Duration(milliseconds: 10));
      ctrl.add(utf8.encode(_frame('S2', 't1', 'msg', {})));
      await Future<void>.delayed(const Duration(milliseconds: 10));
      ctrl.add(utf8.encode(_frame('S3', 't1', 'msg', {})));
      // 等过 debounce 窗口
      await Future<void>.delayed(const Duration(milliseconds: 200));

      expect(saved, ['S3'], reason: 'debounce 应合并 3 帧只写最新 cursor');

      await ctrl.close();
      await hub.dispose();
    });

    test('reconnect 不重复 load (in-memory 比 dao 新)', () async {
      final firstChunks = StreamController<List<int>>();
      final secondChunks = StreamController<List<int>>();
      var connectCount = 0;
      final mockClient = _StreamingMockClient((_) async {
        connectCount++;
        if (connectCount == 1) return _sseResponse(firstChunks.stream);
        return _sseResponse(secondChunks.stream);
      });

      var loadCalls = 0;
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
          initialBackoff: const Duration(milliseconds: 10),
          maxBackoff: const Duration(milliseconds: 10),
        ),
        clientFactory: () => mockClient,
        loadLastEventId: () async {
          loadCalls++;
          return 'OLD-1';
        },
        saveLastEventId: (_) async {},
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // 第一次连接收一帧 NEW-9 (比 dao 里的 OLD-1 新)
      firstChunks.add(utf8.encode(_frame('NEW-9', 't1', 'msg', {})));
      await Future<void>.delayed(const Duration(milliseconds: 30));

      await firstChunks.close();
      await Future<void>.delayed(const Duration(milliseconds: 100));

      expect(loadCalls, 1, reason: 'reconnect 不应重复调 load');
      expect(mockClient.requests.length, greaterThanOrEqualTo(2));
      // reconnect 时回带最新 in-memory id (NEW-9), 而不是过期的 OLD-1
      expect(mockClient.requests[1].headers['last-event-id'], 'NEW-9');

      await secondChunks.close();
      await hub.dispose();
    });

    test('dispose 时 flush 未落盘 cursor', () async {
      final ctrl = StreamController<List<int>>();
      final saved = <String>[];
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () =>
            _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
        loadLastEventId: () async => null,
        saveLastEventId: (id) async {
          saved.add(id);
        },
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // 帧到, debounce 100ms 还没过, 立即 dispose 应 flush
      ctrl.add(utf8.encode(_frame('FLUSH-1', 't1', 'msg', {})));
      await Future<void>.delayed(const Duration(milliseconds: 20));
      await ctrl.close();
      await hub.dispose();

      expect(saved, ['FLUSH-1'], reason: 'dispose 路径应 flush 最新 cursor');
    });

    test('save 抛错不影响 hub', () async {
      final ctrl = StreamController<List<int>>();
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () =>
            _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
        loadLastEventId: () async => null,
        saveLastEventId: (_) async => throw Exception('disk full'),
      );

      final frames = <RealtimeFrame>[];
      hub.subscribe('t1').listen(frames.add);
      await Future<void>.delayed(const Duration(milliseconds: 50));

      ctrl.add(utf8.encode(_frame('E1', 't1', 'msg', {'v': 1})));
      // 等过 debounce + 异步 catchError
      await Future<void>.delayed(const Duration(milliseconds: 200));

      // hub 仍然 dispatch 帧, _lastEventId 在 in-memory 仍更新
      expect(frames, hasLength(1));
      expect(hub.debugLastEventId, 'E1');

      await ctrl.close();
      await hub.dispose();
    });
  });

  group('RealtimeHub v2-6 desync 4009', () {
    test('收 system desync 帧 → 调 onDesync + 清 _lastEventId', () async {
      final ctrl = StreamController<List<int>>();
      final desyncs = <Map<String, dynamic>>[];
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () =>
            _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
        loadLastEventId: () async => 'OLD-CURSOR',
        saveLastEventId: (_) async {},
        onDesync: (code, reason) async {
          desyncs.add({'code': code, 'reason': reason});
        },
      );

      hub.subscribe('t1').listen((_) {});
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // load 注入了 OLD-CURSOR
      expect(hub.debugLastEventId, 'OLD-CURSOR');

      // 服务端发 desync (system topic + kind=desync)
      final desyncFrame = 'id: D1\nevent: message\ndata: '
          '${jsonEncode({
        'topic': 'system',
        'kind': 'desync',
        'payload': {'code': 4009, 'reason': 'last_event_id_beyond_retention'}
      })}\n\n';
      ctrl.add(utf8.encode(desyncFrame));
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(desyncs, hasLength(1));
      expect(desyncs.first['code'], 4009);
      expect(desyncs.first['reason'], 'last_event_id_beyond_retention');
      // 关键: desync 帧的 id 不污染 _lastEventId, 同时清掉 OLD-CURSOR
      expect(hub.debugLastEventId, isNull,
          reason: 'desync 后 _lastEventId 必须清, 否则下次重连还会触发 desync');

      await ctrl.close();
      await hub.dispose();
    });

    test('desync 帧广播到所有 topic 订阅者 (UI 可显示状态)', () async {
      // 多 topic = 多次 subscribe 触发 reconnect, 每次 connect 给独立 stream
      final streams = <StreamController<List<int>>>[];
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
          initialBackoff: const Duration(milliseconds: 5),
          maxBackoff: const Duration(milliseconds: 5),
        ),
        clientFactory: () => _StreamingMockClient((_) async {
          final s = StreamController<List<int>>();
          streams.add(s);
          return _sseResponse(s.stream);
        }),
        loadLastEventId: () async => null,
        saveLastEventId: (_) async {},
        onDesync: (_, __) async {},
      );

      final framesA = <RealtimeFrame>[];
      final framesB = <RealtimeFrame>[];
      hub.subscribe('t-a').listen(framesA.add);
      hub.subscribe('t-b').listen(framesB.add);
      // 等所有 reconnect 链路 settle
      await Future<void>.delayed(const Duration(milliseconds: 100));

      // 取最新一条 stream (subscribe('t-b') 触发的 reconnect 后的连接)
      final latest = streams.last;
      latest.add(utf8.encode('id: D1\nevent: message\ndata: '
          '${jsonEncode({
            'topic': 'system',
            'kind': 'desync',
            'payload': {'code': 4009, 'reason': 'gap'}
          })}\n\n'));
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(framesA.where((f) => f.kind == 'desync'), hasLength(1));
      expect(framesB.where((f) => f.kind == 'desync'), hasLength(1));

      for (final s in streams) {
        if (!s.isClosed) await s.close();
      }
      await hub.dispose();
    });

    test('onDesync 回调抛错不影响 hub 后续帧处理', () async {
      final ctrl = StreamController<List<int>>();
      final hub = RealtimeHub(
        RealtimeHubConfig(
          endpoint: Uri.parse('http://test/v1/realtime/stream'),
          auth: () async => 'tok',
        ),
        clientFactory: () =>
            _StreamingMockClient((_) async => _sseResponse(ctrl.stream)),
        loadLastEventId: () async => null,
        saveLastEventId: (_) async {},
        onDesync: (_, __) async => throw Exception('refetch failed'),
      );

      final frames = <RealtimeFrame>[];
      hub.subscribe('t1').listen(frames.add);
      await Future<void>.delayed(const Duration(milliseconds: 50));

      ctrl.add(utf8.encode('id: D1\nevent: message\ndata: '
          '${jsonEncode({
            'topic': 'system',
            'kind': 'desync',
            'payload': {'code': 4009, 'reason': 'gap'}
          })}\n\n'));
      await Future<void>.delayed(const Duration(milliseconds: 50));

      // hub 后续仍能 dispatch 普通帧
      ctrl.add(utf8.encode(_frame('OK1', 't1', 'msg', {'v': 1})));
      await Future<void>.delayed(const Duration(milliseconds: 50));

      expect(frames.where((f) => f.kind == 'msg').toList(), hasLength(1));
      expect(hub.debugLastEventId, 'OK1');

      await ctrl.close();
      await hub.dispose();
    });
  });

  // 标记保留 import (避免 lint warning)
  test('mock client smoke', () {
    expect(MockClient((_) async => http.Response('', 200)), isNotNull);
    expect(MockClientHandler, isNotNull);
  });
}
