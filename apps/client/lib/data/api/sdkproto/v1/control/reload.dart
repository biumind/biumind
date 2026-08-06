import 'package:json_annotation/json_annotation.dart';

part 'reload.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class ReloadPlugins {
  final String subtype;
  ReloadPlugins({this.subtype = 'reload_plugins'});
  factory ReloadPlugins.fromJson(Map<String, dynamic> json) =>
      _$ReloadPluginsFromJson(json);
  Map<String, dynamic> toJson() => _$ReloadPluginsToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ReloadPluginsResponse {
  final List<dynamic>? commands;
  final List<dynamic>? agents;
  final List<dynamic>? plugins;
  final List<dynamic>? mcpServers;
  @JsonKey(name: 'error_count')
  final int errorCount;

  ReloadPluginsResponse({
    this.commands,
    this.agents,
    this.plugins,
    this.mcpServers,
    required this.errorCount,
  });

  factory ReloadPluginsResponse.fromJson(Map<String, dynamic> json) =>
      _$ReloadPluginsResponseFromJson(json);
  Map<String, dynamic> toJson() => _$ReloadPluginsResponseToJson(this);
}
