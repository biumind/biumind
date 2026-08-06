import 'package:json_annotation/json_annotation.dart';

part 'settings.g.dart';

class SettingSource {
  static const userSettings = 'userSettings';
  static const projectSettings = 'projectSettings';
  static const localSettings = 'localSettings';
  static const managedSettings = 'managedSettings';
  static const policySettings = 'policySettings';
  static const flag = 'flag';
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class GetSettingsResponse {
  final Map<String, dynamic> effective;
  final Map<String, dynamic> sources;
  final Map<String, String> applied;

  GetSettingsResponse({
    required this.effective,
    required this.sources,
    required this.applied,
  });

  factory GetSettingsResponse.fromJson(Map<String, dynamic> json) =>
      _$GetSettingsResponseFromJson(json);
  Map<String, dynamic> toJson() => _$GetSettingsResponseToJson(this);
}
