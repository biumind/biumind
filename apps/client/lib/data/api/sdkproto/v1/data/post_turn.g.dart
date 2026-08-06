// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'post_turn.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKPostTurnSummary _$SDKPostTurnSummaryFromJson(Map json) => SDKPostTurnSummary(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'post_turn_summary',
  summarizesUuid: json['summarizes_uuid'] as String,
  statusCategory: json['status_category'] as String,
  statusDetail: json['status_detail'] as String,
  isNoteworthy: json['is_noteworthy'] as bool,
  title: json['title'] as String,
  description: json['description'] as String,
  recentAction: json['recent_action'] as String,
  needsAction: json['needs_action'] as bool,
  artifactUrls: (json['artifact_urls'] as List<dynamic>)
      .map((e) => e as String)
      .toList(),
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKPostTurnSummaryToJson(SDKPostTurnSummary instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'summarizes_uuid': instance.summarizesUuid,
      'status_category': instance.statusCategory,
      'status_detail': instance.statusDetail,
      'is_noteworthy': instance.isNoteworthy,
      'title': instance.title,
      'description': instance.description,
      'recent_action': instance.recentAction,
      'needs_action': instance.needsAction,
      'artifact_urls': instance.artifactUrls,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };
