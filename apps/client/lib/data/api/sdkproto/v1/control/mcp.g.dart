// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'mcp.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

McpStatus _$McpStatusFromJson(Map json) =>
    McpStatus(subtype: json['subtype'] as String? ?? 'mcp_status');

Map<String, dynamic> _$McpStatusToJson(McpStatus instance) => <String, dynamic>{
  'subtype': instance.subtype,
};

McpMessage _$McpMessageFromJson(Map json) => McpMessage(
  subtype: json['subtype'] as String? ?? 'mcp_message',
  serverName: json['server_name'] as String,
  message: Map<String, dynamic>.from(json['message'] as Map),
);

Map<String, dynamic> _$McpMessageToJson(McpMessage instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'server_name': instance.serverName,
      'message': instance.message,
    };

McpSetServers _$McpSetServersFromJson(Map json) => McpSetServers(
  subtype: json['subtype'] as String? ?? 'mcp_set_servers',
  servers: Map<String, dynamic>.from(json['servers'] as Map),
);

Map<String, dynamic> _$McpSetServersToJson(McpSetServers instance) =>
    <String, dynamic>{'subtype': instance.subtype, 'servers': instance.servers};

McpReconnect _$McpReconnectFromJson(Map json) => McpReconnect(
  subtype: json['subtype'] as String? ?? 'mcp_reconnect',
  serverName: json['serverName'] as String,
);

Map<String, dynamic> _$McpReconnectToJson(McpReconnect instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'serverName': instance.serverName,
    };

McpToggle _$McpToggleFromJson(Map json) => McpToggle(
  subtype: json['subtype'] as String? ?? 'mcp_toggle',
  serverName: json['serverName'] as String,
  enabled: json['enabled'] as bool,
);

Map<String, dynamic> _$McpToggleToJson(McpToggle instance) => <String, dynamic>{
  'subtype': instance.subtype,
  'serverName': instance.serverName,
  'enabled': instance.enabled,
};
