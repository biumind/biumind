// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'rewind.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

RewindFiles _$RewindFilesFromJson(Map json) => RewindFiles(
  subtype: json['subtype'] as String? ?? 'rewind_files',
  userMessageId: json['user_message_id'] as String,
  dryRun: json['dry_run'] as bool?,
);

Map<String, dynamic> _$RewindFilesToJson(RewindFiles instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'user_message_id': instance.userMessageId,
      if (instance.dryRun case final value?) 'dry_run': value,
    };

RewindFilesResponse _$RewindFilesResponseFromJson(Map json) =>
    RewindFilesResponse(
      canRewind: json['canRewind'] as bool,
      error: json['error'] as String?,
      filesChanged: (json['filesChanged'] as num?)?.toInt(),
      insertions: (json['insertions'] as num?)?.toInt(),
      deletions: (json['deletions'] as num?)?.toInt(),
    );

Map<String, dynamic> _$RewindFilesResponseToJson(
  RewindFilesResponse instance,
) => <String, dynamic>{
  'canRewind': instance.canRewind,
  if (instance.error case final value?) 'error': value,
  if (instance.filesChanged case final value?) 'filesChanged': value,
  if (instance.insertions case final value?) 'insertions': value,
  if (instance.deletions case final value?) 'deletions': value,
};
