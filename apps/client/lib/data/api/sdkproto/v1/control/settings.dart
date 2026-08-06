import 'package:json_annotation/json_annotation.dart';

part 'settings.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class ApplyFlagSettings {
  final String subtype;
  final Map<String, dynamic> settings;

  ApplyFlagSettings({
    this.subtype = 'apply_flag_settings',
    required this.settings,
  });

  factory ApplyFlagSettings.fromJson(Map<String, dynamic> json) =>
      _$ApplyFlagSettingsFromJson(json);
  Map<String, dynamic> toJson() => _$ApplyFlagSettingsToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class GetSettings {
  final String subtype;
  GetSettings({this.subtype = 'get_settings'});
  factory GetSettings.fromJson(Map<String, dynamic> json) =>
      _$GetSettingsFromJson(json);
  Map<String, dynamic> toJson() => _$GetSettingsToJson(this);
}
