// ShellTerminalController 单测 —— 每项目多 shell 的 open/close/label/state。
// 用内存 FakeCodeTransport 驱动真 CodeBridgeClient(不起真 daemon)。

import 'dart:async';
import 'dart:convert';

import 'package:biumind/features/code/application/shell_terminal_controller.dart';
import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:flutter_test/flutter_test.dart';

class FakeCodeTransport implements CodeTransport {
  final List<String> sent = [];
  final _ctrl = StreamController<dynamic>.broadcast();

  @override
  Stream<dynamic> get frames => _ctrl.stream;
  @override
  void send(String data) {
    sent.add(data);
    // 自动应答 pty.open / pty.kill,让 controller 的 await 完成。
    final j = jsonDecode(data) as Map<String, dynamic>;
    if (j['type'] == 'code_request') {
      final method = j['method'] as String;
      final id = j['request_id'] as String;
      final result = method == 'pty.open'
          ? {'pty_id': 'srv-${sent.length}'}
          : const <String, dynamic>{};
      scheduleMicrotask(() => _ctrl.add(jsonEncode({
            'type': 'code_response',
            'request_id': id,
            'ok': true,
            'result': result,
          })));
    }
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }

  List<Map<String, dynamic>> requestsFor(String method) => sent
      .map((s) => jsonDecode(s) as Map<String, dynamic>)
      .where((j) => j['type'] == 'code_request' && j['method'] == method)
      .toList();
}

Future<(ShellTerminalController, FakeCodeTransport)> _make() async {
  final fake = FakeCodeTransport();
  final client = CodeBridgeClient(
      bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
  await client.connect();
  return (ShellTerminalController(client), fake);
}

void main() {
  group('ShellTerminalController', () {
    test('open twice → two sessions labeled Shell 1 / Shell 2', () async {
      final (ctl, fake) = await _make();
      final s1 = await ctl.open('proj-a', '/work/a');
      final s2 = await ctl.open('proj-a', '/work/a');

      expect(s1!.label, 'Shell 1');
      expect(s2!.label, 'Shell 2');
      expect(ctl.shellsFor('proj-a').length, 2);

      // pty.open 上行帧带 $SHELL -l + cwd。
      final opens = fake.requestsFor('pty.open');
      expect(opens.length, 2);
      expect(opens.first['params']['args'], ['-l']);
      expect(opens.first['params']['cwd'], '/work/a');
      // server 分配的 pty_id 被记下。
      expect(s1.ptyId, isNotEmpty);
      expect(s1.ptyId, isNot(s2.ptyId));
    });

    test('shells are scoped per project', () async {
      final (ctl, _) = await _make();
      await ctl.open('proj-a', '/work/a');
      await ctl.open('proj-b', '/work/b');
      expect(ctl.shellsFor('proj-a').length, 1);
      expect(ctl.shellsFor('proj-b').length, 1);
    });

    test('close kills the PTY and removes the session', () async {
      final (ctl, fake) = await _make();
      final s1 = await ctl.open('proj-a', '/work/a');
      await ctl.open('proj-a', '/work/a');

      await ctl.close('proj-a', s1!.id);
      expect(ctl.shellsFor('proj-a').length, 1);
      expect(ctl.shellsFor('proj-a').first.label, 'Shell 2');

      // 发了 pty.kill,且 pty_id 是被关那个 shell 的。
      final kills = fake.requestsFor('pty.kill');
      expect(kills.length, 1);
      expect(kills.first['params']['pty_id'], s1.ptyId);
    });

    test('null client (daemon 未就绪) → open returns null, no session', () async {
      final ctl = ShellTerminalController(null);
      final s = await ctl.open('proj-a', '/work/a');
      expect(s, isNull);
      expect(ctl.shellsFor('proj-a'), isEmpty);
    });
  });
}
