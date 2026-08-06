// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'hook_callback.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

HookCallback _$HookCallbackFromJson(Map json) => HookCallback(
  subtype: json['subtype'] as String? ?? 'hook_callback',
  callbackId: json['callback_id'] as String,
  input: Map<String, dynamic>.from(json['input'] as Map),
  toolUseId: json['tool_use_id'] as String?,
);

Map<String, dynamic> _$HookCallbackToJson(HookCallback instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'callback_id': instance.callbackId,
      'input': instance.input,
      if (instance.toolUseId case final value?) 'tool_use_id': value,
    };
