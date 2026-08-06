// 5 个 session lifecycle hooks。

import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'session.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionStart implements HookInput {
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
  final String source;
  @JsonKey(name: 'agent_type')
  final String? agentType;
  final String? model;

  SessionStart({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.sessionStart,
    required this.source,
    this.agentType,
    this.model,
  });

  factory SessionStart.fromJson(Map<String, dynamic> json) =>
      _$SessionStartFromJson(json);
  Map<String, dynamic> toJson() => _$SessionStartToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionEnd implements HookInput {
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
  final String reason;

  SessionEnd({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.sessionEnd,
    required this.reason,
  });

  factory SessionEnd.fromJson(Map<String, dynamic> json) =>
      _$SessionEndFromJson(json);
  Map<String, dynamic> toJson() => _$SessionEndToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class Stop implements HookInput {
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
  @JsonKey(name: 'stop_hook_active')
  final bool stopHookActive;
  @JsonKey(name: 'last_assistant_message')
  final String? lastAssistantMessage;

  Stop({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.stop,
    required this.stopHookActive,
    this.lastAssistantMessage,
  });

  factory Stop.fromJson(Map<String, dynamic> json) => _$StopFromJson(json);
  Map<String, dynamic> toJson() => _$StopToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class StopFailure implements HookInput {
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
  final String error;
  @JsonKey(name: 'error_details')
  final dynamic errorDetails;
  @JsonKey(name: 'last_assistant_message')
  final String? lastAssistantMessage;

  StopFailure({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.stopFailure,
    required this.error,
    this.errorDetails,
    this.lastAssistantMessage,
  });

  factory StopFailure.fromJson(Map<String, dynamic> json) =>
      _$StopFailureFromJson(json);
  Map<String, dynamic> toJson() => _$StopFailureToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class Setup implements HookInput {
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
  final String trigger; // init | maintenance

  Setup({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.setup,
    required this.trigger,
  });

  factory Setup.fromJson(Map<String, dynamic> json) => _$SetupFromJson(json);
  Map<String, dynamic> toJson() => _$SetupToJson(this);
}
