// BiuMind 业务扩展 —— CreateSessionReq / Session / EnvironmentInfo + Mode / WorkerKind 常量。

import 'package:json_annotation/json_annotation.dart';

part 'biumind_ext.g.dart';

class Mode {
  static const chat = 'chat';
  static const agent = 'agent';
  static const task = 'task';
}

class WorkerKind {
  static const biuDaemon = 'biu_daemon';
  static const biuCli = 'biu_cli';
  static const runtime = 'runtime';
}

class EnvState {
  static const online = 'online';
  static const offline = 'offline';
  static const draining = 'draining';
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class CreateSessionReq {
  final String mode;
  @JsonKey(name: 'environment_id')
  final String? environmentId;
  @JsonKey(name: 'thread_id')
  final String? threadId;
  final String? model;
  @JsonKey(name: 'system_prompt')
  final String? systemPrompt;
  @JsonKey(name: 'biumind_attachment_ids')
  final List<String>? biumindAttachmentIds;

  CreateSessionReq({
    required this.mode,
    this.environmentId,
    this.threadId,
    this.model,
    this.systemPrompt,
    this.biumindAttachmentIds,
  });

  factory CreateSessionReq.fromJson(Map<String, dynamic> json) =>
      _$CreateSessionReqFromJson(json);
  Map<String, dynamic> toJson() => _$CreateSessionReqToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class Session {
  @JsonKey(name: 'session_id')
  final String sessionId;
  @JsonKey(name: 'session_token')
  final String sessionToken;
  final String mode;
  @JsonKey(name: 'server_seq_start')
  final int serverSeqStart;
  @JsonKey(name: 'jetstream_subject_in')
  final String? jetstreamSubjectIn;
  @JsonKey(name: 'jetstream_subject_out')
  final String? jetstreamSubjectOut;
  @JsonKey(name: 'environment_id')
  final String? environmentId;
  @JsonKey(name: 'thread_id')
  final String? threadId;

  Session({
    required this.sessionId,
    required this.sessionToken,
    required this.mode,
    required this.serverSeqStart,
    this.jetstreamSubjectIn,
    this.jetstreamSubjectOut,
    this.environmentId,
    this.threadId,
  });

  factory Session.fromJson(Map<String, dynamic> json) =>
      _$SessionFromJson(json);
  Map<String, dynamic> toJson() => _$SessionToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class EnvironmentInfo {
  @JsonKey(name: 'environment_id')
  final String environmentId;
  @JsonKey(name: 'user_id')
  final String? userId;
  @JsonKey(name: 'worker_kind')
  final String workerKind;
  @JsonKey(name: 'machine_name')
  final String machineName;
  @JsonKey(name: 'os_arch')
  final String? osArch;
  @JsonKey(name: 'git_info')
  final Map<String, dynamic>? gitInfo;
  final List<String>? capabilities;
  final String state;
  @JsonKey(name: 'last_seen_at')
  final int? lastSeenAt;

  EnvironmentInfo({
    required this.environmentId,
    this.userId,
    required this.workerKind,
    required this.machineName,
    this.osArch,
    this.gitInfo,
    this.capabilities,
    required this.state,
    this.lastSeenAt,
  });

  factory EnvironmentInfo.fromJson(Map<String, dynamic> json) =>
      _$EnvironmentInfoFromJson(json);
  Map<String, dynamic> toJson() => _$EnvironmentInfoToJson(this);
}
