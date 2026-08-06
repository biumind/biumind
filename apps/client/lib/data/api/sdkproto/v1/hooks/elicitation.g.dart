// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'elicitation.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

ElicitationHook _$ElicitationHookFromJson(Map json) => ElicitationHook(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.elicitation,
  mcpServerName: json['mcp_server_name'] as String,
  message: json['message'] as String,
  mode: json['mode'] as String?,
  url: json['url'] as String?,
  elicitationId: json['elicitation_id'] as String?,
  requestedSchema: (json['requested_schema'] as Map?)?.map(
    (k, e) => MapEntry(k as String, e),
  ),
);

Map<String, dynamic> _$ElicitationHookToJson(ElicitationHook instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'mcp_server_name': instance.mcpServerName,
      'message': instance.message,
      if (instance.mode case final value?) 'mode': value,
      if (instance.url case final value?) 'url': value,
      if (instance.elicitationId case final value?) 'elicitation_id': value,
      if (instance.requestedSchema case final value?) 'requested_schema': value,
    };

ElicitationResultHook _$ElicitationResultHookFromJson(Map json) =>
    ElicitationResultHook(
      sessionId: json['session_id'] as String,
      transcriptPath: json['transcript_path'] as String,
      cwd: json['cwd'] as String,
      hookEventName:
          json['hook_event_name'] as String? ?? HookEvent.elicitationResult,
      mcpServerName: json['mcp_server_name'] as String,
      elicitationId: json['elicitation_id'] as String?,
      mode: json['mode'] as String?,
      action: json['action'] as String,
      content: (json['content'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e),
      ),
    );

Map<String, dynamic> _$ElicitationResultHookToJson(
  ElicitationResultHook instance,
) => <String, dynamic>{
  'session_id': instance.sessionId,
  'transcript_path': instance.transcriptPath,
  'cwd': instance.cwd,
  'hook_event_name': instance.hookEventName,
  'mcp_server_name': instance.mcpServerName,
  if (instance.elicitationId case final value?) 'elicitation_id': value,
  if (instance.mode case final value?) 'mode': value,
  'action': instance.action,
  if (instance.content case final value?) 'content': value,
};
