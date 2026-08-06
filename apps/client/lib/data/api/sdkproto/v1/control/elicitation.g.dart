// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'elicitation.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

Elicitation _$ElicitationFromJson(Map json) => Elicitation(
  subtype: json['subtype'] as String? ?? 'elicitation',
  mcpServerName: json['mcp_server_name'] as String,
  message: json['message'] as String,
  mode: json['mode'] as String?,
  url: json['url'] as String?,
  elicitationId: json['elicitation_id'] as String?,
  requestedSchema: (json['requested_schema'] as Map?)?.map(
    (k, e) => MapEntry(k as String, e),
  ),
);

Map<String, dynamic> _$ElicitationToJson(Elicitation instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'mcp_server_name': instance.mcpServerName,
      'message': instance.message,
      if (instance.mode case final value?) 'mode': value,
      if (instance.url case final value?) 'url': value,
      if (instance.elicitationId case final value?) 'elicitation_id': value,
      if (instance.requestedSchema case final value?) 'requested_schema': value,
    };

ElicitationResponse _$ElicitationResponseFromJson(Map json) =>
    ElicitationResponse(
      action: json['action'] as String,
      content: (json['content'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e),
      ),
    );

Map<String, dynamic> _$ElicitationResponseToJson(
  ElicitationResponse instance,
) => <String, dynamic>{
  'action': instance.action,
  if (instance.content case final value?) 'content': value,
};
