// SDKResultSuccess + SDKResultError (type=result, subtype 区分)

import 'package:json_annotation/json_annotation.dart';

import '../common.dart';
import 'sdk_message.dart';

part 'result.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKResultSuccess extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'duration_ms')
  final int durationMs;
  @JsonKey(name: 'duration_api_ms')
  final int durationApiMs;
  @JsonKey(name: 'is_error')
  final bool isError;
  @JsonKey(name: 'num_turns')
  final int numTurns;
  final String result;
  @JsonKey(name: 'stop_reason')
  final String? stopReason;
  @JsonKey(name: 'total_cost_usd')
  final double totalCostUsd;
  final dynamic usage;
  final Map<String, ModelUsage> modelUsage;
  @JsonKey(name: 'permission_denials')
  final List<dynamic> permissionDenials;
  @JsonKey(name: 'structured_output')
  final dynamic structuredOutput;
  @JsonKey(name: 'fast_mode_state')
  final String? fastModeState;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKResultSuccess({
    this.type = 'result',
    this.subtype = 'success',
    required this.durationMs,
    required this.durationApiMs,
    this.isError = false,
    required this.numTurns,
    required this.result,
    this.stopReason,
    required this.totalCostUsd,
    required this.usage,
    required this.modelUsage,
    required this.permissionDenials,
    this.structuredOutput,
    this.fastModeState,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKResultSuccess.fromJson(Map<String, dynamic> json) =>
      _$SDKResultSuccessFromJson(json);
  Map<String, dynamic> toJson() => _$SDKResultSuccessToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKResultError extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'duration_ms')
  final int durationMs;
  @JsonKey(name: 'duration_api_ms')
  final int durationApiMs;
  @JsonKey(name: 'is_error')
  final bool isError;
  @JsonKey(name: 'num_turns')
  final int numTurns;
  @JsonKey(name: 'total_cost_usd')
  final double totalCostUsd;
  final dynamic usage;
  final Map<String, ModelUsage> modelUsage;
  @JsonKey(name: 'permission_denials')
  final List<dynamic> permissionDenials;
  final List<dynamic> errors;
  @JsonKey(name: 'stop_reason')
  final String? stopReason;
  @JsonKey(name: 'fast_mode_state')
  final String? fastModeState;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKResultError({
    this.type = 'result',
    required this.subtype,
    required this.durationMs,
    required this.durationApiMs,
    this.isError = true,
    required this.numTurns,
    required this.totalCostUsd,
    required this.usage,
    required this.modelUsage,
    required this.permissionDenials,
    required this.errors,
    this.stopReason,
    this.fastModeState,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKResultError.fromJson(Map<String, dynamic> json) =>
      _$SDKResultErrorFromJson(json);
  Map<String, dynamic> toJson() => _$SDKResultErrorToJson(this);
}
