// MultiplexAgentAdapter — 根据 task.agent 把请求转发到对应底层 adapter
// (biu → BiuAdapter 进程内结构化;claude/codex → BridgeTerminalAdapter 真 PTY)。
//
// 让 controller 的代码不感知"任务跑在哪个 agent 上"——它只调
// adapter.run(task)，多路复用层负责选 + cancel 到正确的实例。

import '../domain/code_task.dart';
import 'agent_adapter.dart';

class MultiplexAgentAdapter implements AgentAdapter {
  MultiplexAgentAdapter({
    required AgentAdapter Function(AgentKind) factory,
  }) : _factory = factory;

  final AgentAdapter Function(AgentKind) _factory;

  /// 一个 AgentKind 一个 adapter 单例（每个内部维护 running task map）。
  final Map<AgentKind, AgentAdapter> _adapters = {};

  /// task_id → 它的 agent 类型（cancel 时知道找哪个 adapter）。
  final Map<String, AgentKind> _taskAgent = {};

  AgentAdapter _adapter(AgentKind k) =>
      _adapters.putIfAbsent(k, () => _factory(k));

  @override
  String get name => 'multiplex';

  @override
  Stream<AgentEvent> run(CodeTask task,
      {bool resume = false, String? resumeSessionId, bool reattach = false}) {
    _taskAgent[task.id] = task.agent;
    return _adapter(task.agent).run(task,
        resume: resume, resumeSessionId: resumeSessionId, reattach: reattach);
  }

  @override
  Future<void> respondPermission(
    String toolId,
    PermissionDecision decision,
  ) async {
    // 没法从 toolId 反推到 agent — 这里广播给所有已实例化的 adapter，
    // 每个 adapter 自己判断是否归它管。P1 完善权限决策路由时改成
    // taskId → adapter 精确路由。
    for (final a in _adapters.values) {
      await a.respondPermission(toolId, decision);
    }
  }

  @override
  Future<void> cancel(String taskId) async {
    final kind = _taskAgent.remove(taskId);
    if (kind == null) return;
    await _adapters[kind]?.cancel(taskId);
  }
}
