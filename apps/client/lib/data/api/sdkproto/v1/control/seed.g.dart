// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'seed.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SeedReadState _$SeedReadStateFromJson(Map json) => SeedReadState(
  subtype: json['subtype'] as String? ?? 'seed_read_state',
  path: json['path'] as String,
  mtime: (json['mtime'] as num).toDouble(),
);

Map<String, dynamic> _$SeedReadStateToJson(SeedReadState instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'path': instance.path,
      'mtime': instance.mtime,
    };
