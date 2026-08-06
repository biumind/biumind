// TaskCreated / TaskCompleted hook —— 后台任务生命周期。
// 用 TaskCreatedHook / TaskCompletedHook 后缀避免跟 data plane SDKTaskStarted 等混淆。

import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'task.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class TaskCreatedHook implements HookInput {
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  @override
  @JsonKey(name: 'transcript_path')
  final String transcriptPath;
  @override
  final String cwd;
  @override
  @JsonKey(name: 'hook_event_name')
  final String hookEventName;
  @JsonKey(name: 'task_id')
  final String taskId;
  @JsonKey(name: 'task_subject')
  final String taskSubject;
  @JsonKey(name: 'task_description')
  final String? taskDescription;
  @JsonKey(name: 'teammate_name')
  final String? teammateName;
  @JsonKey(name: 'team_name')
  final String? teamName;

  TaskCreatedHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.taskCreated,
    required this.taskId,
    required this.taskSubject,
    this.taskDescription,
    this.teammateName,
    this.teamName,
  });

  factory TaskCreatedHook.fromJson(Map<String, dynamic> json) =>
      _$TaskCreatedHookFromJson(json);
  Map<String, dynamic> toJson() => _$TaskCreatedHookToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class TaskCompletedHook implements HookInput {
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  @override
  @JsonKey(name: 'transcript_path')
  final String transcriptPath;
  @override
  final String cwd;
  @override
  @JsonKey(name: 'hook_event_name')
  final String hookEventName;
  @JsonKey(name: 'task_id')
  final String taskId;
  @JsonKey(name: 'task_subject')
  final String taskSubject;
  @JsonKey(name: 'task_description')
  final String? taskDescription;
  @JsonKey(name: 'teammate_name')
  final String? teammateName;
  @JsonKey(name: 'team_name')
  final String? teamName;

  TaskCompletedHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.taskCompleted,
    required this.taskId,
    required this.taskSubject,
    this.taskDescription,
    this.teammateName,
    this.teamName,
  });

  factory TaskCompletedHook.fromJson(Map<String, dynamic> json) =>
      _$TaskCompletedHookFromJson(json);
  Map<String, dynamic> toJson() => _$TaskCompletedHookToJson(this);
}
