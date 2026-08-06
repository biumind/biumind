import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'compact.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class PreCompact implements HookInput {
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
  final String trigger;
  @JsonKey(name: 'custom_instructions')
  final String customInstructions;

  PreCompact({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.preCompact,
    required this.trigger,
    required this.customInstructions,
  });

  factory PreCompact.fromJson(Map<String, dynamic> json) =>
      _$PreCompactFromJson(json);
  Map<String, dynamic> toJson() => _$PreCompactToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class PostCompact implements HookInput {
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
  final String trigger;
  @JsonKey(name: 'compact_summary')
  final String compactSummary;

  PostCompact({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.postCompact,
    required this.trigger,
    required this.compactSummary,
  });

  factory PostCompact.fromJson(Map<String, dynamic> json) =>
      _$PostCompactFromJson(json);
  Map<String, dynamic> toJson() => _$PostCompactToJson(this);
}
