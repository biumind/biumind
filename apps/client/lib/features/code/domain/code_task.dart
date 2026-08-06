// 编码任务领域模型 — 与 biu CLI 的 session 概念一一对应。
// 暂不持久化（P0 内存，P1 上 Drift）。

import 'package:flutter/foundation.dart';

import 'workspace.dart';

/// 任务状态机（扩展 input_required / interrupted / detached）
enum CodeTaskStatus {
  queued,
  running,
  paused,
  inputRequired,
  done,
  failed,
  interrupted,

  /// 终端连接断开但 agent 进程**仍在后端存活**(daemon 幸存、客户端换了连接,如
  /// 热重启/崩溃重连)。可「重新连接」接回活动终端;区别于
  /// interrupted(进程已死,只能 --resume 起新会话)。
  detached,
}

/// 三种 Agent 来源（P0 只接 biu）
enum AgentKind { biu, claudeCode, codex }

extension AgentKindLabel on AgentKind {
  String get label => switch (this) {
        AgentKind.biu => 'biu',
        AgentKind.claudeCode => 'Claude',
        AgentKind.codex => 'Codex',
      };
}

/// 本任务执行环境。
/// auto = 跟随全局设置(codeUseWorktree);local = 强制在 cwd 直跑(passthrough);
/// worktree = 强制为本任务新建 git worktree 隔离。
enum CodeLaunchMode { auto, local, worktree }

extension CodeLaunchModeLabel on CodeLaunchMode {
  String get label => switch (this) {
        CodeLaunchMode.auto => '跟随设置',
        CodeLaunchMode.local => '本地直跑',
        CodeLaunchMode.worktree => '新 worktree',
      };
}

/// 三档权限模式（直接映射到 CLI flag）
enum PermissionMode { ask, autoEdit, fullAccess }

extension PermissionModeLabel on PermissionMode {
  String get label => switch (this) {
        PermissionMode.ask => 'ask',
        PermissionMode.autoEdit => 'auto_edit',
        PermissionMode.fullAccess => 'full_access',
      };
}

/// 流式 agent 事件 — 归一化各 adapter 的输出格式（参考 AG-UI 协议）
@immutable
sealed class AgentEvent {
  const AgentEvent({required this.ts});
  final DateTime ts;

  Map<String, dynamic> toJson();

  static AgentEvent? fromJson(Map<String, dynamic> j) {
    final type = j['type'] as String?;
    final ts = DateTime.tryParse(j['ts'] as String? ?? '') ?? DateTime.now();
    switch (type) {
      case 'text_delta':
        return TextDelta(ts: ts, text: j['text'] as String? ?? '');
      case 'tool_use_start':
        return ToolUseStart(
          ts: ts,
          toolId: j['tool_id'] as String? ?? '',
          name: j['name'] as String? ?? '',
          args: (j['args'] as Map?)?.cast<String, dynamic>() ?? const {},
        );
      case 'tool_use_result':
        return ToolUseResult(
          ts: ts,
          toolId: j['tool_id'] as String? ?? '',
          result: j['result'] as String? ?? '',
          isError: j['is_error'] == true,
        );
      case 'permission_ask':
        return PermissionAsk(
          ts: ts,
          toolId: j['tool_id'] as String? ?? '',
          name: j['name'] as String? ?? '',
          args: (j['args'] as Map?)?.cast<String, dynamic>() ?? const {},
        );
      case 'cost_update':
        return CostUpdate(
          ts: ts,
          totalUsd: (j['total_usd'] as num?)?.toDouble() ?? 0,
          inputTokens: (j['input_tokens'] as num?)?.toInt() ?? 0,
          outputTokens: (j['output_tokens'] as num?)?.toInt() ?? 0,
          cacheCreationTokens:
              (j['cache_creation_tokens'] as num?)?.toInt() ?? 0,
          cacheReadTokens: (j['cache_read_tokens'] as num?)?.toInt() ?? 0,
          contextTokens: (j['context_tokens'] as num?)?.toInt() ?? 0,
          contextWindow: (j['context_window'] as num?)?.toInt() ?? 0,
        );
      case 'agent_status':
        return AgentStatus(ts: ts, status: j['status'] as String? ?? '');
      case 'session_info':
        return SessionInfo(
          ts: ts,
          agent: j['agent'] as String? ?? '',
          sessionId: j['session_id'] as String? ?? '',
        );
      case 'task_finished':
        return TaskFinished(
          ts: ts,
          reason: j['reason'] as String? ?? 'end_turn',
          errorMessage: j['error_message'] as String?,
        );
      default:
        return null;
    }
  }
}

/// 文本流增量
class TextDelta extends AgentEvent {
  const TextDelta({required super.ts, required this.text});
  final String text;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'text_delta',
        'ts': ts.toIso8601String(),
        'text': text,
      };
}

