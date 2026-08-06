// BridgeTerminalAdapter 单测 —— claude/codex 经 bridge 在 PTY 里跑(D6)。
//
// 用内存 FakeCodeTransport 驱动真 CodeBridgeClient,验证 adapter:
//   - run() 发出正确的 code.runTask 上行帧(task_id/agent_type/permission_mode/prompt)
//   - code_pty_exit(code=0)  → TaskFinished(end_turn)
//   - code_pty_exit(code!=0) → TaskFinished(error, 带 code/stderr)
//   - client==null(daemon 未就绪) → 立即 TaskFinished(error) 而非静默挂起
//   - cancel() → 发 pty.kill 帧

import 'dart:async';
import 'dart:convert';

import 'package:biumind/features/code/agent/bridge_terminal_adapter.dart';
import 'package:biumind/features/code/data/code_bridge_client.dart';
import 'package:biumind/features/code/domain/code_task.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

class FakeCodeTransport implements CodeTransport {
  final List<String> sent = [];
  final _ctrl = StreamController<dynamic>.broadcast();

  @override
  Stream<dynamic> get frames => _ctrl.stream;

  @override
  void send(String data) => sent.add(data);

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }

  void serverPush(Map<String, dynamic> frame) => _ctrl.add(jsonEncode(frame));

  Map<String, dynamic> lastSentJson() =>
      jsonDecode(sent.last) as Map<String, dynamic>;

  /// 找最近一条指定 method 的 code_request,返回其 request_id(供回 response)。
  String requestIdFor(String method) {
    for (final raw in sent.reversed) {
      final j = jsonDecode(raw) as Map<String, dynamic>;
      if (j['type'] == 'code_request' && j['method'] == method) {
        return j['request_id'] as String;
      }
    }
    throw StateError('no code_request for $method in $sent');
  }
}

CodeTask _task({
  String id = 't1',
  AgentKind agent = AgentKind.claudeCode,
  PermissionMode mode = PermissionMode.autoEdit,
  String prompt = 'fix the bug',
}) =>
    CodeTask(
      id: id,
      title: 'fix',
      prompt: prompt,
      agent: agent,
      mode: mode,
      status: CodeTaskStatus.queued,
      events: const [],
      cost: const TaskCost(),
      createdAt: DateTime(2026, 6, 26),
    );

Future<(CodeBridgeClient, FakeCodeTransport)> _connected() async {
  final fake = FakeCodeTransport();
  final client = CodeBridgeClient(
      bridgeUrl: 'http://127.0.0.1:1', connector: (_) => fake);
  await client.connect();
  return (client, fake);
}

