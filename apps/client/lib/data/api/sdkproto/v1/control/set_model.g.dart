// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'set_model.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SetModel _$SetModelFromJson(Map json) => SetModel(
  subtype: json['subtype'] as String? ?? 'set_model',
  model: json['model'] as String?,
);

Map<String, dynamic> _$SetModelToJson(SetModel instance) => <String, dynamic>{
  'subtype': instance.subtype,
  if (instance.model case final value?) 'model': value,
};

SetPermissionMode _$SetPermissionModeFromJson(Map json) => SetPermissionMode(
  subtype: json['subtype'] as String? ?? 'set_permission_mode',
  mode: json['mode'] as String,
  ultraplan: json['ultraplan'] as bool?,
);

Map<String, dynamic> _$SetPermissionModeToJson(SetPermissionMode instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'mode': instance.mode,
      if (instance.ultraplan case final value?) 'ultraplan': value,
    };

SetMaxThinkingTokens _$SetMaxThinkingTokensFromJson(Map json) =>
    SetMaxThinkingTokens(
      subtype: json['subtype'] as String? ?? 'set_max_thinking_tokens',
      maxThinkingTokens: (json['max_thinking_tokens'] as num?)?.toInt(),
    );

Map<String, dynamic> _$SetMaxThinkingTokensToJson(
  SetMaxThinkingTokens instance,
) => <String, dynamic>{
  'subtype': instance.subtype,
  if (instance.maxThinkingTokens case final value?)
    'max_thinking_tokens': value,
};
