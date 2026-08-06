// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'reload.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

ReloadPlugins _$ReloadPluginsFromJson(Map json) =>
    ReloadPlugins(subtype: json['subtype'] as String? ?? 'reload_plugins');

Map<String, dynamic> _$ReloadPluginsToJson(ReloadPlugins instance) =>
    <String, dynamic>{'subtype': instance.subtype};

ReloadPluginsResponse _$ReloadPluginsResponseFromJson(Map json) =>
    ReloadPluginsResponse(
      commands: json['commands'] as List<dynamic>?,
      agents: json['agents'] as List<dynamic>?,
      plugins: json['plugins'] as List<dynamic>?,
      mcpServers: json['mcpServers'] as List<dynamic>?,
      errorCount: (json['error_count'] as num).toInt(),
    );

Map<String, dynamic> _$ReloadPluginsResponseToJson(
  ReloadPluginsResponse instance,
) => <String, dynamic>{
  if (instance.commands case final value?) 'commands': value,
  if (instance.agents case final value?) 'agents': value,
  if (instance.plugins case final value?) 'plugins': value,
  if (instance.mcpServers case final value?) 'mcpServers': value,
  'error_count': instance.errorCount,
};
