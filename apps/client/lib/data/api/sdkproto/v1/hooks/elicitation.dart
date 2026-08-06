// Elicitation / ElicitationResult hooks —— 跟 control 通道是不同载体。
// 用 Hook 后缀避免命名冲突。

import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'elicitation.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class ElicitationHook implements HookInput {
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
  @JsonKey(name: 'mcp_server_name')
  final String mcpServerName;
  final String message;
  final String? mode;
  final String? url;
  @JsonKey(name: 'elicitation_id')
  final String? elicitationId;
  @JsonKey(name: 'requested_schema')
  final Map<String, dynamic>? requestedSchema;

  ElicitationHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.elicitation,
    required this.mcpServerName,
    required this.message,
    this.mode,
    this.url,
    this.elicitationId,
    this.requestedSchema,
  });

  factory ElicitationHook.fromJson(Map<String, dynamic> json) =>
      _$ElicitationHookFromJson(json);
  Map<String, dynamic> toJson() => _$ElicitationHookToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ElicitationResultHook implements HookInput {
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
  @JsonKey(name: 'mcp_server_name')
  final String mcpServerName;
  @JsonKey(name: 'elicitation_id')
  final String? elicitationId;
  final String? mode;
  final String action;
  final Map<String, dynamic>? content;

  ElicitationResultHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.elicitationResult,
    required this.mcpServerName,
    this.elicitationId,
    this.mode,
    required this.action,
    this.content,
  });

  factory ElicitationResultHook.fromJson(Map<String, dynamic> json) =>
      _$ElicitationResultHookFromJson(json);
  Map<String, dynamic> toJson() => _$ElicitationResultHookToJson(this);
}
