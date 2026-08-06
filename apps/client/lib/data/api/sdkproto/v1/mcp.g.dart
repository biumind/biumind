// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'mcp.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

McpStdioServerConfig _$McpStdioServerConfigFromJson(Map json) =>
    McpStdioServerConfig(
      type: json['type'] as String? ?? 'stdio',
      command: json['command'] as String,
      args: (json['args'] as List<dynamic>?)?.map((e) => e as String).toList(),
      env: (json['env'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e as String),
      ),
    );

Map<String, dynamic> _$McpStdioServerConfigToJson(
  McpStdioServerConfig instance,
) => <String, dynamic>{
  'type': instance.type,
  'command': instance.command,
  if (instance.args case final value?) 'args': value,
  if (instance.env case final value?) 'env': value,
};

McpSSEServerConfig _$McpSSEServerConfigFromJson(Map json) => McpSSEServerConfig(
  type: json['type'] as String? ?? 'sse',
  url: json['url'] as String,
  headers: (json['headers'] as Map?)?.map(
    (k, e) => MapEntry(k as String, e as String),
  ),
);

Map<String, dynamic> _$McpSSEServerConfigToJson(McpSSEServerConfig instance) =>
    <String, dynamic>{
      'type': instance.type,
      'url': instance.url,
      if (instance.headers case final value?) 'headers': value,
    };

McpHttpServerConfig _$McpHttpServerConfigFromJson(Map json) =>
    McpHttpServerConfig(
      type: json['type'] as String? ?? 'http',
      url: json['url'] as String,
      headers: (json['headers'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e as String),
      ),
    );

Map<String, dynamic> _$McpHttpServerConfigToJson(
  McpHttpServerConfig instance,
) => <String, dynamic>{
  'type': instance.type,
  'url': instance.url,
  if (instance.headers case final value?) 'headers': value,
};

McpSdkServerConfig _$McpSdkServerConfigFromJson(Map json) => McpSdkServerConfig(
  type: json['type'] as String? ?? 'sdk',
  name: json['name'] as String,
  instance: json['instance'],
);

Map<String, dynamic> _$McpSdkServerConfigToJson(McpSdkServerConfig instance) =>
    <String, dynamic>{
      'type': instance.type,
      'name': instance.name,
      if (instance.instance case final value?) 'instance': value,
    };

McpClaudeAIProxyServerConfig _$McpClaudeAIProxyServerConfigFromJson(Map json) =>
    McpClaudeAIProxyServerConfig(
      type: json['type'] as String? ?? 'claudeai-proxy',
      url: json['url'] as String,
      headers: (json['headers'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e as String),
      ),
    );

Map<String, dynamic> _$McpClaudeAIProxyServerConfigToJson(
  McpClaudeAIProxyServerConfig instance,
) => <String, dynamic>{
  'type': instance.type,
  'url': instance.url,
  if (instance.headers case final value?) 'headers': value,
};

McpServerInfo _$McpServerInfoFromJson(Map json) => McpServerInfo(
  name: json['name'] as String?,
  version: json['version'] as String?,
);

Map<String, dynamic> _$McpServerInfoToJson(McpServerInfo instance) =>
    <String, dynamic>{
      if (instance.name case final value?) 'name': value,
      if (instance.version case final value?) 'version': value,
    };

McpServerStatus _$McpServerStatusFromJson(Map json) => McpServerStatus(
  name: json['name'] as String,
  status: json['status'] as String,
  serverInfo: json['serverInfo'] == null
      ? null
      : McpServerInfo.fromJson(
          Map<String, dynamic>.from(json['serverInfo'] as Map),
        ),
  error: json['error'] as String?,
);

Map<String, dynamic> _$McpServerStatusToJson(McpServerStatus instance) =>
    <String, dynamic>{
      'name': instance.name,
      'status': instance.status,
      if (instance.serverInfo case final value?) 'serverInfo': value,
      if (instance.error case final value?) 'error': value,
    };
