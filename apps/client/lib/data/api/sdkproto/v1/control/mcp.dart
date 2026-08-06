// 5 个 mcp control variant 共享一个文件。

import 'package:json_annotation/json_annotation.dart';

part 'mcp.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpStatus {
  final String subtype;
  McpStatus({this.subtype = 'mcp_status'});
  factory McpStatus.fromJson(Map<String, dynamic> json) =>
      _$McpStatusFromJson(json);
  Map<String, dynamic> toJson() => _$McpStatusToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpMessage {
  final String subtype;
  @JsonKey(name: 'server_name')
  final String serverName;
  final Map<String, dynamic> message;

  McpMessage({
    this.subtype = 'mcp_message',
    required this.serverName,
    required this.message,
  });

  factory McpMessage.fromJson(Map<String, dynamic> json) =>
      _$McpMessageFromJson(json);
  Map<String, dynamic> toJson() => _$McpMessageToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpSetServers {
  final String subtype;
  final Map<String, dynamic> servers;

  McpSetServers({this.subtype = 'mcp_set_servers', required this.servers});
  factory McpSetServers.fromJson(Map<String, dynamic> json) =>
      _$McpSetServersFromJson(json);
  Map<String, dynamic> toJson() => _$McpSetServersToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpReconnect {
  final String subtype;
  final String serverName;
  McpReconnect({this.subtype = 'mcp_reconnect', required this.serverName});
  factory McpReconnect.fromJson(Map<String, dynamic> json) =>
      _$McpReconnectFromJson(json);
  Map<String, dynamic> toJson() => _$McpReconnectToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class McpToggle {
  final String subtype;
  final String serverName;
  final bool enabled;
  McpToggle({
    this.subtype = 'mcp_toggle',
    required this.serverName,
    required this.enabled,
  });
  factory McpToggle.fromJson(Map<String, dynamic> json) =>
      _$McpToggleFromJson(json);
  Map<String, dynamic> toJson() => _$McpToggleToJson(this);
}
