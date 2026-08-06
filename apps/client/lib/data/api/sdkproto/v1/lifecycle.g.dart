// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'lifecycle.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

KeepAlive _$KeepAliveFromJson(Map json) => KeepAlive(
  type: json['type'] as String? ?? 'keep_alive',
  ts: (json['ts'] as num?)?.toInt(),
);

Map<String, dynamic> _$KeepAliveToJson(KeepAlive instance) => <String, dynamic>{
  'type': instance.type,
  if (instance.ts case final value?) 'ts': value,
};

UpdateEnvironmentVariables _$UpdateEnvironmentVariablesFromJson(Map json) =>
    UpdateEnvironmentVariables(
      type: json['type'] as String? ?? 'update_environment_variables',
      variables: Map<String, String>.from(json['variables'] as Map),
    );

Map<String, dynamic> _$UpdateEnvironmentVariablesToJson(
  UpdateEnvironmentVariables instance,
) => <String, dynamic>{'type': instance.type, 'variables': instance.variables};

SessionDesynced _$SessionDesyncedFromJson(Map json) => SessionDesynced(
  type: json['type'] as String? ?? 'biumind.session_desynced',
  sessionId: json['session_id'] as String,
  finalResultUrl: json['final_result_url'] as String?,
  sinceSeq: (json['since_seq'] as num?)?.toInt(),
);

Map<String, dynamic> _$SessionDesyncedToJson(SessionDesynced instance) =>
    <String, dynamic>{
      'type': instance.type,
      'session_id': instance.sessionId,
      if (instance.finalResultUrl case final value?) 'final_result_url': value,
      if (instance.sinceSeq case final value?) 'since_seq': value,
    };

SessionPaused _$SessionPausedFromJson(Map json) => SessionPaused(
  type: json['type'] as String? ?? 'biumind.session_paused',
  sessionId: json['session_id'] as String,
  reason: json['reason'] as String?,
);

Map<String, dynamic> _$SessionPausedToJson(SessionPaused instance) =>
    <String, dynamic>{
      'type': instance.type,
      'session_id': instance.sessionId,
      if (instance.reason case final value?) 'reason': value,
    };

SessionResumed _$SessionResumedFromJson(Map json) => SessionResumed(
  type: json['type'] as String? ?? 'biumind.session_resumed',
  sessionId: json['session_id'] as String,
  sinceSeq: (json['since_seq'] as num?)?.toInt(),
);

Map<String, dynamic> _$SessionResumedToJson(SessionResumed instance) =>
    <String, dynamic>{
      'type': instance.type,
      'session_id': instance.sessionId,
      if (instance.sinceSeq case final value?) 'since_seq': value,
    };

SessionPrimaryPromoted _$SessionPrimaryPromotedFromJson(Map json) =>
    SessionPrimaryPromoted(
      type: json['type'] as String? ?? 'biumind.session_primary_promoted',
      sessionId: json['session_id'] as String,
      primaryReplica: json['primary_replica'] as String,
    );

Map<String, dynamic> _$SessionPrimaryPromotedToJson(
  SessionPrimaryPromoted instance,
) => <String, dynamic>{
  'type': instance.type,
  'session_id': instance.sessionId,
  'primary_replica': instance.primaryReplica,
};

BiumindCompactStarted _$BiumindCompactStartedFromJson(Map json) =>
    BiumindCompactStarted(
      type: json['type'] as String? ?? 'biumind.compact_started',
      sessionId: json['session_id'] as String,
      reason: json['reason'] as String,
      tokensBefore: (json['tokens_before'] as num).toInt(),
    );

Map<String, dynamic> _$BiumindCompactStartedToJson(
  BiumindCompactStarted instance,
) => <String, dynamic>{
  'type': instance.type,
  'session_id': instance.sessionId,
  'reason': instance.reason,
  'tokens_before': instance.tokensBefore,
};

BiumindCompactFinished _$BiumindCompactFinishedFromJson(Map json) =>
    BiumindCompactFinished(
      type: json['type'] as String? ?? 'biumind.compact_finished',
      sessionId: json['session_id'] as String,
      tokensBefore: (json['tokens_before'] as num).toInt(),
      tokensAfter: (json['tokens_after'] as num).toInt(),
      tokensSaved: (json['tokens_saved'] as num).toInt(),
    );

Map<String, dynamic> _$BiumindCompactFinishedToJson(
  BiumindCompactFinished instance,
) => <String, dynamic>{
  'type': instance.type,
  'session_id': instance.sessionId,
  'tokens_before': instance.tokensBefore,
  'tokens_after': instance.tokensAfter,
  'tokens_saved': instance.tokensSaved,
};
