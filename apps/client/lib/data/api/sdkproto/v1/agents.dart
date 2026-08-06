// SlashCommand / AgentInfo / AgentDefinition / ModelInfo / AccountInfo

import 'package:json_annotation/json_annotation.dart';

part 'agents.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SlashCommand {
  final String name;
  final String? description;
  final String? argumentHint;
  final List<String>? aliases;

  SlashCommand({
    required this.name,
    this.description,
    this.argumentHint,
    this.aliases,
  });

  factory SlashCommand.fromJson(Map<String, dynamic> json) =>
      _$SlashCommandFromJson(json);
  Map<String, dynamic> toJson() => _$SlashCommandToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class AgentInfo {
  final String name;
  final String? description;
  final String? model;
  final List<String>? tools;

  AgentInfo({required this.name, this.description, this.model, this.tools});

  factory AgentInfo.fromJson(Map<String, dynamic> json) =>
      _$AgentInfoFromJson(json);
  Map<String, dynamic> toJson() => _$AgentInfoToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class AgentDefinition {
  final String description;
  final String prompt;
  final String? model;
  final List<String>? tools;
  @JsonKey(name: 'mcp_servers')
  final List<String>? mcpServers;
  final String? whenToUse;

  AgentDefinition({
    required this.description,
    required this.prompt,
    this.model,
    this.tools,
    this.mcpServers,
    this.whenToUse,
  });

  factory AgentDefinition.fromJson(Map<String, dynamic> json) =>
      _$AgentDefinitionFromJson(json);
  Map<String, dynamic> toJson() => _$AgentDefinitionToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ModelInfo {
  final String id;
  final String displayName;
  final int? contextWindow;
  final int? maxOutputTokens;
  final bool? defaultModel;

  ModelInfo({
    required this.id,
    required this.displayName,
    this.contextWindow,
    this.maxOutputTokens,
    this.defaultModel,
  });

  factory ModelInfo.fromJson(Map<String, dynamic> json) =>
      _$ModelInfoFromJson(json);
  Map<String, dynamic> toJson() => _$ModelInfoToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class AccountInfo {
  final String? email;
  final String? organization;

  AccountInfo({this.email, this.organization});

  factory AccountInfo.fromJson(Map<String, dynamic> json) =>
      _$AccountInfoFromJson(json);
  Map<String, dynamic> toJson() => _$AccountInfoToJson(this);
}
