import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'config.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class ConfigChange implements HookInput {
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
  @JsonKey(name: 'file_path')
  final String? filePath;

  ConfigChange({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.configChange,
    required this.source,
    this.filePath,
  });

  factory ConfigChange.fromJson(Map<String, dynamic> json) =>
      _$ConfigChangeFromJson(json);
  Map<String, dynamic> toJson() => _$ConfigChangeToJson(this);
}
