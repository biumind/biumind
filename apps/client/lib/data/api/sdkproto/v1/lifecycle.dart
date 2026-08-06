// BiuMind 自有 lifecycle 帧 —— 8 种（含 compact_started/compact_finished）。

import 'package:json_annotation/json_annotation.dart';

part 'lifecycle.g.dart';

abstract class Lifecycle {
  String get type;
  Map<String, dynamic> toJson();

  static Lifecycle fromJson(Map<String, dynamic> json) {
    final t = json['type'] as String? ?? '';
    switch (t) {
      case 'keep_alive':
        return KeepAlive.fromJson(json);
      case 'update_environment_variables':
        return UpdateEnvironmentVariables.fromJson(json);
      case 'biumind.session_desynced':
        return SessionDesynced.fromJson(json);
      case 'biumind.session_paused':
        return SessionPaused.fromJson(json);
      case 'biumind.session_resumed':
        return SessionResumed.fromJson(json);
      case 'biumind.session_primary_promoted':
        return SessionPrimaryPromoted.fromJson(json);
      case 'biumind.compact_started':
        return BiumindCompactStarted.fromJson(json);
      case 'biumind.compact_finished':
        return BiumindCompactFinished.fromJson(json);
      default:
        throw ArgumentError('unknown lifecycle type: $t');
    }
  }
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class KeepAlive implements Lifecycle {
  @override
  final String type;
  final int? ts;

  KeepAlive({this.type = 'keep_alive', this.ts});

  factory KeepAlive.fromJson(Map<String, dynamic> json) =>
      _$KeepAliveFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$KeepAliveToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class UpdateEnvironmentVariables implements Lifecycle {
  @override
  final String type;
  final Map<String, String> variables;

  UpdateEnvironmentVariables({
    this.type = 'update_environment_variables',
    required this.variables,
  });

  factory UpdateEnvironmentVariables.fromJson(Map<String, dynamic> json) =>
      _$UpdateEnvironmentVariablesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$UpdateEnvironmentVariablesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionDesynced implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  @JsonKey(name: 'final_result_url')
  final String? finalResultUrl;
  @JsonKey(name: 'since_seq')
  final int? sinceSeq;

  SessionDesynced({
    this.type = 'biumind.session_desynced',
    required this.sessionId,
    this.finalResultUrl,
    this.sinceSeq,
  });

  factory SessionDesynced.fromJson(Map<String, dynamic> json) =>
      _$SessionDesyncedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$SessionDesyncedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionPaused implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  final String? reason;

  SessionPaused({
    this.type = 'biumind.session_paused',
    required this.sessionId,
    this.reason,
  });

  factory SessionPaused.fromJson(Map<String, dynamic> json) =>
      _$SessionPausedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$SessionPausedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionResumed implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  @JsonKey(name: 'since_seq')
  final int? sinceSeq;

  SessionResumed({
    this.type = 'biumind.session_resumed',
    required this.sessionId,
    this.sinceSeq,
  });

  factory SessionResumed.fromJson(Map<String, dynamic> json) =>
      _$SessionResumedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$SessionResumedToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SessionPrimaryPromoted implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  @JsonKey(name: 'primary_replica')
  final String primaryReplica;

  SessionPrimaryPromoted({
    this.type = 'biumind.session_primary_promoted',
    required this.sessionId,
    required this.primaryReplica,
  });

  factory SessionPrimaryPromoted.fromJson(Map<String, dynamic> json) =>
      _$SessionPrimaryPromotedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$SessionPrimaryPromotedToJson(this);
}

/// biumindkit 拆分的 compact 开始事件 —— 协议层无对应概念，BiuMind 扩展。
@JsonSerializable(includeIfNull: false, anyMap: true)
class BiumindCompactStarted implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  final String reason;
  @JsonKey(name: 'tokens_before')
  final int tokensBefore;

  BiumindCompactStarted({
    this.type = 'biumind.compact_started',
    required this.sessionId,
    required this.reason,
    required this.tokensBefore,
  });

  factory BiumindCompactStarted.fromJson(Map<String, dynamic> json) =>
      _$BiumindCompactStartedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$BiumindCompactStartedToJson(this);
}

/// biumindkit compact 完成事件，含 token 节省报告。
@JsonSerializable(includeIfNull: false, anyMap: true)
class BiumindCompactFinished implements Lifecycle {
  @override
  final String type;
  @JsonKey(name: 'session_id')
  final String sessionId;
  @JsonKey(name: 'tokens_before')
  final int tokensBefore;
  @JsonKey(name: 'tokens_after')
  final int tokensAfter;
  @JsonKey(name: 'tokens_saved')
  final int tokensSaved;

  BiumindCompactFinished({
    this.type = 'biumind.compact_finished',
    required this.sessionId,
    required this.tokensBefore,
    required this.tokensAfter,
    required this.tokensSaved,
  });

  factory BiumindCompactFinished.fromJson(Map<String, dynamic> json) =>
      _$BiumindCompactFinishedFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$BiumindCompactFinishedToJson(this);
}
