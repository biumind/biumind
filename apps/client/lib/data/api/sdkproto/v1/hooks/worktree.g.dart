// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'worktree.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

WorktreeCreate _$WorktreeCreateFromJson(Map json) => WorktreeCreate(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.worktreeCreate,
  name: json['name'] as String,
);

Map<String, dynamic> _$WorktreeCreateToJson(WorktreeCreate instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'name': instance.name,
    };

WorktreeRemove _$WorktreeRemoveFromJson(Map json) => WorktreeRemove(
  sessionId: json['session_id'] as String,
  transcriptPath: json['transcript_path'] as String,
  cwd: json['cwd'] as String,
  hookEventName: json['hook_event_name'] as String? ?? HookEvent.worktreeRemove,
  worktreePath: json['worktree_path'] as String,
);

Map<String, dynamic> _$WorktreeRemoveToJson(WorktreeRemove instance) =>
    <String, dynamic>{
      'session_id': instance.sessionId,
      'transcript_path': instance.transcriptPath,
      'cwd': instance.cwd,
      'hook_event_name': instance.hookEventName,
      'worktree_path': instance.worktreePath,
    };
