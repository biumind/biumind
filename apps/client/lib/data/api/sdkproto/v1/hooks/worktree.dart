import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'worktree.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class WorktreeCreate implements HookInput {
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
  final String name;

  WorktreeCreate({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.worktreeCreate,
    required this.name,
  });

  factory WorktreeCreate.fromJson(Map<String, dynamic> json) =>
      _$WorktreeCreateFromJson(json);
  Map<String, dynamic> toJson() => _$WorktreeCreateToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class WorktreeRemove implements HookInput {
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
  @JsonKey(name: 'worktree_path')
  final String worktreePath;

  WorktreeRemove({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.worktreeRemove,
    required this.worktreePath,
  });

  factory WorktreeRemove.fromJson(Map<String, dynamic> json) =>
      _$WorktreeRemoveFromJson(json);
  Map<String, dynamic> toJson() => _$WorktreeRemoveToJson(this);
}
