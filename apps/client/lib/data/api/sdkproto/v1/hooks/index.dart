// Hook input dispatcher —— peek hook_event_name 后 dispatch 到具体类型。

import 'compact.dart';
import 'config.dart';
import 'elicitation.dart';
import 'events.dart';
import 'filesystem.dart';
import 'instructions.dart';
import 'notification.dart';
import 'permission.dart';
import 'session.dart';
import 'subagent.dart';
import 'task.dart';
import 'tool_use.dart';
import 'worktree.dart';

class HookInputFactory {
  /// 给一段 JSON，根据 hook_event_name 返回具体 HookInput 子类。
  /// 未知 hook_event_name 抛 ArgumentError。
  static HookInput fromJson(Map<String, dynamic> json) {
    final name = json['hook_event_name'] as String? ?? '';
    switch (name) {
      case HookEvent.preToolUse:
        return PreToolUse.fromJson(json);
      case HookEvent.postToolUse:
        return PostToolUse.fromJson(json);
      case HookEvent.postToolUseFailure:
        return PostToolUseFailure.fromJson(json);
      case HookEvent.permissionDenied:
        return PermissionDenied.fromJson(json);
      case HookEvent.permissionRequest:
        return PermissionRequestHook.fromJson(json);
      case HookEvent.notification:
        return NotificationHook.fromJson(json);
      case HookEvent.userPromptSubmit:
        return UserPromptSubmit.fromJson(json);
      case HookEvent.sessionStart:
        return SessionStart.fromJson(json);
      case HookEvent.sessionEnd:
        return SessionEnd.fromJson(json);
      case HookEvent.stop:
        return Stop.fromJson(json);
      case HookEvent.stopFailure:
        return StopFailure.fromJson(json);
      case HookEvent.setup:
        return Setup.fromJson(json);
      case HookEvent.subagentStart:
        return SubagentStart.fromJson(json);
      case HookEvent.subagentStop:
        return SubagentStop.fromJson(json);
      case HookEvent.teammateIdle:
        return TeammateIdle.fromJson(json);
      case HookEvent.preCompact:
        return PreCompact.fromJson(json);
      case HookEvent.postCompact:
        return PostCompact.fromJson(json);
      case HookEvent.taskCreated:
        return TaskCreatedHook.fromJson(json);
      case HookEvent.taskCompleted:
        return TaskCompletedHook.fromJson(json);
      case HookEvent.elicitation:
        return ElicitationHook.fromJson(json);
      case HookEvent.elicitationResult:
        return ElicitationResultHook.fromJson(json);
      case HookEvent.configChange:
        return ConfigChange.fromJson(json);
      case HookEvent.instructionsLoaded:
        return InstructionsLoaded.fromJson(json);
      case HookEvent.worktreeCreate:
        return WorktreeCreate.fromJson(json);
      case HookEvent.worktreeRemove:
        return WorktreeRemove.fromJson(json);
      case HookEvent.cwdChanged:
        return CwdChanged.fromJson(json);
      case HookEvent.fileChanged:
        return FileChanged.fromJson(json);
      default:
        throw ArgumentError('unknown hook_event_name: $name');
    }
  }
}
