// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'result.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKResultSuccess _$SDKResultSuccessFromJson(Map json) => SDKResultSuccess(
  type: json['type'] as String? ?? 'result',
  subtype: json['subtype'] as String? ?? 'success',
  durationMs: (json['duration_ms'] as num).toInt(),
  durationApiMs: (json['duration_api_ms'] as num).toInt(),
  isError: json['is_error'] as bool? ?? false,
  numTurns: (json['num_turns'] as num).toInt(),
  result: json['result'] as String,
  stopReason: json['stop_reason'] as String?,
  totalCostUsd: (json['total_cost_usd'] as num).toDouble(),
  usage: json['usage'],
  modelUsage: (json['modelUsage'] as Map).map(
    (k, e) => MapEntry(
      k as String,
      ModelUsage.fromJson(Map<String, dynamic>.from(e as Map)),
    ),
  ),
  permissionDenials: json['permission_denials'] as List<dynamic>,
  structuredOutput: json['structured_output'],
  fastModeState: json['fast_mode_state'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKResultSuccessToJson(
  SDKResultSuccess instance,
) => <String, dynamic>{
  'type': instance.type,
  'subtype': instance.subtype,
  'duration_ms': instance.durationMs,
  'duration_api_ms': instance.durationApiMs,
  'is_error': instance.isError,
  'num_turns': instance.numTurns,
  'result': instance.result,
  if (instance.stopReason case final value?) 'stop_reason': value,
  'total_cost_usd': instance.totalCostUsd,
  if (instance.usage case final value?) 'usage': value,
  'modelUsage': instance.modelUsage,
  'permission_denials': instance.permissionDenials,
  if (instance.structuredOutput case final value?) 'structured_output': value,
  if (instance.fastModeState case final value?) 'fast_mode_state': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKResultError _$SDKResultErrorFromJson(Map json) => SDKResultError(
  type: json['type'] as String? ?? 'result',
  subtype: json['subtype'] as String,
  durationMs: (json['duration_ms'] as num).toInt(),
  durationApiMs: (json['duration_api_ms'] as num).toInt(),
  isError: json['is_error'] as bool? ?? true,
  numTurns: (json['num_turns'] as num).toInt(),
  totalCostUsd: (json['total_cost_usd'] as num).toDouble(),
  usage: json['usage'],
  modelUsage: (json['modelUsage'] as Map).map(
    (k, e) => MapEntry(
      k as String,
      ModelUsage.fromJson(Map<String, dynamic>.from(e as Map)),
    ),
  ),
  permissionDenials: json['permission_denials'] as List<dynamic>,
  errors: json['errors'] as List<dynamic>,
  stopReason: json['stop_reason'] as String?,
  fastModeState: json['fast_mode_state'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKResultErrorToJson(SDKResultError instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'duration_ms': instance.durationMs,
      'duration_api_ms': instance.durationApiMs,
      'is_error': instance.isError,
      'num_turns': instance.numTurns,
      'total_cost_usd': instance.totalCostUsd,
      if (instance.usage case final value?) 'usage': value,
      'modelUsage': instance.modelUsage,
      'permission_denials': instance.permissionDenials,
      'errors': instance.errors,
      if (instance.stopReason case final value?) 'stop_reason': value,
      if (instance.fastModeState case final value?) 'fast_mode_state': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };
