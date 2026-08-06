// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'permission.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

PermissionRequestHook _$PermissionRequestHookFromJson(Map json) =>
    PermissionRequestHook(
      sessionId: json['session_id'] as String,
      transcriptPath: json['transcript_path'] as String,
      cwd: json['cwd'] as String,
      hookEventName:
          json['hook_event_name'] as String? ?? HookEvent.permissionRequest,
      toolName: json['tool_name'] as String,
      toolInput: json['tool_input'],
      permissionSuggestions: json['permission_suggestions'] as List<dynamic>?,
    );

Map<String, dynamic> _$PermissionRequestHookToJson(
  PermissionRequestHook instance,
) => <String, dynamic>{
  'session_id': instance.sessionId,
  'transcript_path': instance.transcriptPath,
  'cwd': instance.cwd,
  'hook_event_name': instance.hookEventName,
  'tool_name': instance.toolName,
  if (instance.toolInput case final value?) 'tool_input': value,
  if (instance.permissionSuggestions case final value?)
    'permission_suggestions': value,
};
