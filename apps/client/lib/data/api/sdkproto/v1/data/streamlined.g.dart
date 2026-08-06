// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'streamlined.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKStreamlinedText _$SDKStreamlinedTextFromJson(Map json) => SDKStreamlinedText(
  type: json['type'] as String? ?? 'streamlined_text',
  text: json['text'] as String,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKStreamlinedTextToJson(SDKStreamlinedText instance) =>
    <String, dynamic>{
      'type': instance.type,
      'text': instance.text,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKStreamlinedToolUseSummary _$SDKStreamlinedToolUseSummaryFromJson(Map json) =>
    SDKStreamlinedToolUseSummary(
      type: json['type'] as String? ?? 'streamlined_tool_use_summary',
      toolSummary: json['tool_summary'] as String,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKStreamlinedToolUseSummaryToJson(
  SDKStreamlinedToolUseSummary instance,
) => <String, dynamic>{
  'type': instance.type,
  'tool_summary': instance.toolSummary,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};
