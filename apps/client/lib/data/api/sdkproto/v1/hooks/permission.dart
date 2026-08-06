import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'permission.g.dart';

/// PermissionRequest hook —— 跟 control/permission.dart 的 PermissionRequest 是不同载体。
/// Dart 加 Hook 后缀避免命名冲突。
@JsonSerializable(includeIfNull: false, anyMap: true)
class PermissionRequestHook implements HookInput {
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  @override
  @JsonKey(name: 'transcript_path')
  final String transcriptPath;
  @override
  final String cwd;
  @override
  @JsonKey(name: 'hook_event_name')
  final String hookEventName;
  @JsonKey(name: 'tool_name')
  final String toolName;
  @JsonKey(name: 'tool_input')
  final dynamic toolInput;
  @JsonKey(name: 'permission_suggestions')
  final List<dynamic>? permissionSuggestions;

  PermissionRequestHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.permissionRequest,
    required this.toolName,
    required this.toolInput,
    this.permissionSuggestions,
  });

  factory PermissionRequestHook.fromJson(Map<String, dynamic> json) =>
      _$PermissionRequestHookFromJson(json);
  Map<String, dynamic> toJson() => _$PermissionRequestHookToJson(this);
}
