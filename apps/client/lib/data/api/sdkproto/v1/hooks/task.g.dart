// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'task.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

TaskCreatedHook _$TaskCreatedHookFromJson(Map json) => TaskCreatedHook(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.taskCreated,
  taskId: json['task_id'] as String,
  taskSubject: json['task_subject'] as String,
  taskDescription: json['task_description'] as String?,
  teammateName: json['teammate_name'] as String?,
  teamName: json['team_name'] as String?,
);

Map<String, dynamic> _$TaskCreatedHookToJson(TaskCreatedHook instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'task_id': instance.taskId,
      'task_subject': instance.taskSubject,
      if (instance.taskDescription case final value?) 'task_description': value,
      if (instance.teammateName case final value?) 'teammate_name': value,
      if (instance.teamName case final value?) 'team_name': value,
    };

TaskCompletedHook _$TaskCompletedHookFromJson(Map json) => TaskCompletedHook(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.taskCompleted,
  taskId: json['task_id'] as String,
  taskSubject: json['task_subject'] as String,
  taskDescription: json['task_description'] as String?,
  teammateName: json['teammate_name'] as String?,
  teamName: json['team_name'] as String?,
);

Map<String, dynamic> _$TaskCompletedHookToJson(TaskCompletedHook instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'task_id': instance.taskId,
      'task_subject': instance.taskSubject,
      if (instance.taskDescription case final value?) 'task_description': value,
      if (instance.teammateName case final value?) 'teammate_name': value,
      if (instance.teamName case final value?) 'team_name': value,
    };
