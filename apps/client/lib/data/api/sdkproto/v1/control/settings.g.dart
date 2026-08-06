// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'settings.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

ApplyFlagSettings _$ApplyFlagSettingsFromJson(Map json) => ApplyFlagSettings(
  subtype: json['subtype'] as String? ?? 'apply_flag_settings',
  settings: Map<String, dynamic>.from(json['settings'] as Map),
);

Map<String, dynamic> _$ApplyFlagSettingsToJson(ApplyFlagSettings instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'settings': instance.settings,
    };

GetSettings _$GetSettingsFromJson(Map json) =>
    GetSettings(subtype: json['subtype'] as String? ?? 'get_settings');

Map<String, dynamic> _$GetSettingsToJson(GetSettings instance) =>
    <String, dynamic>{'subtype': instance.subtype};
