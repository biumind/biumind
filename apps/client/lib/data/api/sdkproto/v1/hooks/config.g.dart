// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'config.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

ConfigChange _$ConfigChangeFromJson(Map json) => ConfigChange(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.configChange,
  source: json['source'] as String,
  filePath: json['file_path'] as String?,
);

Map<String, dynamic> _$ConfigChangeToJson(ConfigChange instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'source': instance.source,
      if (instance.filePath case final value?) 'file_path': value,
    };
