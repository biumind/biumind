// 27 个 hook event 字面量 + BaseHookInput 公共字段。
//
// Dart 这里不用 enum，直接用 String const，方便字段名与服务端 1:1 对齐。

class HookEvent {
  static const preToolUse = 'PreToolUse';
  static const postToolUse = 'PostToolUse';
  static const postToolUseFailure = 'PostToolUseFailure';
  static const notification = 'Notification';
  static const userPromptSubmit = 'UserPromptSubmit';
  static const sessionStart = 'SessionStart';
  static const sessionEnd = 'SessionEnd';
  static const stop = 'Stop';
  static const stopFailure = 'StopFailure';
  static const subagentStart = 'SubagentStart';
  static const subagentStop = 'SubagentStop';
  static const preCompact = 'PreCompact';
  static const postCompact = 'PostCompact';
  static const permissionRequest = 'PermissionRequest';
  static const permissionDenied = 'PermissionDenied';
  static const setup = 'Setup';
  static const teammateIdle = 'TeammateIdle';
  static const taskCreated = 'TaskCreated';
  static const taskCompleted = 'TaskCompleted';
  static const elicitation = 'Elicitation';
  static const elicitationResult = 'ElicitationResult';
  static const configChange = 'ConfigChange';
  static const worktreeCreate = 'WorktreeCreate';
  static const worktreeRemove = 'WorktreeRemove';
  static const instructionsLoaded = 'InstructionsLoaded';
  static const cwdChanged = 'CwdChanged';
  static const fileChanged = 'FileChanged';
}

/// 27 个 HookInput variant 共用字段。Dart 用 abstract base class，
/// 子类把这些字段重新声明为 final（json_serializable 不支持继承字段直接 ser/des）。
abstract class HookInput {
  String get sessionId;
  String get transcriptPath;
  String get cwd;
  String get hookEventName;
}
