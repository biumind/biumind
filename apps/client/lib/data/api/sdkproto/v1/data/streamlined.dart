// SDKStreamlinedText (type=streamlined_text) + SDKStreamlinedToolUseSummary

import 'package:json_annotation/json_annotation.dart';
import 'sdk_message.dart';

part 'streamlined.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKStreamlinedText extends SDKMessage {
  @override
  final String type;
  final String text;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKStreamlinedText({
    this.type = 'streamlined_text',
    required this.text,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKStreamlinedText.fromJson(Map<String, dynamic> json) =>
      _$SDKStreamlinedTextFromJson(json);
  Map<String, dynamic> toJson() => _$SDKStreamlinedTextToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKStreamlinedToolUseSummary extends SDKMessage {
  @override
  final String type;
  @JsonKey(name: 'tool_summary')
  final String toolSummary;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKStreamlinedToolUseSummary({
    this.type = 'streamlined_tool_use_summary',
    required this.toolSummary,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKStreamlinedToolUseSummary.fromJson(Map<String, dynamic> json) =>
      _$SDKStreamlinedToolUseSummaryFromJson(json);
  Map<String, dynamic> toJson() => _$SDKStreamlinedToolUseSummaryToJson(this);
}
