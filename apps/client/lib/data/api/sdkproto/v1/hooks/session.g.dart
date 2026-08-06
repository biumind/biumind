// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'session.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SessionStart _$SessionStartFromJson(Map json) => SessionStart(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.sessionStart,
  source: json['source'] as String,
  agentType: json['agent_type'] as String?,
  model: json['model'] as String?,
);

Map<String, dynamic> _$SessionStartToJson(SessionStart instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'source': instance.source,
      if (instance.agentType case final value?) 'agent_type': value,
      if (instance.model case final value?) 'model': value,
    };

SessionEnd _$SessionEndFromJson(Map json) => SessionEnd(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.sessionEnd,
  reason: json['reason'] as String,
);

Map<String, dynamic> _$SessionEndToJson(SessionEnd instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'reason': instance.reason,
    };

Stop _$StopFromJson(Map json) => Stop(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.stop,
  stopHookActive: json['stop_hook_active'] as bool,
  lastAssistantMessage: json['last_assistant_message'] as String?,
);

Map<String, dynamic> _$StopToJson(Stop instance) => <String, dynamic>{
  'session_id': instance.sessionId,
  'transcript_path': instance.transcriptPath,
  'cwd': instance.cwd,
  'hook_event_name': instance.hookEventName,
  'stop_hook_active': instance.stopHookActive,
  if (instance.lastAssistantMessage case final value?)
    'last_assistant_message': value,
};

StopFailure _$StopFailureFromJson(Map json) => StopFailure(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.stopFailure,
  error: json['error'] as String,
  errorDetails: json['error_details'],
  lastAssistantMessage: json['last_assistant_message'] as String?,
);

Map<String, dynamic> _$StopFailureToJson(StopFailure instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'error': instance.error,
      if (instance.errorDetails case final value?) 'error_details': value,
      if (instance.lastAssistantMessage case final value?)
        'last_assistant_message': value,
    };

Setup _$SetupFromJson(Map json) => Setup(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.setup,
  trigger: json['trigger'] as String,
);

Map<String, dynamic> _$SetupToJson(Setup instance) => <String, dynamic>{
  'session_id': instance.sessionId,
  'transcript_path': instance.transcriptPath,
  'cwd': instance.cwd,
  'hook_event_name': instance.hookEventName,
  'trigger': instance.trigger,
};
