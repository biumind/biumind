// 5 个 McpServerConfig 类型 + McpServerStatus

import 'package:json_annotation/json_annotation.dart';

part 'mcp.g.dart';

abstract class McpServerConfig {
  String get type;
  Map<String, dynamic> toJson();

  static McpServerConfig fromJson(Map<String, dynamic> json) {
    final t = json['type'] as String? ?? 'stdio';
    switch (t) {
      case 'stdio':
      case '':
        return McpStdioServerConfig.fromJson(json);
      case 'sse':
        return McpSSEServerConfig.fromJson(json);
      case 'http':
        return McpHttpServerConfig.fromJson(json);
      case 'sdk':
        return McpSdkServerConfig.fromJson(json);
      case 'claudeai-proxy':
        return McpClaudeAIProxyServerConfig.fromJson(json);
      default:
        throw ArgumentError('unknown McpServerConfig type: $t');
    }
  }
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpStdioServerConfig implements McpServerConfig {
  @override
  final String type;
  final String command;
  final List<String>? args;
  final Map<String, String>? env;

  McpStdioServerConfig({
    this.type = 'stdio',
    required this.command,
    this.args,
    this.env,
  });

  factory McpStdioServerConfig.fromJson(Map<String, dynamic> json) =>
      _$McpStdioServerConfigFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$McpStdioServerConfigToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpSSEServerConfig implements McpServerConfig {
  @override
  final String type;
  final String url;
  final Map<String, String>? headers;

  McpSSEServerConfig({this.type = 'sse', required this.url, this.headers});
  factory McpSSEServerConfig.fromJson(Map<String, dynamic> json) =>
      _$McpSSEServerConfigFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$McpSSEServerConfigToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpHttpServerConfig implements McpServerConfig {
  @override
  final String type;
  final String url;
  final Map<String, String>? headers;

  McpHttpServerConfig({this.type = 'http', required this.url, this.headers});
  factory McpHttpServerConfig.fromJson(Map<String, dynamic> json) =>
      _$McpHttpServerConfigFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$McpHttpServerConfigToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpSdkServerConfig implements McpServerConfig {
  @override
  final String type;
  final String name;
  final dynamic instance;

  McpSdkServerConfig({this.type = 'sdk', required this.name, this.instance});
  factory McpSdkServerConfig.fromJson(Map<String, dynamic> json) =>
      _$McpSdkServerConfigFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$McpSdkServerConfigToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpClaudeAIProxyServerConfig implements McpServerConfig {
  @override
  final String type;
  final String url;
  final Map<String, String>? headers;

  McpClaudeAIProxyServerConfig({
    this.type = 'claudeai-proxy',
    required this.url,
    this.headers,
  });
  factory McpClaudeAIProxyServerConfig.fromJson(Map<String, dynamic> json) =>
      _$McpClaudeAIProxyServerConfigFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$McpClaudeAIProxyServerConfigToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpServerInfo {
  final String? name;
  final String? version;

  McpServerInfo({this.name, this.version});

  factory McpServerInfo.fromJson(Map<String, dynamic> json) =>
      _$McpServerInfoFromJson(json);
  Map<String, dynamic> toJson() => _$McpServerInfoToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpServerStatus {
  final String name;
  final String status;
  final McpServerInfo? serverInfo;
  final String? error;

  McpServerStatus({
    required this.name,
    required this.status,
    this.serverInfo,
    this.error,
  });

  factory McpServerStatus.fromJson(Map<String, dynamic> json) =>
      _$McpServerStatusFromJson(json);
  Map<String, dynamic> toJson() => _$McpServerStatusToJson(this);
}
