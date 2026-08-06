import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'filesystem.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class CwdChanged implements HookInput {
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
  @JsonKey(name: 'old_cwd')
  final String oldCwd;
  @JsonKey(name: 'new_cwd')
  final String newCwd;

  CwdChanged({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.cwdChanged,
    required this.oldCwd,
    required this.newCwd,
  });

  factory CwdChanged.fromJson(Map<String, dynamic> json) =>
      _$CwdChangedFromJson(json);
  Map<String, dynamic> toJson() => _$CwdChangedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class FileChanged implements HookInput {
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
  final String event;

  FileChanged({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.fileChanged,
    required this.filePath,
    required this.event,
  });

  factory FileChanged.fromJson(Map<String, dynamic> json) =>
      _$FileChangedFromJson(json);
  Map<String, dynamic> toJson() => _$FileChangedToJson(this);
}
