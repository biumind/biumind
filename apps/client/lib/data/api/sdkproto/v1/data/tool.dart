// SDKToolProgress (type=tool_progress) + SDKToolUseSummary (type=tool_use_summary)

import 'package:json_annotation/json_annotation.dart';
import 'sdk_message.dart';

part 'tool.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKToolProgress extends SDKMessage {
  @override
  final String type;
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;
  @JsonKey(name: 'tool_name')
  final String toolName;
  @JsonKey(name: 'parent_tool_use_id')
  final String? parentToolUseId;
  @JsonKey(name: 'elapsed_time_seconds')
  final double elapsedTimeSeconds;
  @JsonKey(name: 'task_id')
  final String? taskId;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKToolProgress({
    this.type = 'tool_progress',
    required this.toolUseId,
    required this.toolName,
    this.parentToolUseId,
    required this.elapsedTimeSeconds,
    this.taskId,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKToolProgress.fromJson(Map<String, dynamic> json) =>
      _$SDKToolProgressFromJson(json);
  Map<String, dynamic> toJson() => _$SDKToolProgressToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKToolUseSummary extends SDKMessage {
  @override
  final String type;
  final String summary;
  @JsonKey(name: 'preceding_tool_use_ids')
  final List<String> precedingToolUseIds;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKToolUseSummary({
    this.type = 'tool_use_summary',
    required this.summary,
    required this.precedingToolUseIds,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKToolUseSummary.fromJson(Map<String, dynamic> json) =>
      _$SDKToolUseSummaryFromJson(json);
  Map<String, dynamic> toJson() => _$SDKToolUseSummaryToJson(this);
}
