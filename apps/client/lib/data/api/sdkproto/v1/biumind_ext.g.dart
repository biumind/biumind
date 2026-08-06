// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'biumind_ext.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

CreateSessionReq _$CreateSessionReqFromJson(Map json) => CreateSessionReq(
  mode: json['mode'] as String,
  environmentId: json['environment_id'] as String?,
  threadId: json['thread_id'] as String?,
  model: json['model'] as String?,
  systemPrompt: json['system_prompt'] as String?,
  biumindAttachmentIds: (json['biumind_attachment_ids'] as List<dynamic>?)
      ?.map((e) => e as String)
      .toList(),
);

Map<String, dynamic> _$CreateSessionReqToJson(CreateSessionReq instance) =>
    <String, dynamic>{
      'mode': instance.mode,
      if (instance.environmentId case final value?) 'environment_id': value,
      if (instance.threadId case final value?) 'thread_id': value,
      if (instance.model case final value?) 'model': value,
      if (instance.systemPrompt case final value?) 'system_prompt': value,
      if (instance.biumindAttachmentIds case final value?)
        'biumind_attachment_ids': value,
    };

Session _$SessionFromJson(Map json) => Session(
  sessionId: json['session_id'] as String,
  sessionToken: json['session_token'] as String,
  mode: json['mode'] as String,
  serverSeqStart: (json['server_seq_start'] as num).toInt(),
  jetstreamSubjectIn: json['jetstream_subject_in'] as String?,
  jetstreamSubjectOut: json['jetstream_subject_out'] as String?,
  environmentId: json['environment_id'] as String?,
  threadId: json['thread_id'] as String?,
);

Map<String, dynamic> _$SessionToJson(Session instance) => <String, dynamic>{
  'session_id': instance.sessionId,
  'session_token': instance.sessionToken,
  'mode': instance.mode,
  'server_seq_start': instance.serverSeqStart,
  if (instance.jetstreamSubjectIn case final value?)
    'jetstream_subject_in': value,
  if (instance.jetstreamSubjectOut case final value?)
    'jetstream_subject_out': value,
  if (instance.environmentId case final value?) 'environment_id': value,
  if (instance.threadId case final value?) 'thread_id': value,
};

EnvironmentInfo _$EnvironmentInfoFromJson(Map json) => EnvironmentInfo(
  environmentId: json['environment_id'] as String,
  userId: json['user_id'] as String?,
  workerKind: json['worker_kind'] as String,
  machineName: json['machine_name'] as String,
  osArch: json['os_arch'] as String?,
  gitInfo: (json['git_info'] as Map?)?.map((k, e) => MapEntry(k as String, e)),
  capabilities: (json['capabilities'] as List<dynamic>?)
      ?.map((e) => e as String)
      .toList(),
  state: json['state'] as String,
  lastSeenAt: (json['last_seen_at'] as num?)?.toInt(),
);

Map<String, dynamic> _$EnvironmentInfoToJson(EnvironmentInfo instance) =>
    <String, dynamic>{
      'environment_id': instance.environmentId,
      if (instance.userId case final value?) 'user_id': value,
      'worker_kind': instance.workerKind,
      'machine_name': instance.machineName,
      if (instance.osArch case final value?) 'os_arch': value,
      if (instance.gitInfo case final value?) 'git_info': value,
      if (instance.capabilities case final value?) 'capabilities': value,
      'state': instance.state,
      if (instance.lastSeenAt case final value?) 'last_seen_at': value,
    };
