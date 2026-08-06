// BridgeTerminalAdapter — claude/codex 经本地 bridge 在真 PTY 里跑(决策 D6)。
//
// 不在 Flutter 直接 spawn(那是已废弃的 v3 stream-json adapter,见
// docs/BiuMind-Code-Design.md:700 D6);改为发 RPC `code.runTask` 让 biu daemon
// 在 PTY 里拉起交互式 TUI(pty_id = task.id),字节流经 code_pty_chunk 推回,由
// CodeTerminalView(xterm)直接订阅渲染 —— 字节流不经 AgentEvent。
//
// 本 adapter 只负责「生命周期 + cancel」这两件事,把 PTY 的退出归一化成控制器
// 认得的 AgentEvent:
//   - run()      → 发 code.runTask;订阅该 task 的 code_pty_exit → TaskFinished
//   - cancel()   → code.killPty(task_id)(SIGTERM 子进程)
//   - 实时输出 / 用户输入 / resize 全由 CodeTerminalView 直连 bridge,不过这里
//
// 与 BiuAdapter(进程内 SDK Protocol、产结构化事件)有意分叉:claude/codex 在
// M2 只有「真终端 live 视图」,结构化 SessionView 由 JSONL 会话发现在 M3 补。

import 'dart:async';

import '../data/code_bridge_client.dart';
import '../domain/code_task.dart';
import 'agent_adapter.dart';

class BridgeTerminalAdapter implements AgentAdapter {
  BridgeTerminalAdapter({
    required this.resolveClient,
    required this.agentType,
  });

  /// 惰性解析当前 bridge 连接 —— **不在构造时捕获**。
  ///
  /// 关键:agentAdapterProvider 若 watch(codeBridgeClientProvider) 把 client 注进来,
  /// 则 bridge 每次连/断(启动 null→connected、daemon 重启换端口)都会重建 adapter →
  /// 进而重建 tasksController → _hydrate 重跑 → 把正在跑的任务误标 interrupted
  /// (实测过:任务 events 空 + errorMessage="(restart 时被中断)")。改为运行时
  /// ref.read 取当前 bridge,provider 不再 watch,controller 生命周期与 bridge 解耦。
  final CodeBridgeClient? Function() resolveClient;

  /// 'claude' | 'codex' —— 透传给 Go 侧 agent.DetectPath / BuildLaunch。
  final String agentType;

  /// task_id → 该 task 的订阅(pty_exit + session events;cancel / 结束时全取消)。
  final Map<String, List<StreamSubscription<void>>> _subs = {};

  @override
  String get name => agentType;

  @override
  Stream<AgentEvent> run(CodeTask task,
      {bool resume = false, String? resumeSessionId, bool reattach = false}) {
    final out = StreamController<AgentEvent>();
    _drive(task, out,
        resume: resume, resumeSessionId: resumeSessionId, reattach: reattach);
    return out.stream;
  }

  Future<void> _drive(CodeTask task, StreamController<AgentEvent> out,
      {bool resume = false,
      String? resumeSessionId,
      bool reattach = false}) async {
    DateTime now() => DateTime.now();
    Future<void> cleanup() async {
      final subs = _subs.remove(task.id);
      if (subs != null) {
        for (final s in subs) {
          await s.cancel();
        }
      }
    }

    void finish(String reason, [String? err]) {
      if (out.isClosed) return;
      out.add(TaskFinished(ts: now(), reason: reason, errorMessage: err));
      unawaited(cleanup());
      unawaited(out.close());
    }

    final c = resolveClient();
    if (c == null) {
      finish('error', '本地 daemon 未就绪 —— 无法在终端运行 $agentType。'
          '请确认已登录且桌面端 biu serve 已启动。');
      return;
    }

    // 先挂订阅,再发 runTask —— 避免进程瞬间退出 / 首批会话事件早到时漏帧。
    final exitSub = c.ptyExits.where((e) => e.ptyId == task.id).listen((e) {
      final ok = e.exitCode == 0;
      final detail = (e.error != null && e.error!.isNotEmpty) ? ' (${e.error})' : '';
      finish(ok ? 'end_turn' : 'error',
          ok ? null : '$agentType 退出 code=${e.exitCode}$detail');
    });
    // 结构化会话事件(从 agent JSONL 解析)并入输出流 → 控制器 append 进 task.events
    // → AgentStream 渲染。原始终端字节走 code_pty_chunk / CodeTerminalView,两者并存。
    final sessionSub = c.sessionEvents.where((e) => e.taskId == task.id).listen((e) {
      if (out.isClosed) return;
      final ev = AgentEvent.fromJson(e.event);
      if (ev != null) out.add(ev);
    });
    _subs[task.id] = [exitSub, sessionSub];
    // 监听方取消时(切任务 / dispose)清理订阅,防泄漏。
    out.onCancel = cleanup;

    try {
      if (reattach) {
        // 断线重连:不 spawn,把仍在跑的 PTY 输出重绑到当前连接。alive=false 表示
        // 进程其实已退 → 归一化为结束(让上层退回「恢复」--resume 起新会话)。
        final alive = await c.reattachTask(
          taskId: task.id,
          agentType: agentType,
          cwd: task.workspace?.localPath,
          sessionId: resumeSessionId,
        );
        if (!alive) {
          finish('error', '会话已不在(进程已退出),请改用「恢复」续跑。');
        }
      } else {
        await c.runTask(
          taskId: task.id,
          agentType: agentType,
          permissionMode: task.mode.label, // ask / auto_edit / full_access
          prompt: task.prompt,
          model: task.model, // 用户选的模型;null = CLI 默认(不传 --model)
          cwd: task.workspace?.localPath,
          resume: resume,
          sessionId: resumeSessionId,
          // 初始尺寸;CodeTerminalView 挂载后 onResize 会立刻用真实尺寸纠正(SIGWINCH)。
          cols: 80,
          rows: 24,
        );
      }
    } on CodeBridgeException catch (e) {
      // runTask 失败(binary 未找到 / agent=biu 被拒等)→ 直接失败,不会再有 exit 帧。
      finish('error', e.error ?? e.toString());
    } catch (e) {
      finish('error', e.toString());
    }
  }

  @override
  Future<void> cancel(String taskId) async {
    // killPty 触发子进程 SIGTERM → daemon 推 code_pty_exit → 上面订阅归一化为
    // TaskFinished(reason=error/end_turn)。这里不直接 emit,让退出帧作单一真相源。
    try {
      await resolveClient()?.killPty(taskId);
    } on CodeBridgeException {
      // 进程可能已退出 / pty 已不在;忽略,exit 帧(若有)会推进状态。
    }
  }

  @override
  Future<void> respondPermission(String toolId, PermissionDecision decision) async {
    // PTY 交互模式下,审批由用户直接在 xterm 里按键回应(claude/codex 的 TUI 自己弹问)。
    // 跨端 PreToolUse hook 审批是 M4(接 hook → code frame),M2 不实现。
  }
}
