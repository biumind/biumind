// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'tool_use.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

PreToolUse _$PreToolUseFromJson(Map json) => PreToolUse(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.preToolUse,
  permissionMode: json['permission_mode'] as String?,
  toolName: json['tool_name'] as String,
  toolInput: json['tool_input'],
  toolUseId: json['tool_use_id'] as String,
);

Map<String, dynamic> _$PreToolUseToJson(PreToolUse instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      if (instance.permissionMode case final value?) 'permission_mode': value,
      'tool_name': instance.toolName,
      if (instance.toolInput case final value?) 'tool_input': value,
      'tool_use_id': instance.toolUseId,
    };

PostToolUse _$PostToolUseFromJson(Map json) => PostToolUse(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.postToolUse,
  toolName: json['tool_name'] as String,
  toolInput: json['tool_input'],
  toolResponse: json['tool_response'],
  toolUseId: json['tool_use_id'] as String,
);

Map<String, dynamic> _$PostToolUseToJson(PostToolUse instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'tool_name': instance.toolName,
      if (instance.toolInput case final value?) 'tool_input': value,
      if (instance.toolResponse case final value?) 'tool_response': value,
      'tool_use_id': instance.toolUseId,
    };

PostToolUseFailure _$PostToolUseFailureFromJson(Map json) => PostToolUseFailure(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName:
      json['hook_event_name'] as String? ?? HookEvent.postToolUseFailure,
  toolName: json['tool_name'] as String,
  toolInput: json['tool_input'],
  toolUseId: json['tool_use_id'] as String,
  error: json['error'] as String,
  isInterrupt: json['is_interrupt'] as bool?,
);

Map<String, dynamic> _$PostToolUseFailureToJson(PostToolUseFailure instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'tool_name': instance.toolName,
      if (instance.toolInput case final value?) 'tool_input': value,
      'tool_use_id': instance.toolUseId,
      'error': instance.error,
      if (instance.isInterrupt case final value?) 'is_interrupt': value,
    };

PermissionDenied _$PermissionDeniedFromJson(Map json) => PermissionDenied(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName:
      json['hook_event_name'] as String? ?? HookEvent.permissionDenied,
  toolName: json['tool_name'] as String,
  toolInput: json['tool_input'],
  toolUseId: json['tool_use_id'] as String,
  reason: json['reason'] as String,
);

Map<String, dynamic> _$PermissionDeniedToJson(PermissionDenied instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'tool_name': instance.toolName,
      if (instance.toolInput case final value?) 'tool_input': value,
      'tool_use_id': instance.toolUseId,
      'reason': instance.reason,
    };
