// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'initialize.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

Initialize _$InitializeFromJson(Map json) => Initialize(
  subtype: json['subtype'] as String? ?? 'initialize',
  hooks: json['hooks'],
  sdkMcpServers: json['sdkMcpServers'],
  jsonSchema: json['jsonSchema'],
  systemPrompt: json['systemPrompt'] as String?,
  appendSystemPrompt: json['appendSystemPrompt'] as String?,
  agents: json['agents'],
  promptSuggestions: json['promptSuggestions'] as bool?,
  agentProgressSummaries: json['agentProgressSummaries'] as bool?,
);

Map<String, dynamic> _$InitializeToJson(
  Initialize instance,
) => <String, dynamic>{
  'subtype': instance.subtype,
  if (instance.hooks case final value?) 'hooks': value,
  if (instance.sdkMcpServers case final value?) 'sdkMcpServers': value,
  if (instance.jsonSchema case final value?) 'jsonSchema': value,
  if (instance.systemPrompt case final value?) 'systemPrompt': value,
  if (instance.appendSystemPrompt case final value?)
    'appendSystemPrompt': value,
  if (instance.agents case final value?) 'agents': value,
  if (instance.promptSuggestions case final value?) 'promptSuggestions': value,
  if (instance.agentProgressSummaries case final value?)
    'agentProgressSummaries': value,
};
