// auditTeeConnector —— 权限审计行 WS 旁路 tee 的单测（设计 §7-4 客户端半）。
//
// 覆盖：
//   - 下行帧原样透传（含顺序）
//   - local_command_output 帧 tee 出 (uuid, content) 回调
//   - 其他帧不触发回调；坏 JSON 静默吞掉不中断流
//   - send / close 透传 inner

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/biu_client.dart';
import 'package:biumind/features/chat/data/permission_audit_tap.dart';

class _FakeInner implements BiuTransport {
  final controller = StreamController<dynamic>();
  final sent = <String>[];
  var closed = false;

  @override
  Stream<dynamic> get frames => controller.stream;

  @override
  void send(String data) => sent.add(data);

  @override
  Future<void> close() async {
    closed = true;
    await controller.close();
  }
}

void main() {
  test('下行帧透传 + local_command_output tee 出回调', () async {
    final inner = _FakeInner();
    final lines = <(String, String)>[];
    final connector = auditTeeConnector(
      inner: (_) => inner,
      onLocalCommandOutput: (uuid, content) => lines.add((uuid, content)),
    );
    final transport = connector(Uri.parse('ws://example/ws'));

    final received = <dynamic>[];
    final sub = transport.frames.listen(received.add);

    const auditJson =
        '{"type":"system","subtype":"local_command_output","content":"[auto] Bash approved without prompting","uuid":"u-1","session_id":"s-1"}';
    inner.controller.add('{"type":"streamlined_text","text":"hi"}');
    inner.controller.add(auditJson);
    // 坏 JSON 命中预筛:静默吞掉,不中断后续帧。
    inner.controller.add('{"type":"system","subtype":"local_command_output",bad');
    inner.controller.add('{"type":"result","subtype":"success"}');
    await Future.delayed(Duration.zero);

    expect(received, hasLength(4), reason: '坏帧也透传给 BiuClient(它有自身容错)');
    expect(lines, [
      ('u-1', '[auto] Bash approved without prompting'),
    ]);

    transport.send('{"x":1}');
    expect(inner.sent, ['{"x":1}']);
    await transport.close();
    expect(inner.closed, isTrue);
    await sub.cancel();
  });
}