/// 工具调用开始
class ToolUseStart extends AgentEvent {
  const ToolUseStart({
    required super.ts,
    required this.toolId,
    required this.name,
    required this.args,
  });
  final String toolId;
  final String name;
  final Map<String, dynamic> args;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'tool_use_start',
        'ts': ts.toIso8601String(),
        'tool_id': toolId,
        'name': name,
        'args': args,
      };
}

/// 工具调用结果（成功 / 失败）
class ToolUseResult extends AgentEvent {
  const ToolUseResult({
    required super.ts,
    required this.toolId,
    required this.result,
    this.isError = false,
  });
  final String toolId;
  final String result;
  final bool isError;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'tool_use_result',
        'ts': ts.toIso8601String(),
        'tool_id': toolId,
        'result': result,
        'is_error': isError,
      };
}

/// PreToolUse 权限弹问 — UI 显示 inline 卡片让用户决定
class PermissionAsk extends AgentEvent {
  const PermissionAsk({
    required super.ts,
    required this.toolId,
    required this.name,
    required this.args,
  });
  final String toolId;
  final String name;
  final Map<String, dynamic> args;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'permission_ask',
        'ts': ts.toIso8601String(),
        'tool_id': toolId,
        'name': name,
        'args': args,
      };
}

/// 实时累计 cost / token 用量。input/output 是任务累计;context_* 是最近一轮
/// 喂给模型的上下文(用于详情头算上下文利用率,G4)。
class CostUpdate extends AgentEvent {
  const CostUpdate({
    required super.ts,
    required this.totalUsd,
    required this.inputTokens,
    required this.outputTokens,
    this.cacheCreationTokens = 0,
    this.cacheReadTokens = 0,
    this.contextTokens = 0,
    this.contextWindow = 0,
  });
  final double totalUsd;
  final int inputTokens;
  final int outputTokens;
  final int cacheCreationTokens;
  final int cacheReadTokens;

  /// 最近一轮上下文 token 数(非累计)。
  final int contextTokens;

  /// 模型上下文窗口上限;0 = 未知(Claude JSONL 不报)。
  final int contextWindow;

  @override
  Map<String, dynamic> toJson() => {
        'type': 'cost_update',
        'ts': ts.toIso8601String(),
        'total_usd': totalUsd,
        'input_tokens': inputTokens,
        'output_tokens': outputTokens,
        'cache_creation_tokens': cacheCreationTokens,
        'cache_read_tokens': cacheReadTokens,
        'context_tokens': contextTokens,
        'context_window': contextWindow,
      };
}

/// agent 生命周期状态信号（PERI-1）。由 hook 驱动：running ↔ input_required 的可靠
/// 切换 —— Stop/Notification/PermissionRequest → input_required；PostToolUse/
/// UserPromptSubmit → running。纯 JSONL 轮询测不到「一轮结束停在等用户」，故走 hook。
class AgentStatus extends AgentEvent {
  const AgentStatus({required super.ts, required this.status});

  /// 'running' | 'input_required'
  final String status;

  @override
  Map<String, dynamic> toJson() => {
        'type': 'agent_status',
        'ts': ts.toIso8601String(),
        'status': status,
      };
}

/// 会话标识(G5)。daemon 在 agent 启动后回传 sessionId,客户端持久化(进 events,
/// 随 eventsJson 全量存盘),供「恢复中断任务」用 --resume 续上原会话。不渲染成消息。
class SessionInfo extends AgentEvent {
  const SessionInfo({
    required super.ts,
    required this.agent,
    required this.sessionId,
  });
  final String agent; // claude / codex
  final String sessionId;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'session_info',
        'ts': ts.toIso8601String(),
        'agent': agent,
        'session_id': sessionId,
      };
}

/// 任务终止（done / failed / interrupted）
class TaskFinished extends AgentEvent {
  const TaskFinished({
    required super.ts,
    required this.reason,
    this.errorMessage,
  });
  final String reason; // end_turn / max_turns / error / canceled
  final String? errorMessage;
  @override
  Map<String, dynamic> toJson() => {
        'type': 'task_finished',
        'ts': ts.toIso8601String(),
        'reason': reason,
        if (errorMessage != null) 'error_message': errorMessage,
      };
}

/// 累计 cost 快照
@immutable
class TaskCost {
  const TaskCost({
    this.usd = 0,
    this.inputTokens = 0,
    this.outputTokens = 0,
    this.contextTokens = 0,
    this.contextWindow = 0,
  });
  final double usd;
  final int inputTokens;
  final int outputTokens;

  /// 最近一轮上下文 token(replace 语义,反映当前上下文占用)。
  final int contextTokens;

  /// 模型上下文窗口;0 = 未知。
  final int contextWindow;

