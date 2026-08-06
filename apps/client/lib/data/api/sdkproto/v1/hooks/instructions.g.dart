// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'instructions.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

InstructionsLoaded _$InstructionsLoadedFromJson(Map json) => InstructionsLoaded(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName:
      json['hook_event_name'] as String? ?? HookEvent.instructionsLoaded,
  filePath: json['file_path'] as String,
  memoryType: json['memory_type'] as String,
  loadReason: json['load_reason'] as String,
  globs: (json['globs'] as List<dynamic>?)?.map((e) => e as String).toList(),
  triggerFilePath: json['trigger_file_path'] as String?,
  parentFilePath: json['parent_file_path'] as String?,
);

Map<String, dynamic> _$InstructionsLoadedToJson(
  InstructionsLoaded instance,
) => <String, dynamic>{
  'session_id': instance.sessionId,
  'transcript_path': instance.transcriptPath,
  'cwd': instance.cwd,
  'hook_event_name': instance.hookEventName,
  'file_path': instance.filePath,
  'memory_type': instance.memoryType,
  'load_reason': instance.loadReason,
  if (instance.globs case final value?) 'globs': value,
  if (instance.triggerFilePath case final value?) 'trigger_file_path': value,
  if (instance.parentFilePath case final value?) 'parent_file_path': value,
};
