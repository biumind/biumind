// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'notification.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

NotificationHook _$NotificationHookFromJson(Map json) => NotificationHook(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.notification,
  message: json['message'] as String,
  title: json['title'] as String?,
  notificationType: json['notification_type'] as String,
);

Map<String, dynamic> _$NotificationHookToJson(NotificationHook instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'message': instance.message,
      if (instance.title case final value?) 'title': value,
      'notification_type': instance.notificationType,
    };

UserPromptSubmit _$UserPromptSubmitFromJson(Map json) => UserPromptSubmit(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName:
      json['hook_event_name'] as String? ?? HookEvent.userPromptSubmit,
  prompt: json['prompt'] as String,
);

Map<String, dynamic> _$UserPromptSubmitToJson(UserPromptSubmit instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'prompt': instance.prompt,
    };
