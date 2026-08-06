// CodeBridgeClient + code 帧 Dart 镜像的单测。
//
// 用内存 FakeCodeTransport 验证:
//   - code_request 上行 wire 格式与 Go 对端一致(snake_case tag)
//   - code_response 按 request_id 关联回 Completer
//   - code_pty_chunk base64 字节解码正确
//   - 便捷封装 gitStatus/fsRead/openPty 解析结果
//
// 真实跨进程 e2e(Dart 连真 biu serve)在 M0 验证脚本里单独跑;此处只锁协议契约。

import 'dart:async';
import 'dart:convert';

import 'package:biumind/data/api/sdkproto/v1/code.dart';
import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_test/flutter_test.dart';

class FakeCodeTransport implements CodeTransport {
  final List<String> sent = [];
  final _ctrl = StreamController<dynamic>.broadcast();
  bool closed = false;

  @override
  Stream<dynamic> get frames => _ctrl.stream;

  @override
  void send(String data) => sent.add(data);

  @override
  Future<void> close() async {
    closed = true;
    if (!_ctrl.isClosed) await _ctrl.close();
  }

  /// 模拟服务端推一条原始帧。
  void serverPush(Map<String, dynamic> frame) => _ctrl.add(jsonEncode(frame));

  Map<String, dynamic> lastSentJson() =>
      jsonDecode(sent.last) as Map<String, dynamic>;
}

void main() {
  group('code frame codec', () {
    test('CodeRequest round-trips with snake_case wire tags', () {
      final req = CodeRequest(
          requestId: 'r1', method: 'git.status', params: {'cwd': '/tmp'});
      final json = req.toJson();
      expect(json['type'], 'code_request');
      expect(json['request_id'], 'r1');
      expect(json['method'], 'git.status');
      final back = CodeFrame.fromJson(json) as CodeRequest;
      expect(back.requestId, 'r1');
      expect(back.params?['cwd'], '/tmp');
    });

    test('CodePtyChunk decodes base64 data to bytes', () {
      final json = {
        'type': 'code_pty_chunk',
        'pty_id': 'p1',
        'data': base64Encode(utf8.encode('héllo')),
      };
      final chunk = CodeFrame.fromJson(json) as CodePtyChunk;
      expect(chunk.ptyId, 'p1');
      expect(utf8.decode(chunk.data), 'héllo');
      // re-encode 回 base64 不丢
      expect(chunk.toJson()['data'], base64Encode(utf8.encode('héllo')));
    });

    test('CodePtyInput encodes bytes as base64', () {
      final input = CodePtyInput(ptyId: 'p1', data: utf8.encode('ls\n'));
      final json = input.toJson();
      expect(json['type'], 'code_pty_input');
      expect(json['data'], base64Encode(utf8.encode('ls\n')));
    });
  });

  group('CodeBridgeClient', () {
    test('request correlates response by request_id', () async {
      final fake = FakeCodeTransport();
      final client =
          CodeBridgeClient(bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
      await client.connect();

      final fut = client.gitStatus('/tmp/repo');
      // 上行帧检查
      final sent = fake.lastSentJson();
      expect(sent['type'], 'code_request');
      expect(sent['method'], 'git.status');
      expect(sent['params']['cwd'], '/tmp/repo');
      final reqId = sent['request_id'] as String;

      // 服务端按同 id 回响应
      fake.serverPush({
        'type': 'code_response',
        'request_id': reqId,
        'ok': true,
        'result': {'branch': 'main', 'clean': true},
      });
      final result = await fut;
      expect(result['branch'], 'main');
      expect(result['clean'], true);

      await client.close();
    });

    test('failed response surfaces as CodeBridgeException', () async {
      final fake = FakeCodeTransport();
      final client =
          CodeBridgeClient(bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
      await client.connect();

      final fut = client.fsRead('/nope');
      final reqId = fake.lastSentJson()['request_id'] as String;
      fake.serverPush({
        'type': 'code_response',
        'request_id': reqId,
        'ok': false,
        'error': 'no such file',
      });
      await expectLater(fut, throwsA(isA<CodeBridgeException>()));

      await client.close();
    });

    test('pty chunks stream through with decoded bytes', () async {
      final fake = FakeCodeTransport();
      final client =
          CodeBridgeClient(bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
      await client.connect();

      final got = <String>[];
      final sub = client.ptyChunks.listen((c) => got.add(utf8.decode(c.data)));

      fake.serverPush({
        'type': 'code_pty_chunk',
        'pty_id': 'p1',
        'data': base64Encode(utf8.encode('echo-out')),
      });
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(got, ['echo-out']);

      await sub.cancel();
      await client.close();
    });

    test('openPty returns server-assigned pty_id', () async {
      final fake = FakeCodeTransport();
      final client =
          CodeBridgeClient(bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
      await client.connect();

      final fut = client.openPty(cmd: 'cat');
      final reqId = fake.lastSentJson()['request_id'] as String;
      expect(fake.lastSentJson()['method'], 'pty.open');
      fake.serverPush({
        'type': 'code_response',
        'request_id': reqId,
        'ok': true,
        'result': {'pty_id': 'pty-abc'},
      });
      expect(await fut, 'pty-abc');

      await client.close();
    });

    test('sendInput/resize emit correct frames', () async {
      final fake = FakeCodeTransport();
      final client =
          CodeBridgeClient(bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
      await client.connect();

      client.sendInput('p1', utf8.encode('hi'));
      var sent = fake.lastSentJson();
      expect(sent['type'], 'code_pty_input');
      expect(sent['pty_id'], 'p1');
      expect(sent['data'], base64Encode(utf8.encode('hi')));

      client.resize('p1', 120, 40);
      sent = fake.lastSentJson();
      expect(sent['type'], 'code_pty_resize');
      expect(sent['cols'], 120);
      expect(sent['rows'], 40);

      await client.close();
    });
  });
}
