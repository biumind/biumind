// SDKAssistantMessage (type=assistant) + SDKPartialAssistantMessage (type=stream_event)

import 'package:json_annotation/json_annotation.dart';

import '../common.dart';
import 'sdk_message.dart';

part 'assistant.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKAssistantMessage extends SDKMessage {
  @override
  final String type;
  final AnthropicMessage message;
  @JsonKey(name: 'parent_tool_use_id')
  final String? parentToolUseId;
  final String? error;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKAssistantMessage({
    this.type = 'assistant',
    required this.message,
    this.parentToolUseId,
    this.error,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKAssistantMessage.fromJson(Map<String, dynamic> json) =>
      _$SDKAssistantMessageFromJson(json);
  Map<String, dynamic> toJson() => _$SDKAssistantMessageToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKPartialAssistantMessage extends SDKMessage {
  @override
  final String type;
  final Map<String, dynamic> event;
  @JsonKey(name: 'parent_tool_use_id')
  final String? parentToolUseId;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKPartialAssistantMessage({
    this.type = 'stream_event',
    required this.event,
    this.parentToolUseId,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKPartialAssistantMessage.fromJson(Map<String, dynamic> json) =>
      _$SDKPartialAssistantMessageFromJson(json);
  Map<String, dynamic> toJson() => _$SDKPartialAssistantMessageToJson(this);
}
