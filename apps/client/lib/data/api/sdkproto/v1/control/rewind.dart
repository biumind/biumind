import 'package:json_annotation/json_annotation.dart';

part 'rewind.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class RewindFiles {
  final String subtype;
  @JsonKey(name: 'user_message_id')
  final String userMessageId;
  @JsonKey(name: 'dry_run')
  final bool? dryRun;

  RewindFiles({
    this.subtype = 'rewind_files',
    required this.userMessageId,
    this.dryRun,
  });

  factory RewindFiles.fromJson(Map<String, dynamic> json) =>
      _$RewindFilesFromJson(json);
  Map<String, dynamic> toJson() => _$RewindFilesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class RewindFilesResponse {
  final bool canRewind;
  final String? error;
  final int? filesChanged;
  final int? insertions;
  final int? deletions;

  RewindFilesResponse({
    required this.canRewind,
    this.error,
    this.filesChanged,
    this.insertions,
    this.deletions,
  });

  factory RewindFilesResponse.fromJson(Map<String, dynamic> json) =>
      _$RewindFilesResponseFromJson(json);
  Map<String, dynamic> toJson() => _$RewindFilesResponseToJson(this);
}
