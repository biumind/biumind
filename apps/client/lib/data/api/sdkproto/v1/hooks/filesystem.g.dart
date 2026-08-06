// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'filesystem.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

CwdChanged _$CwdChangedFromJson(Map json) => CwdChanged(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.cwdChanged,
  oldCwd: json['old_cwd'] as String,
  newCwd: json['new_cwd'] as String,
);

Map<String, dynamic> _$CwdChangedToJson(CwdChanged instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'old_cwd': instance.oldCwd,
      'new_cwd': instance.newCwd,
    };

FileChanged _$FileChangedFromJson(Map json) => FileChanged(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.fileChanged,
  filePath: json['file_path'] as String,
  event: json['event'] as String,
);

Map<String, dynamic> _$FileChangedToJson(FileChanged instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'file_path': instance.filePath,
      'event': instance.event,
    };