  TaskCost copyWith({
    double? usd,
    int? inputTokens,
    int? outputTokens,
    int? contextTokens,
    int? contextWindow,
  }) =>
      TaskCost(
        usd: usd ?? this.usd,
        inputTokens: inputTokens ?? this.inputTokens,
        outputTokens: outputTokens ?? this.outputTokens,
        contextTokens: contextTokens ?? this.contextTokens,
        contextWindow: contextWindow ?? this.contextWindow,
      );
}

/// 编码任务 — 一个 agent 会话的全状态快照
@immutable
class CodeTask {
  const CodeTask({
    required this.id,
    required this.title,
    required this.prompt,
    required this.agent,
    required this.mode,
    required this.status,
    required this.events,
    required this.cost,
    required this.createdAt,
    this.completedAt,
    this.errorMessage,
    this.workspace,
    this.compareGroupId,
    this.originDeviceId,
    this.originDeviceLabel,
    this.projectId,
    this.updatedAt,
    this.model,
    this.starred = false,
  });

  final String id;
  final String title;
  final String prompt;
  final AgentKind agent;
  final PermissionMode mode;
  final CodeTaskStatus status;
  final List<AgentEvent> events;
  final TaskCost cost;
  final DateTime createdAt;
  final DateTime? completedAt;
  final String? errorMessage;

  /// 任务关联的工作区。null = 任务还在 allocate 中（短暂窗口）。
  /// adapter spawn 时取 [workspace?.localPath] 作 cwd。
  final WorkspaceRef? workspace;

  /// 对比组 id — 同 prompt 派给多个 agent 时, 这些 task 共享同一 id。
  /// null = 单独任务。UI 主区切到"任务对比" Tab 时把同组任务并排显示。
  final String? compareGroupId;

  /// 任务实际跑在哪台机器 (CSY4: 仅 origin 处理 cmd)。
  /// 本机创建的任务 = 本机的 settings.codeOriginDeviceId;
  /// 从 Realtime / pull 拉来的任务 = 远端 device id。
  final String? originDeviceId;

  /// origin device 的人类可读 label (本机 hostname / 'macOS' 等)。
  /// UI 列表 / 详情显示 "@<label>" tag, 用户能一眼看出任务在哪台机跑。
  final String? originDeviceLabel;

  /// 所属项目 id (M1 多项目, 指向 CodeProject.id)。null = 老任务无归属 / 全局。
  final String? projectId;

  /// 最后更新时间 (M1)。TaskList 排序 / "n 分钟前" 显示;null 时回退 createdAt。
  final DateTime? updatedAt;

  /// 用户为本任务选的模型 id(创建时定)。null = 用 agent 默认(biu 用 config,
  /// claude/codex 不传 --model 回退 CLI 自有配置)。仅客户端持久化,不进同步
  /// (codeSync 已废弃,D4/Code-I6)。
  final String? model;

  /// 星标(CORE-2)。列表里星标任务可置顶/筛选。仅本地。
  final bool starred;

  /// 任务持续时间 (running → done/failed/interrupted)
  Duration? get duration =>
      completedAt?.difference(createdAt);

  /// 续跑用的会话 id(G5):扫 events 取最后一条 SessionInfo。null = 不可续跑
  /// (无会话 id / agent 不支持)。当前仅 claude 回传 session_info。
  String? get resumeSessionId {
    for (final e in events.reversed) {
      if (e is SessionInfo && e.sessionId.isNotEmpty) return e.sessionId;
    }
    return null;
  }

  /// 是否可续跑:非终态卡住(中断/暂停/等输入)且持有会话 id。
  bool get canResume =>
      resumeSessionId != null &&
      (status == CodeTaskStatus.interrupted ||
          status == CodeTaskStatus.paused ||
          status == CodeTaskStatus.inputRequired);

  CodeTask copyWith({
    String? title,
    CodeTaskStatus? status,
    List<AgentEvent>? events,
    TaskCost? cost,
    DateTime? completedAt,
    String? errorMessage,
    WorkspaceRef? workspace,
    String? originDeviceId,
    String? originDeviceLabel,
    String? projectId,
    DateTime? updatedAt,
    bool? starred,
  }) =>
      CodeTask(
        id: id,
        title: title ?? this.title,
        prompt: prompt,
        agent: agent,
        mode: mode,
        status: status ?? this.status,
        events: events ?? this.events,
        cost: cost ?? this.cost,
        createdAt: createdAt,
        completedAt: completedAt ?? this.completedAt,
        errorMessage: errorMessage ?? this.errorMessage,
        workspace: workspace ?? this.workspace,
        compareGroupId: compareGroupId,
        originDeviceId: originDeviceId ?? this.originDeviceId,
        originDeviceLabel: originDeviceLabel ?? this.originDeviceLabel,
        projectId: projectId ?? this.projectId,
        updatedAt: updatedAt ?? this.updatedAt,
        model: model, // 创建时定,patch 不改
        starred: starred ?? this.starred,
      );
}