void main() {
  group('BridgeTerminalAdapter', () {
    test('run() emits code.runTask frame with task params', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'claude');

      final events = adapter.run(_task(prompt: 'do it')).toList();
      // 让 _drive 跑到发出 runTask 请求
      await Future<void>.delayed(const Duration(milliseconds: 10));

      final reqId = fake.requestIdFor('code.runTask');
      final params = fake.lastSentJson()['params'] as Map<String, dynamic>;
      expect(params['task_id'], 't1');
      expect(params['agent_type'], 'claude');
      expect(params['permission_mode'], 'auto_edit');
      expect(params['prompt'], 'do it');

      // 回 runTask 响应 + 进程退出,收尾让 stream 关闭
      fake.serverPush({
        'type': 'code_response',
        'request_id': reqId,
        'ok': true,
        'result': {'pty_id': 't1'},
      });
      fake.serverPush(
          {'type': 'code_pty_exit', 'pty_id': 't1', 'exit_code': 0});
      final list = await events;
      expect(list.whereType<TaskFinished>().single.reason, 'end_turn');

      await client.close();
    });

    test('pty_exit code=0 → TaskFinished(end_turn)', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'claude');
      final events = adapter.run(_task()).toList();
      await Future<void>.delayed(const Duration(milliseconds: 10));

      fake.serverPush({
        'type': 'code_response',
        'request_id': fake.requestIdFor('code.runTask'),
        'ok': true,
        'result': {'pty_id': 't1'},
      });
      fake.serverPush(
          {'type': 'code_pty_exit', 'pty_id': 't1', 'exit_code': 0});

      final fin = (await events).single as TaskFinished;
      expect(fin.reason, 'end_turn');
      expect(fin.errorMessage, isNull);
      await client.close();
    });

    test('pty_exit non-zero → TaskFinished(error) carrying code', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'codex');
      final events = adapter.run(_task(agent: AgentKind.codex)).toList();
      await Future<void>.delayed(const Duration(milliseconds: 10));

      fake.serverPush({
        'type': 'code_response',
        'request_id': fake.requestIdFor('code.runTask'),
        'ok': true,
        'result': {'pty_id': 't1'},
      });
      fake.serverPush({
        'type': 'code_pty_exit',
        'pty_id': 't1',
        'exit_code': 130,
        'error': 'interrupted',
      });

      final fin = (await events).single as TaskFinished;
      expect(fin.reason, 'error');
      expect(fin.errorMessage, contains('130'));
      expect(fin.errorMessage, contains('codex'));
      await client.close();
    });

    test('exit frame for a different pty_id is ignored', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'claude');
      final events = <AgentEvent>[];
      final sub = adapter.run(_task(id: 'mine')).listen(events.add);
      await Future<void>.delayed(const Duration(milliseconds: 10));

      fake.serverPush({
        'type': 'code_response',
        'request_id': fake.requestIdFor('code.runTask'),
        'ok': true,
        'result': {'pty_id': 'mine'},
      });
      // 别的 task 的退出帧 —— 不该终止本 task
      fake.serverPush(
          {'type': 'code_pty_exit', 'pty_id': 'other', 'exit_code': 0});
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(events.whereType<TaskFinished>(), isEmpty);

      // 自己的退出帧才终止
      fake.serverPush(
          {'type': 'code_pty_exit', 'pty_id': 'mine', 'exit_code': 0});
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(events.whereType<TaskFinished>().single.reason, 'end_turn');

      await sub.cancel();
      await client.close();
    });

    test('session events for this task merge into the run stream (M3)', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'claude');
      final events = <AgentEvent>[];
      final sub = adapter.run(_task(id: 'tt')).listen(events.add);
      await Future<void>.delayed(const Duration(milliseconds: 10));

      fake.serverPush({
        'type': 'code_response',
        'request_id': fake.requestIdFor('code.runTask'),
        'ok': true,
        'result': {'pty_id': 'tt'},
      });
      // 本 task 的会话事件 → 解析成 AgentEvent 并入流
      fake.serverPush({
        'type': 'code_session_event',
        'task_id': 'tt',
        'event': {'type': 'text_delta', 'ts': 't', 'text': '你好'},
      });
      fake.serverPush({
        'type': 'code_session_event',
        'task_id': 'tt',
        'event': {
          'type': 'tool_use_start',
          'ts': 't',
          'tool_id': 'x',
          'name': 'Read',
          'args': {'p': 1}
        },
      });
      // 别的 task 的会话事件 → 忽略
      fake.serverPush({
        'type': 'code_session_event',
        'task_id': 'other',
        'event': {'type': 'text_delta', 'ts': 't', 'text': 'nope'},
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      expect(events.whereType<TextDelta>().single.text, '你好');
      expect(events.whereType<ToolUseStart>().single.name, 'Read');

      // 退出收尾
      fake.serverPush(
          {'type': 'code_pty_exit', 'pty_id': 'tt', 'exit_code': 0});
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(events.whereType<TaskFinished>().single.reason, 'end_turn');

      await sub.cancel();
      await client.close();
    });

    test('null client (daemon 未就绪) → immediate TaskFinished(error)', () async {
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => null, agentType: 'claude');
      final fin = (await adapter.run(_task()).toList()).single as TaskFinished;
      expect(fin.reason, 'error');
      expect(fin.errorMessage, contains('daemon'));
    });

    test('cancel() sends pty.kill frame', () async {
      final (client, fake) = await _connected();
      final adapter =
          BridgeTerminalAdapter(resolveClient: () => client, agentType: 'claude');

      final fut = adapter.cancel('t1');
      await Future<void>.delayed(const Duration(milliseconds: 10));
      final reqId = fake.requestIdFor('pty.kill');
      expect(fake.lastSentJson()['params']['pty_id'], 't1');
      // 回响应让 cancel future 完成
      fake.serverPush({
        'type': 'code_response',
        'request_id': reqId,
        'ok': true,
        'result': const <String, dynamic>{},
      });
      await fut;
      await client.close();
    });
  });
}
