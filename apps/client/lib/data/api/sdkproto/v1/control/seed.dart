import 'package:json_annotation/json_annotation.dart';

part 'seed.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SeedReadState {
  final String subtype;
  final String path;
  final double mtime;

  SeedReadState({
    this.subtype = 'seed_read_state',
    required this.path,
    required this.mtime,
  });

  factory SeedReadState.fromJson(Map<String, dynamic> json) =>
      _$SeedReadStateFromJson(json);
  Map<String, dynamic> toJson() => _$SeedReadStateToJson(this);
}
