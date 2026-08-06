// 4 个 tool use hooks：PreToolUse / PostToolUse / PostToolUseFailure / PermissionDenied

import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'tool_use.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class PreToolUse implements HookInput {
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
  @JsonKey(name: 'permission_mode')
  final String? permissionMode;
  @JsonKey(name: 'tool_name')
  final String toolName;
  @JsonKey(name: 'tool_input')
  final dynamic toolInput;
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;

  PreToolUse({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.preToolUse,
    this.permissionMode,
    required this.toolName,
    required this.toolInput,
    required this.toolUseId,
  });

  factory PreToolUse.fromJson(Map<String, dynamic> json) =>
      _$PreToolUseFromJson(json);
  Map<String, dynamic> toJson() => _$PreToolUseToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class PostToolUse implements HookInput {
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
  @JsonKey(name: 'tool_response')
  final dynamic toolResponse;
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;

  PostToolUse({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.postToolUse,
    required this.toolName,
    required this.toolInput,
    required this.toolResponse,
    required this.toolUseId,
  });

  factory PostToolUse.fromJson(Map<String, dynamic> json) =>
      _$PostToolUseFromJson(json);
  Map<String, dynamic> toJson() => _$PostToolUseToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class PostToolUseFailure implements HookInput {
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
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;
  final String error;
  @JsonKey(name: 'is_interrupt')
  final bool? isInterrupt;

  PostToolUseFailure({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.postToolUseFailure,
    required this.toolName,
    required this.toolInput,
    required this.toolUseId,
    required this.error,
    this.isInterrupt,
  });

  factory PostToolUseFailure.fromJson(Map<String, dynamic> json) =>
      _$PostToolUseFailureFromJson(json);
  Map<String, dynamic> toJson() => _$PostToolUseFailureToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class PermissionDenied implements HookInput {
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
  @JsonKey(name: 'tool_use_id')
  final String toolUseId;
  final String reason;

  PermissionDenied({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.permissionDenied,
    required this.toolName,
    required this.toolInput,
    required this.toolUseId,
    required this.reason,
  });

  factory PermissionDenied.fromJson(Map<String, dynamic> json) =>
      _$PermissionDeniedFromJson(json);
  Map<String, dynamic> toJson() => _$PermissionDeniedToJson(this);
}
