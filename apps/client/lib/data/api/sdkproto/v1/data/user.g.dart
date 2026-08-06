// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'user.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKUserMessage _$SDKUserMessageFromJson(Map json) => SDKUserMessage(
  type: json['type'] as String? ?? 'user',
  message: AnthropicMessage.fromJson(
    Map<String, dynamic>.from(json['message'] as Map),
  ),
  parentToolUseId: json['parent_tool_use_id'] as String?,
  isSynthetic: json['isSynthetic'] as bool?,
  toolUseResult: json['tool_use_result'],
  priority: json['priority'] as String?,
  timestamp: (json['timestamp'] as num?)?.toInt(),
  uuid: json['uuid'] as String? ?? '',
  sessionId: json['session_id'] as String? ?? '',
  isReplay: json['isReplay'] as bool?,
);

Map<String, dynamic> _$SDKUserMessageToJson(
  SDKUserMessage instance,
) => <String, dynamic>{
  'type': instance.type,
  'message': instance.message,
  if (instance.parentToolUseId case final value?) 'parent_tool_use_id': value,
  if (instance.isSynthetic case final value?) 'isSynthetic': value,
  if (instance.toolUseResult case final value?) 'tool_use_result': value,
  if (instance.priority case final value?) 'priority': value,
  if (instance.timestamp case final value?) 'timestamp': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
  if (instance.isReplay case final value?) 'isReplay': value,
};
