// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'assistant.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKAssistantMessage _$SDKAssistantMessageFromJson(Map json) =>
    SDKAssistantMessage(
      type: json['type'] as String? ?? 'assistant',
      message: AnthropicMessage.fromJson(
        Map<String, dynamic>.from(json['message'] as Map),
      ),
      parentToolUseId: json['parent_tool_use_id'] as String?,
      error: json['error'] as String?,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKAssistantMessageToJson(
  SDKAssistantMessage instance,
) => <String, dynamic>{
  'type': instance.type,
  'message': instance.message,
  if (instance.parentToolUseId case final value?) 'parent_tool_use_id': value,
  if (instance.error case final value?) 'error': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKPartialAssistantMessage _$SDKPartialAssistantMessageFromJson(Map json) =>
    SDKPartialAssistantMessage(
      type: json['type'] as String? ?? 'stream_event',
      event: Map<String, dynamic>.from(json['event'] as Map),
      parentToolUseId: json['parent_tool_use_id'] as String?,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKPartialAssistantMessageToJson(
  SDKPartialAssistantMessage instance,
) => <String, dynamic>{
  'type': instance.type,
  'event': instance.event,
  if (instance.parentToolUseId case final value?) 'parent_tool_use_id': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};
