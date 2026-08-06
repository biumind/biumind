// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'tool.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKToolProgress _$SDKToolProgressFromJson(Map json) => SDKToolProgress(
  type: json['type'] as String? ?? 'tool_progress',
  toolUseId: json['tool_use_id'] as String,
  toolName: json['tool_name'] as String,
  parentToolUseId: json['parent_tool_use_id'] as String?,
  elapsedTimeSeconds: (json['elapsed_time_seconds'] as num).toDouble(),
  taskId: json['task_id'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKToolProgressToJson(
  SDKToolProgress instance,
) => <String, dynamic>{
  'type': instance.type,
  'tool_use_id': instance.toolUseId,
  'tool_name': instance.toolName,
  if (instance.parentToolUseId case final value?) 'parent_tool_use_id': value,
  'elapsed_time_seconds': instance.elapsedTimeSeconds,
  if (instance.taskId case final value?) 'task_id': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKToolUseSummary _$SDKToolUseSummaryFromJson(Map json) => SDKToolUseSummary(
  type: json['type'] as String? ?? 'tool_use_summary',
  summary: json['summary'] as String,
  precedingToolUseIds: (json['preceding_tool_use_ids'] as List<dynamic>)
      .map((e) => e as String)
      .toList(),
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKToolUseSummaryToJson(SDKToolUseSummary instance) =>
    <String, dynamic>{
      'type': instance.type,
      'summary': instance.summary,
      'preceding_tool_use_ids': instance.precedingToolUseIds,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };
