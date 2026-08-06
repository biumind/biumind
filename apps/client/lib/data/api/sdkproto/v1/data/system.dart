// 16 个 system subtype（含 SDKAuthStatus / SDKRateLimitEvent / SDKPromptSuggestion 等
// 实际 type 不是 "system" 但归属 system 概念域的 variant）。
//
// 字段表见 schema/sdk/v1/data/system.json + Schema-Mapping §4。
//
// 大部分子类同一文件，按 Dart 命名习惯（一个文件一组相关类）。

import 'package:json_annotation/json_annotation.dart';
import 'sdk_message.dart';

part 'system.g.dart';

// ── system: init ────────────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKSystemInit extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final List<dynamic> agents;
  final String apiKeySource;
  final List<String> betas;
  @JsonKey(name: 'claude_code_version')
  final String claudeCodeVersion;
  final String cwd;
  final List<String> tools;
  @JsonKey(name: 'mcp_servers')
  final List<dynamic> mcpServers;
  final String model;
  final String permissionMode;
  @JsonKey(name: 'slash_commands')
  final List<dynamic> slashCommands;
  @JsonKey(name: 'output_style')
  final String outputStyle;
  final List<dynamic>? skills;
  final List<dynamic>? plugins;
  @JsonKey(name: 'fast_mode_state')
  final String? fastModeState;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKSystemInit({
    this.type = 'system',
    this.subtype = 'init',
    required this.agents,
    required this.apiKeySource,
    required this.betas,
    required this.claudeCodeVersion,
    required this.cwd,
    required this.tools,
    required this.mcpServers,
    required this.model,
    required this.permissionMode,
    required this.slashCommands,
    required this.outputStyle,
    this.skills,
    this.plugins,
    this.fastModeState,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKSystemInit.fromJson(Map<String, dynamic> json) =>
      _$SDKSystemInitFromJson(json);
  Map<String, dynamic> toJson() => _$SDKSystemInitToJson(this);
}

// ── system: status ──────────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKSystemStatus extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final String? status;
  final String? permissionMode;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKSystemStatus({
    this.type = 'system',
    this.subtype = 'status',
    this.status,
    this.permissionMode,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKSystemStatus.fromJson(Map<String, dynamic> json) =>
      _$SDKSystemStatusFromJson(json);
  Map<String, dynamic> toJson() => _$SDKSystemStatusToJson(this);
}

// ── system: compact_boundary ────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class CompactMetadata {
  final String trigger;
  @JsonKey(name: 'pre_tokens')
  final int preTokens;
  @JsonKey(name: 'preserved_segment')
  final dynamic preservedSegment;

  CompactMetadata({required this.trigger, required this.preTokens, this.preservedSegment});

  factory CompactMetadata.fromJson(Map<String, dynamic> json) =>
      _$CompactMetadataFromJson(json);
  Map<String, dynamic> toJson() => _$CompactMetadataToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKCompactBoundary extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'compact_metadata')
  final CompactMetadata compactMetadata;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKCompactBoundary({
    this.type = 'system',
    this.subtype = 'compact_boundary',
    required this.compactMetadata,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKCompactBoundary.fromJson(Map<String, dynamic> json) =>
      _$SDKCompactBoundaryFromJson(json);
  Map<String, dynamic> toJson() => _$SDKCompactBoundaryToJson(this);
}

// ── system: api_retry ───────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKAPIRetry extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final int attempt;
  @JsonKey(name: 'max_retries')
  final int maxRetries;
  @JsonKey(name: 'retry_delay_ms')
  final int retryDelayMs;
  @JsonKey(name: 'error_status')
  final dynamic errorStatus;
  final String error;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKAPIRetry({
    this.type = 'system',
    this.subtype = 'api_retry',
    required this.attempt,
    required this.maxRetries,
    required this.retryDelayMs,
    this.errorStatus,
    required this.error,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKAPIRetry.fromJson(Map<String, dynamic> json) =>
      _$SDKAPIRetryFromJson(json);
  Map<String, dynamic> toJson() => _$SDKAPIRetryToJson(this);
}

// ── system: local_command_output ────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKLocalCommandOutput extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final String content;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKLocalCommandOutput({
    this.type = 'system',
    this.subtype = 'local_command_output',
    required this.content,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKLocalCommandOutput.fromJson(Map<String, dynamic> json) =>
      _$SDKLocalCommandOutputFromJson(json);
  Map<String, dynamic> toJson() => _$SDKLocalCommandOutputToJson(this);
}

// ── system: hook_started / hook_progress / hook_response ────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKHookStarted extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'hook_id')
  final String hookId;
  @JsonKey(name: 'hook_name')
  final String hookName;
  @JsonKey(name: 'hook_event')
  final String hookEvent;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKHookStarted({
    this.type = 'system',
    this.subtype = 'hook_started',
    required this.hookId,
    required this.hookName,
    required this.hookEvent,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKHookStarted.fromJson(Map<String, dynamic> json) =>
      _$SDKHookStartedFromJson(json);
  Map<String, dynamic> toJson() => _$SDKHookStartedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKHookProgress extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'hook_id')
  final String hookId;
  @JsonKey(name: 'hook_name')
  final String? hookName;
  @JsonKey(name: 'hook_event')
  final String? hookEvent;
  final String? stdout;
  final String? stderr;
  final String? output;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKHookProgress({
    this.type = 'system',
    this.subtype = 'hook_progress',
    required this.hookId,
    this.hookName,
    this.hookEvent,
    this.stdout,
    this.stderr,
    this.output,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKHookProgress.fromJson(Map<String, dynamic> json) =>
      _$SDKHookProgressFromJson(json);
  Map<String, dynamic> toJson() => _$SDKHookProgressToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKHookResponse extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'hook_id')
  final String hookId;
  @JsonKey(name: 'hook_name')
  final String? hookName;
  @JsonKey(name: 'hook_event')
  final String? hookEvent;
  @JsonKey(name: 'exit_code')
  final int? exitCode;
  final String outcome;
  final String? stdout;
  final String? stderr;
  final String? output;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKHookResponse({
    this.type = 'system',
    this.subtype = 'hook_response',
    required this.hookId,
    this.hookName,
    this.hookEvent,
    this.exitCode,
    required this.outcome,
    this.stdout,
    this.stderr,
    this.output,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKHookResponse.fromJson(Map<String, dynamic> json) =>
      _$SDKHookResponseFromJson(json);
  Map<String, dynamic> toJson() => _$SDKHookResponseToJson(this);
}

// ── auth_status ─────────────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKAuthStatus extends SDKMessage {
  @override
  final String type;
  final bool isAuthenticating;
  final List<String> output;
  final String? error;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKAuthStatus({
    this.type = 'auth_status',
    required this.isAuthenticating,
    required this.output,
    this.error,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKAuthStatus.fromJson(Map<String, dynamic> json) =>
      _$SDKAuthStatusFromJson(json);
  Map<String, dynamic> toJson() => _$SDKAuthStatusToJson(this);
}

// ── system: files_persisted ─────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKFilesPersisted extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final List<String> files;
  final List<dynamic> failed;
  @JsonKey(name: 'processed_at')
  final int processedAt;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKFilesPersisted({
    this.type = 'system',
    this.subtype = 'files_persisted',
    required this.files,
    required this.failed,
    required this.processedAt,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKFilesPersisted.fromJson(Map<String, dynamic> json) =>
      _$SDKFilesPersistedFromJson(json);
  Map<String, dynamic> toJson() => _$SDKFilesPersistedToJson(this);
}

// ── system: task_notification / task_started / task_progress ─

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKTaskNotification extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'task_id')
  final String taskId;
  @JsonKey(name: 'tool_use_id')
  final String? toolUseId;
  final String status;
  @JsonKey(name: 'output_file')
  final String outputFile;
  final String summary;
  final dynamic usage;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKTaskNotification({
    this.type = 'system',
    this.subtype = 'task_notification',
    required this.taskId,
    this.toolUseId,
    required this.status,
    required this.outputFile,
    required this.summary,
    this.usage,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKTaskNotification.fromJson(Map<String, dynamic> json) =>
      _$SDKTaskNotificationFromJson(json);
  Map<String, dynamic> toJson() => _$SDKTaskNotificationToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKTaskStarted extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'task_id')
  final String taskId;
  @JsonKey(name: 'tool_use_id')
  final String? toolUseId;
  final String description;
  @JsonKey(name: 'task_type')
  final String? taskType;
  @JsonKey(name: 'workflow_name')
  final String? workflowName;
  final String? prompt;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKTaskStarted({
    this.type = 'system',
    this.subtype = 'task_started',
    required this.taskId,
    this.toolUseId,
    required this.description,
    this.taskType,
    this.workflowName,
    this.prompt,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKTaskStarted.fromJson(Map<String, dynamic> json) =>
      _$SDKTaskStartedFromJson(json);
  Map<String, dynamic> toJson() => _$SDKTaskStartedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKTaskProgress extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'task_id')
  final String taskId;
  @JsonKey(name: 'last_tool_name')
  final String? lastToolName;
  final String? summary;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKTaskProgress({
    this.type = 'system',
    this.subtype = 'task_progress',
    required this.taskId,
    this.lastToolName,
    this.summary,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKTaskProgress.fromJson(Map<String, dynamic> json) =>
      _$SDKTaskProgressFromJson(json);
  Map<String, dynamic> toJson() => _$SDKTaskProgressToJson(this);
}

// ── session_state_changed ───────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKSessionStateChanged extends SDKMessage {
  @override
  final String type;
  final String subtype;
  final String state;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKSessionStateChanged({
    this.type = 'system',
    this.subtype = 'session_state_changed',
    required this.state,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKSessionStateChanged.fromJson(Map<String, dynamic> json) =>
      _$SDKSessionStateChangedFromJson(json);
  Map<String, dynamic> toJson() => _$SDKSessionStateChangedToJson(this);
}

// ── rate_limit_event ────────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKRateLimitEvent extends SDKMessage {
  @override
  final String type;
  @JsonKey(name: 'rate_limit_info')
  final dynamic rateLimitInfo;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKRateLimitEvent({
    this.type = 'rate_limit_event',
    required this.rateLimitInfo,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKRateLimitEvent.fromJson(Map<String, dynamic> json) =>
      _$SDKRateLimitEventFromJson(json);
  Map<String, dynamic> toJson() => _$SDKRateLimitEventToJson(this);
}

// ── elicitation_complete ────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKElicitationComplete extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'mcp_server_name')
  final String mcpServerName;
  @JsonKey(name: 'elicitation_id')
  final String elicitationId;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKElicitationComplete({
    this.type = 'system',
    this.subtype = 'elicitation_complete',
    required this.mcpServerName,
    required this.elicitationId,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKElicitationComplete.fromJson(Map<String, dynamic> json) =>
      _$SDKElicitationCompleteFromJson(json);
  Map<String, dynamic> toJson() => _$SDKElicitationCompleteToJson(this);
}

// ── prompt_suggestion ───────────────────────────────────────

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKPromptSuggestion extends SDKMessage {
  @override
  final String type;
  final String suggestion;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKPromptSuggestion({
    this.type = 'prompt_suggestion',
    required this.suggestion,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKPromptSuggestion.fromJson(Map<String, dynamic> json) =>
      _$SDKPromptSuggestionFromJson(json);
  Map<String, dynamic> toJson() => _$SDKPromptSuggestionToJson(this);
}
