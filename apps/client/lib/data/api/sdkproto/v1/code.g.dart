// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'code.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

CodeRequest _$CodeRequestFromJson(Map json) => CodeRequest(
  type: json['type'] as String? ?? 'code_request',
  requestId: json['request_id'] as String,
  method: json['method'] as String,
  params: (json['params'] as Map?)?.map((k, e) => MapEntry(k as String, e)),
);

Map<String, dynamic> _$CodeRequestToJson(CodeRequest instance) =>
    <String, dynamic>{
      'type': instance.type,
      'request_id': instance.requestId,
      'method': instance.method,
      if (instance.params case final value?) 'params': value,
    };

CodeResponse _$CodeResponseFromJson(Map json) => CodeResponse(
  type: json['type'] as String? ?? 'code_response',
  requestId: json['request_id'] as String,
  ok: json['ok'] as bool,
  result: (json['result'] as Map?)?.map((k, e) => MapEntry(k as String, e)),
  error: json['error'] as String?,
);

Map<String, dynamic> _$CodeResponseToJson(CodeResponse instance) =>
    <String, dynamic>{
      'type': instance.type,
      'request_id': instance.requestId,
      'ok': instance.ok,
      if (instance.result case final value?) 'result': value,
      if (instance.error case final value?) 'error': value,
    };

CodePtyChunk _$CodePtyChunkFromJson(Map json) => CodePtyChunk(
  type: json['type'] as String? ?? 'code_pty_chunk',
  ptyId: json['pty_id'] as String,
  data: base64Decode(json['data'] as String),
);

Map<String, dynamic> _$CodePtyChunkToJson(CodePtyChunk instance) =>
    <String, dynamic>{
      'type': instance.type,
      'pty_id': instance.ptyId,
      'data': base64Encode(instance.data),
    };

CodePtyInput _$CodePtyInputFromJson(Map json) => CodePtyInput(
  type: json['type'] as String? ?? 'code_pty_input',
  ptyId: json['pty_id'] as String,
  data: base64Decode(json['data'] as String),
);

Map<String, dynamic> _$CodePtyInputToJson(CodePtyInput instance) =>
    <String, dynamic>{
      'type': instance.type,
      'pty_id': instance.ptyId,
      'data': base64Encode(instance.data),
    };

CodePtyResize _$CodePtyResizeFromJson(Map json) => CodePtyResize(
  type: json['type'] as String? ?? 'code_pty_resize',
  ptyId: json['pty_id'] as String,
  cols: (json['cols'] as num).toInt(),
  rows: (json['rows'] as num).toInt(),
);

Map<String, dynamic> _$CodePtyResizeToJson(CodePtyResize instance) =>
    <String, dynamic>{
      'type': instance.type,
      'pty_id': instance.ptyId,
      'cols': instance.cols,
      'rows': instance.rows,
    };

CodePtyExit _$CodePtyExitFromJson(Map json) => CodePtyExit(
  type: json['type'] as String? ?? 'code_pty_exit',
  ptyId: json['pty_id'] as String,
  exitCode: (json['exit_code'] as num).toInt(),
  error: json['error'] as String?,
);

Map<String, dynamic> _$CodePtyExitToJson(CodePtyExit instance) =>
    <String, dynamic>{
      'type': instance.type,
      'pty_id': instance.ptyId,
      'exit_code': instance.exitCode,
      if (instance.error case final value?) 'error': value,
    };

CodeSessionEvent _$CodeSessionEventFromJson(Map json) => CodeSessionEvent(
  type: json['type'] as String? ?? 'code_session_event',
  taskId: json['task_id'] as String,
  event: Map<String, dynamic>.from(json['event'] as Map),
);

Map<String, dynamic> _$CodeSessionEventToJson(CodeSessionEvent instance) =>
    <String, dynamic>{
      'type': instance.type,
      'task_id': instance.taskId,
      'event': instance.event,
    };
