// AgentAdapter — 三种 Agent 的统一接口。
//
// 实现:
//   - DummyAdapter        — 演示流程(web/mobile / BIUMIND_AGENT=dummy)
//   - BiuAdapter          — biu 进程内 SDK Protocol,产结构化 AgentEvent
//   - BridgeTerminalAdapter — claude/codex 经本地 bridge 在真 PTY 里跑(D6),
//                             生命周期归一化为 AgentEvent,实时输出走 xterm

import 'dart:async';
import 'package:uuid/uuid.dart';

import '../domain/code_task.dart';

abstract class AgentAdapter {
  String get name;

  /// 启动 agent 跑这个任务，返回流式事件。
  /// 调用方在 stream 关闭时认为任务结束（最后一个事件应为 [TaskFinished]）。
  ///
  /// [resume]=true 且 [resumeSessionId] 非空 → 续跑已有会话(G5,--resume,不重发
  /// prompt);仅外部 CLI agent(claude)支持,其余实现忽略。
  ///
  /// [reattach]=true → 不 spawn 新进程,把一个**仍在跑**的 PTY 输出重绑到当前连接
  /// (断线重连,detached → 重新连接);仅外部 CLI agent(claude/codex)支持。
  Stream<AgentEvent> run(CodeTask task,
      {bool resume = false, String? resumeSessionId, bool reattach = false});

  /// 用户对 PermissionAsk 的回应（allow / deny / allow_once）
  Future<void> respondPermission(String toolId, PermissionDecision decision);

  /// 取消正在跑的任务
  Future<void> cancel(String taskId);
}

enum PermissionDecision { allow, allowOnce, deny }

/// P0 演示用 — 模拟 5s 内的完整 agent 流程：
/// 文本 → Read → 文本 → Edit → Cost → Finished
class DummyAdapter implements AgentAdapter {
  DummyAdapter() : _uuid = const Uuid();
  final Uuid _uuid;
  final Map<String, StreamController<AgentEvent>?> _running = {};

  @override
  String get name => 'biu (dummy)';

  @override
  Stream<AgentEvent> run(CodeTask task,
      {bool resume = false, String? resumeSessionId, bool reattach = false}) {
    final controller = StreamController<AgentEvent>();
    _running[task.id] = controller;

    Future<void> drive() async {
      DateTime now() => DateTime.now();
      void emit(AgentEvent e) {
        if (controller.isClosed) return;
        controller.add(e);
      }

      // 0. 短暂延迟模拟首字时延
      await Future<void>.delayed(const Duration(milliseconds: 200));
      emit(TextDelta(ts: now(), text: '好的，我来处理 "${task.title}"。先看一下相关文件。'));

      // 1. Read
      await Future<void>.delayed(const Duration(milliseconds: 600));
      final readId = _uuid.v4();
      emit(ToolUseStart(
        ts: now(),
        toolId: readId,
        name: 'Read',
        args: {'path': 'lib/main.dart'},
      ));
      await Future<void>.delayed(const Duration(milliseconds: 800));
      emit(ToolUseResult(
        ts: now(),
        toolId: readId,
        result: '''import 'package:flutter/material.dart';

void main() => runApp(const BiuMindApp());

class BiuMindApp extends StatelessWidget {
  const BiuMindApp({super.key});
  @override
  Widget build(BuildContext context) =>
      const MaterialApp(home: HomePage());
}''',
      ));

      // 2. 思考 + Edit
      await Future<void>.delayed(const Duration(milliseconds: 700));
      emit(TextDelta(
        ts: now(),
        text: '\n\n看到了入口结构清晰。我在 README.md 末尾加一行欢迎提示。',
      ));

      await Future<void>.delayed(const Duration(milliseconds: 500));
      final editId = _uuid.v4();
      emit(ToolUseStart(
        ts: now(),
        toolId: editId,
        name: 'Edit',
        args: {
          'file_path': 'README.md',
          'old_string': '# BiuMind Agentics',
          'new_string': '# BiuMind Agentics\n\n> Welcome to the future of AI workbenches.',
        },
      ));
      await Future<void>.delayed(const Duration(milliseconds: 600));
      emit(ToolUseResult(
        ts: now(),
        toolId: editId,
        result: '✓ Applied: README.md (+1 line)',
      ));

      // 3. 收尾文本 + cost
      await Future<void>.delayed(const Duration(milliseconds: 500));
      emit(TextDelta(ts: now(), text: '\n\n完成。一行欢迎提示已加到 README.md 顶部。'));

      await Future<void>.delayed(const Duration(milliseconds: 300));
      emit(CostUpdate(
        ts: now(),
        totalUsd: 0.003,
        inputTokens: 412,
        outputTokens: 89,
      ));

      // 4. Finished
      await Future<void>.delayed(const Duration(milliseconds: 200));
      emit(TaskFinished(ts: now(), reason: 'end_turn'));

      await controller.close();
      _running.remove(task.id);
    }

    // 不 await — 让 caller 立刻拿到 stream
    drive();
    return controller.stream;
  }

  @override
  Future<void> respondPermission(String toolId, PermissionDecision decision) async {
    // P0 dummy 没有真实 PreToolUse 拦截
  }

  @override
  Future<void> cancel(String taskId) async {
    final c = _running[taskId];
    if (c != null && !c.isClosed) {
      c.add(TaskFinished(ts: DateTime.now(), reason: 'canceled'));
      await c.close();
      _running.remove(taskId);
    }
  }
}
