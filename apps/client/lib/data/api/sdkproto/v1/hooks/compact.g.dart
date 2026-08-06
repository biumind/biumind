// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'compact.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

PreCompact _$PreCompactFromJson(Map json) => PreCompact(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.preCompact,
  trigger: json['trigger'] as String,
  customInstructions: json['custom_instructions'] as String,
);

Map<String, dynamic> _$PreCompactToJson(PreCompact instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'trigger': instance.trigger,
      'custom_instructions': instance.customInstructions,
    };

PostCompact _$PostCompactFromJson(Map json) => PostCompact(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.postCompact,
  trigger: json['trigger'] as String,
  compactSummary: json['compact_summary'] as String,
);

Map<String, dynamic> _$PostCompactToJson(PostCompact instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'trigger': instance.trigger,
      'compact_summary': instance.compactSummary,
    };
