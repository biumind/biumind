// SDKUserMessage / SDKUserMessageReplay (type=user, isReplay=true 时为 replay)

import 'package:json_annotation/json_annotation.dart';

import '../common.dart';
import 'sdk_message.dart';

part 'user.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKUserMessage extends SDKMessage {
  @override
  final String type;
  final AnthropicMessage message;
  @JsonKey(name: 'parent_tool_use_id')
  final String? parentToolUseId;
  final bool? isSynthetic;
  @JsonKey(name: 'tool_use_result')
  final dynamic toolUseResult;
  final String? priority;
  final int? timestamp;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  final bool? isReplay;

  SDKUserMessage({
    this.type = 'user',
    required this.message,
    this.parentToolUseId,
    this.isSynthetic,
    this.toolUseResult,
    this.priority,
    this.timestamp,
    this.uuid = '',
    this.sessionId = '',
    this.isReplay,
  });

  factory SDKUserMessage.fromJson(Map<String, dynamic> json) =>
      _$SDKUserMessageFromJson(json);
  Map<String, dynamic> toJson() => _$SDKUserMessageToJson(this);
}
