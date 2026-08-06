import 'package:json_annotation/json_annotation.dart';

part 'permission.g.dart';

/// can_use_tool — runtime/worker → client 询问 tool 是否允许。
@JsonSerializable(includeIfNull: false, anyMap: true)
class PermissionRequest {
  final String subtype;
  @JsonKey(name: 'tool_name')
  final String toolName;
  final dynamic input;
  @JsonKey(name: 'permission_suggestions')
  final List<dynamic>? permissionSuggestions;
  @JsonKey(name: 'blocked_path')
  final String? blockedPath;
  @JsonKey(name: 'decision_reason')
  final String? decisionReason;
  final String? title;
  @JsonKey(name: 'display_name')
  final String? displayName;
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;
  @JsonKey(name: 'agent_id')
  final String? agentId;
  final String? description;

  PermissionRequest({
    this.subtype = 'can_use_tool',
    required this.toolName,
    required this.input,
    this.permissionSuggestions,
    this.blockedPath,
    this.decisionReason,
    this.title,
    this.displayName,
    required this.toolUseId,
    this.agentId,
    this.description,
  });

  factory PermissionRequest.fromJson(Map<String, dynamic> json) =>
      _$PermissionRequestFromJson(json);
  Map<String, dynamic> toJson() => _$PermissionRequestToJson(this);
}
