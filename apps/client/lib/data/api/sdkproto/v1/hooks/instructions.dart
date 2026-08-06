import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'instructions.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class InstructionsLoaded implements HookInput {
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
  @JsonKey(name: 'file_path')
  final String filePath;
  @JsonKey(name: 'memory_type')
  final String memoryType;
  @JsonKey(name: 'load_reason')
  final String loadReason;
  final List<String>? globs;
  @JsonKey(name: 'trigger_file_path')
  final String? triggerFilePath;
  @JsonKey(name: 'parent_file_path')
  final String? parentFilePath;

  InstructionsLoaded({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.instructionsLoaded,
    required this.filePath,
    required this.memoryType,
    required this.loadReason,
    this.globs,
    this.triggerFilePath,
    this.parentFilePath,
  });

  factory InstructionsLoaded.fromJson(Map<String, dynamic> json) =>
      _$InstructionsLoadedFromJson(json);
  Map<String, dynamic> toJson() => _$InstructionsLoadedToJson(this);
}
