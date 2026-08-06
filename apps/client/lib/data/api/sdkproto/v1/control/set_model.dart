// SetModel / SetPermissionMode / SetMaxThinkingTokens —— 三合一文件。

import 'package:json_annotation/json_annotation.dart';

part 'set_model.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SetModel {
  final String subtype;
  final String? model;

  SetModel({this.subtype = 'set_model', this.model});

  factory SetModel.fromJson(Map<String, dynamic> json) =>
      _$SetModelFromJson(json);
  Map<String, dynamic> toJson() => _$SetModelToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SetPermissionMode {
  final String subtype;
  final String mode;
  final bool? ultraplan;

  SetPermissionMode({
    this.subtype = 'set_permission_mode',
    required this.mode,
    this.ultraplan,
  });

  factory SetPermissionMode.fromJson(Map<String, dynamic> json) =>
      _$SetPermissionModeFromJson(json);
  Map<String, dynamic> toJson() => _$SetPermissionModeToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SetMaxThinkingTokens {
  final String subtype;
  @JsonKey(name: 'max_thinking_tokens')
  final int? maxThinkingTokens;

  SetMaxThinkingTokens({
    this.subtype = 'set_max_thinking_tokens',
    this.maxThinkingTokens,
  });

  factory SetMaxThinkingTokens.fromJson(Map<String, dynamic> json) =>
      _$SetMaxThinkingTokensFromJson(json);
  Map<String, dynamic> toJson() => _$SetMaxThinkingTokensToJson(this);
}
