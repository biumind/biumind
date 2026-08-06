// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'settings.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

GetSettingsResponse _$GetSettingsResponseFromJson(Map json) =>
    GetSettingsResponse(
      effective: Map<String, dynamic>.from(json['effective'] as Map),
      sources: Map<String, dynamic>.from(json['sources'] as Map),
      applied: Map<String, String>.from(json['applied'] as Map),
    );

Map<String, dynamic> _$GetSettingsResponseToJson(
  GetSettingsResponse instance,
) => <String, dynamic>{
  'effective': instance.effective,
  'sources': instance.sources,
  'applied': instance.applied,
};
