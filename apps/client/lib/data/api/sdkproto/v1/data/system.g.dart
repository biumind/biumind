// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'system.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKSystemInit _$SDKSystemInitFromJson(Map json) => SDKSystemInit(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'init',
  agents: json['agents'] as List<dynamic>,
  apiKeySource: json['apiKeySource'] as String,
  betas: (json['betas'] as List<dynamic>).map((e) => e as String).toList(),
  claudeCodeVersion: json['claude_code_version'] as String,
  cwd: json['cwd'] as String,
  tools: (json['tools'] as List<dynamic>).map((e) => e as String).toList(),
  mcpServers: json['mcp_servers'] as List<dynamic>,
  model: json['model'] as String,
  permissionMode: json['permissionMode'] as String,
  slashCommands: json['slash_commands'] as List<dynamic>,
  outputStyle: json['output_style'] as String,
  skills: json['skills'] as List<dynamic>?,
  plugins: json['plugins'] as List<dynamic>?,
  fastModeState: json['fast_mode_state'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKSystemInitToJson(SDKSystemInit instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'agents': instance.agents,
      'apiKeySource': instance.apiKeySource,
      'betas': instance.betas,
      'claude_code_version': instance.claudeCodeVersion,
      'cwd': instance.cwd,
      'tools': instance.tools,
      'mcp_servers': instance.mcpServers,
      'model': instance.model,
      'permissionMode': instance.permissionMode,
      'slash_commands': instance.slashCommands,
      'output_style': instance.outputStyle,
      if (instance.skills case final value?) 'skills': value,
      if (instance.plugins case final value?) 'plugins': value,
      if (instance.fastModeState case final value?) 'fast_mode_state': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKSystemStatus _$SDKSystemStatusFromJson(Map json) => SDKSystemStatus(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'status',
  status: json['status'] as String?,
  permissionMode: json['permissionMode'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKSystemStatusToJson(SDKSystemStatus instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      if (instance.status case final value?) 'status': value,
      if (instance.permissionMode case final value?) 'permissionMode': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

CompactMetadata _$CompactMetadataFromJson(Map json) => CompactMetadata(
  trigger: json['trigger'] as String,
  preTokens: (json['pre_tokens'] as num).toInt(),
  preservedSegment: json['preserved_segment'],
);

Map<String, dynamic> _$CompactMetadataToJson(
  CompactMetadata instance,
) => <String, dynamic>{
  'trigger': instance.trigger,
  'pre_tokens': instance.preTokens,
  if (instance.preservedSegment case final value?) 'preserved_segment': value,
};

SDKCompactBoundary _$SDKCompactBoundaryFromJson(Map json) => SDKCompactBoundary(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'compact_boundary',
  compactMetadata: CompactMetadata.fromJson(
    Map<String, dynamic>.from(json['compact_metadata'] as Map),
  ),
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKCompactBoundaryToJson(SDKCompactBoundary instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'compact_metadata': instance.compactMetadata,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKAPIRetry _$SDKAPIRetryFromJson(Map json) => SDKAPIRetry(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'api_retry',
  attempt: (json['attempt'] as num).toInt(),
  maxRetries: (json['max_retries'] as num).toInt(),
  retryDelayMs: (json['retry_delay_ms'] as num).toInt(),
  errorStatus: json['error_status'],
  error: json['error'] as String,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKAPIRetryToJson(SDKAPIRetry instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'attempt': instance.attempt,
      'max_retries': instance.maxRetries,
      'retry_delay_ms': instance.retryDelayMs,
      if (instance.errorStatus case final value?) 'error_status': value,
      'error': instance.error,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKLocalCommandOutput _$SDKLocalCommandOutputFromJson(Map json) =>
    SDKLocalCommandOutput(
      type: json['type'] as String? ?? 'system',
      subtype: json['subtype'] as String? ?? 'local_command_output',
      content: json['content'] as String,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKLocalCommandOutputToJson(
  SDKLocalCommandOutput instance,
) => <String, dynamic>{
  'type': instance.type,
  'subtype': instance.subtype,
  'content': instance.content,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKHookStarted _$SDKHookStartedFromJson(Map json) => SDKHookStarted(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'hook_started',
  hookId: json['hook_id'] as String,
  hookName: json['hook_name'] as String,
  hookEvent: json['hook_event'] as String,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKHookStartedToJson(SDKHookStarted instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'hook_id': instance.hookId,
      'hook_name': instance.hookName,
      'hook_event': instance.hookEvent,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKHookProgress _$SDKHookProgressFromJson(Map json) => SDKHookProgress(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'hook_progress',
  hookId: json['hook_id'] as String,
  hookName: json['hook_name'] as String?,
  hookEvent: json['hook_event'] as String?,
  stdout: json['stdout'] as String?,
  stderr: json['stderr'] as String?,
  output: json['output'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKHookProgressToJson(SDKHookProgress instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'hook_id': instance.hookId,
      if (instance.hookName case final value?) 'hook_name': value,
      if (instance.hookEvent case final value?) 'hook_event': value,
      if (instance.stdout case final value?) 'stdout': value,
      if (instance.stderr case final value?) 'stderr': value,
      if (instance.output case final value?) 'output': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKHookResponse _$SDKHookResponseFromJson(Map json) => SDKHookResponse(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'hook_response',
  hookId: json['hook_id'] as String,
  hookName: json['hook_name'] as String?,
  hookEvent: json['hook_event'] as String?,
  exitCode: (json['exit_code'] as num?)?.toInt(),
  outcome: json['outcome'] as String,
  stdout: json['stdout'] as String?,
  stderr: json['stderr'] as String?,
  output: json['output'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKHookResponseToJson(SDKHookResponse instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'hook_id': instance.hookId,
      if (instance.hookName case final value?) 'hook_name': value,
      if (instance.hookEvent case final value?) 'hook_event': value,
      if (instance.exitCode case final value?) 'exit_code': value,
      'outcome': instance.outcome,
      if (instance.stdout case final value?) 'stdout': value,
      if (instance.stderr case final value?) 'stderr': value,
      if (instance.output case final value?) 'output': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKAuthStatus _$SDKAuthStatusFromJson(Map json) => SDKAuthStatus(
  type: json['type'] as String? ?? 'auth_status',
  isAuthenticating: json['isAuthenticating'] as bool,
  output: (json['output'] as List<dynamic>).map((e) => e as String).toList(),
  error: json['error'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKAuthStatusToJson(SDKAuthStatus instance) =>
    <String, dynamic>{
      'type': instance.type,
      'isAuthenticating': instance.isAuthenticating,
      'output': instance.output,
      if (instance.error case final value?) 'error': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKFilesPersisted _$SDKFilesPersistedFromJson(Map json) => SDKFilesPersisted(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'files_persisted',
  files: (json['files'] as List<dynamic>).map((e) => e as String).toList(),
  failed: json['failed'] as List<dynamic>,
  processedAt: (json['processed_at'] as num).toInt(),
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKFilesPersistedToJson(SDKFilesPersisted instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'files': instance.files,
      'failed': instance.failed,
      'processed_at': instance.processedAt,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKTaskNotification _$SDKTaskNotificationFromJson(Map json) =>
    SDKTaskNotification(
      type: json['type'] as String? ?? 'system',
      subtype: json['subtype'] as String? ?? 'task_notification',
      taskId: json['task_id'] as String,
      toolUseId: json['tool_use_id'] as String?,
      status: json['status'] as String,
      outputFile: json['output_file'] as String,
      summary: json['summary'] as String,
      usage: json['usage'],
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKTaskNotificationToJson(
  SDKTaskNotification instance,
) => <String, dynamic>{
  'type': instance.type,
  'subtype': instance.subtype,
  'task_id': instance.taskId,
  if (instance.toolUseId case final value?) 'tool_use_id': value,
  'status': instance.status,
  'output_file': instance.outputFile,
  'summary': instance.summary,
  if (instance.usage case final value?) 'usage': value,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKTaskStarted _$SDKTaskStartedFromJson(Map json) => SDKTaskStarted(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'task_started',
  taskId: json['task_id'] as String,
  toolUseId: json['tool_use_id'] as String?,
  description: json['description'] as String,
  taskType: json['task_type'] as String?,
  workflowName: json['workflow_name'] as String?,
  prompt: json['prompt'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKTaskStartedToJson(SDKTaskStarted instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'task_id': instance.taskId,
      if (instance.toolUseId case final value?) 'tool_use_id': value,
      'description': instance.description,
      if (instance.taskType case final value?) 'task_type': value,
      if (instance.workflowName case final value?) 'workflow_name': value,
      if (instance.prompt case final value?) 'prompt': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKTaskProgress _$SDKTaskProgressFromJson(Map json) => SDKTaskProgress(
  type: json['type'] as String? ?? 'system',
  subtype: json['subtype'] as String? ?? 'task_progress',
  taskId: json['task_id'] as String,
  lastToolName: json['last_tool_name'] as String?,
  summary: json['summary'] as String?,
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKTaskProgressToJson(SDKTaskProgress instance) =>
    <String, dynamic>{
      'type': instance.type,
      'subtype': instance.subtype,
      'task_id': instance.taskId,
      if (instance.lastToolName case final value?) 'last_tool_name': value,
      if (instance.summary case final value?) 'summary': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKSessionStateChanged _$SDKSessionStateChangedFromJson(Map json) =>
    SDKSessionStateChanged(
      type: json['type'] as String? ?? 'system',
      subtype: json['subtype'] as String? ?? 'session_state_changed',
      state: json['state'] as String,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKSessionStateChangedToJson(
  SDKSessionStateChanged instance,
) => <String, dynamic>{
  'type': instance.type,
  'subtype': instance.subtype,
  'state': instance.state,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKRateLimitEvent _$SDKRateLimitEventFromJson(Map json) => SDKRateLimitEvent(
  type: json['type'] as String? ?? 'rate_limit_event',
  rateLimitInfo: json['rate_limit_info'],
  uuid: json['uuid'] as String,
  sessionId: json['session_id'] as String,
);

Map<String, dynamic> _$SDKRateLimitEventToJson(SDKRateLimitEvent instance) =>
    <String, dynamic>{
      'type': instance.type,
      if (instance.rateLimitInfo case final value?) 'rate_limit_info': value,
      'uuid': instance.uuid,
      'session_id': instance.sessionId,
    };

SDKElicitationComplete _$SDKElicitationCompleteFromJson(Map json) =>
    SDKElicitationComplete(
      type: json['type'] as String? ?? 'system',
      subtype: json['subtype'] as String? ?? 'elicitation_complete',
      mcpServerName: json['mcp_server_name'] as String,
      elicitationId: json['elicitation_id'] as String,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKElicitationCompleteToJson(
  SDKElicitationComplete instance,
) => <String, dynamic>{
  'type': instance.type,
  'subtype': instance.subtype,
  'mcp_server_name': instance.mcpServerName,
  'elicitation_id': instance.elicitationId,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};

SDKPromptSuggestion _$SDKPromptSuggestionFromJson(Map json) =>
    SDKPromptSuggestion(
      type: json['type'] as String? ?? 'prompt_suggestion',
      suggestion: json['suggestion'] as String,
      uuid: json['uuid'] as String,
      sessionId: json['session_id'] as String,
    );

Map<String, dynamic> _$SDKPromptSuggestionToJson(
  SDKPromptSuggestion instance,
) => <String, dynamic>{
  'type': instance.type,
  'suggestion': instance.suggestion,
  'uuid': instance.uuid,
  'session_id': instance.sessionId,
};
