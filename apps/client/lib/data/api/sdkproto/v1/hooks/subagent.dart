import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'subagent.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SubagentStart implements HookInput {
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
  @JsonKey(name: 'agent_id')
  final String agentId;
  @JsonKey(name: 'agent_type')
  final String agentType;

  SubagentStart({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.subagentStart,
    required this.agentId,
    required this.agentType,
  });

  factory SubagentStart.fromJson(Map<String, dynamic> json) =>
      _$SubagentStartFromJson(json);
  Map<String, dynamic> toJson() => _$SubagentStartToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SubagentStop implements HookInput {
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
  @JsonKey(name: 'agent_id')
  final String agentId;
  @JsonKey(name: 'agent_transcript_path')
  final String agentTranscriptPath;
  @JsonKey(name: 'agent_type')
  final String agentType;
  @JsonKey(name: 'last_assistant_message')
  final String? lastAssistantMessage;

  SubagentStop({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.subagentStop,
    required this.stopHookActive,
    required this.agentId,
    required this.agentTranscriptPath,
    required this.agentType,
    this.lastAssistantMessage,
  });

  factory SubagentStop.fromJson(Map<String, dynamic> json) =>
      _$SubagentStopFromJson(json);
  Map<String, dynamic> toJson() => _$SubagentStopToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class TeammateIdle implements HookInput {
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
  @JsonKey(name: 'teammate_name')
  final String teammateName;
  @JsonKey(name: 'team_name')
  final String teamName;

  TeammateIdle({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.teammateIdle,
    required this.teammateName,
    required this.teamName,
  });

  factory TeammateIdle.fromJson(Map<String, dynamic> json) =>
      _$TeammateIdleFromJson(json);
  Map<String, dynamic> toJson() => _$TeammateIdleToJson(this);
}
