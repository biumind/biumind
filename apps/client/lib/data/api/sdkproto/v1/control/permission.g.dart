// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'permission.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

PermissionRequest _$PermissionRequestFromJson(Map json) => PermissionRequest(
  subtype: json['subtype'] as String? ?? 'can_use_tool',
  toolName: json['tool_name'] as String,
  input: json['input'],
  permissionSuggestions: json['permission_suggestions'] as List<dynamic>?,
  blockedPath: json['blocked_path'] as String?,
  decisionReason: json['decision_reason'] as String?,
  title: json['title'] as String?,
  displayName: json['display_name'] as String?,
  toolUseId: json['tool_use_id'] as String,
  agentId: json['agent_id'] as String?,
  description: json['description'] as String?,
);

Map<String, dynamic> _$PermissionRequestToJson(PermissionRequest instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'tool_name': instance.toolName,
      if (instance.input case final value?) 'input': value,
      if (instance.permissionSuggestions case final value?)
        'permission_suggestions': value,
      if (instance.blockedPath case final value?) 'blocked_path': value,
      if (instance.decisionReason case final value?) 'decision_reason': value,
      if (instance.title case final value?) 'title': value,
      if (instance.displayName case final value?) 'display_name': value,
      'tool_use_id': instance.toolUseId,
      if (instance.agentId case final value?) 'agent_id': value,
      if (instance.description case final value?) 'description': value,
    };
