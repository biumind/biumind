// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'subagent.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SubagentStart _$SubagentStartFromJson(Map json) => SubagentStart(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.subagentStart,
  agentId: json['agent_id'] as String,
  agentType: json['agent_type'] as String,
);

Map<String, dynamic> _$SubagentStartToJson(SubagentStart instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'agent_id': instance.agentId,
      'agent_type': instance.agentType,
    };

SubagentStop _$SubagentStopFromJson(Map json) => SubagentStop(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.subagentStop,
  stopHookActive: json['stop_hook_active'] as bool,
  agentId: json['agent_id'] as String,
  agentTranscriptPath: json['agent_transcript_path'] as String,
  agentType: json['agent_type'] as String,
  lastAssistantMessage: json['last_assistant_message'] as String?,
);

Map<String, dynamic> _$SubagentStopToJson(SubagentStop instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'stop_hook_active': instance.stopHookActive,
      'agent_id': instance.agentId,
      'agent_transcript_path': instance.agentTranscriptPath,
      'agent_type': instance.agentType,
      if (instance.lastAssistantMessage case final value?)
        'last_assistant_message': value,
    };

TeammateIdle _$TeammateIdleFromJson(Map json) => TeammateIdle(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.teammateIdle,
  teammateName: json['teammate_name'] as String,
  teamName: json['team_name'] as String,
);

Map<String, dynamic> _$TeammateIdleToJson(TeammateIdle instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'teammate_name': instance.teammateName,
      'team_name': instance.teamName,
    };
