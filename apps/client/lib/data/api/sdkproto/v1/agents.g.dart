// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'agents.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SlashCommand _$SlashCommandFromJson(Map json) => SlashCommand(
  name: json['name'] as String,
  description: json['description'] as String?,
  argumentHint: json['argumentHint'] as String?,
  aliases: (json['aliases'] as List<dynamic>?)
      ?.map((e) => e as String)
      .toList(),
);

Map<String, dynamic> _$SlashCommandToJson(SlashCommand instance) =>
    <String, dynamic>{
      'name': instance.name,
      if (instance.description case final value?) 'description': value,
      if (instance.argumentHint case final value?) 'argumentHint': value,
      if (instance.aliases case final value?) 'aliases': value,
    };

AgentInfo _$AgentInfoFromJson(Map json) => AgentInfo(
  name: json['name'] as String,
  description: json['description'] as String?,
  model: json['model'] as String?,
  tools: (json['tools'] as List<dynamic>?)?.map((e) => e as String).toList(),
);

Map<String, dynamic> _$AgentInfoToJson(AgentInfo instance) => <String, dynamic>{
  'name': instance.name,
  if (instance.description case final value?) 'description': value,
  if (instance.model case final value?) 'model': value,
  if (instance.tools case final value?) 'tools': value,
};

AgentDefinition _$AgentDefinitionFromJson(Map json) => AgentDefinition(
  description: json['description'] as String,
  prompt: json['prompt'] as String,
  model: json['model'] as String?,
  tools: (json['tools'] as List<dynamic>?)?.map((e) => e as String).toList(),
  mcpServers: (json['mcp_servers'] as List<dynamic>?)
      ?.map((e) => e as String)
      .toList(),
  whenToUse: json['whenToUse'] as String?,
);

Map<String, dynamic> _$AgentDefinitionToJson(AgentDefinition instance) =>
    <String, dynamic>{
      'description': instance.description,
      'prompt': instance.prompt,
      if (instance.model case final value?) 'model': value,
      if (instance.tools case final value?) 'tools': value,
      if (instance.mcpServers case final value?) 'mcp_servers': value,
      if (instance.whenToUse case final value?) 'whenToUse': value,
    };

ModelInfo _$ModelInfoFromJson(Map json) => ModelInfo(
  id: json['id'] as String,
  displayName: json['displayName'] as String,
  contextWindow: (json['contextWindow'] as num?)?.toInt(),
  maxOutputTokens: (json['maxOutputTokens'] as num?)?.toInt(),
  defaultModel: json['defaultModel'] as bool?,
);

Map<String, dynamic> _$ModelInfoToJson(ModelInfo instance) => <String, dynamic>{
  'id': instance.id,
  'displayName': instance.displayName,
  if (instance.contextWindow case final value?) 'contextWindow': value,
  if (instance.maxOutputTokens case final value?) 'maxOutputTokens': value,
  if (instance.defaultModel case final value?) 'defaultModel': value,
};

AccountInfo _$AccountInfoFromJson(Map json) => AccountInfo(
  email: json['email'] as String?,
  organization: json['organization'] as String?,
);

Map<String, dynamic> _$AccountInfoToJson(AccountInfo instance) =>
    <String, dynamic>{
      if (instance.email case final value?) 'email': value,
      if (instance.organization case final value?) 'organization': value,
    };
