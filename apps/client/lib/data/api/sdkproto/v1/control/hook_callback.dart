import 'package:json_annotation/json_annotation.dart';

part 'hook_callback.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class HookCallback {
  final String subtype;
  @JsonKey(name: 'callback_id')
  final String callbackId;
  final Map<String, dynamic> input;
  @JsonKey(name: 'tool_use_id')
  final String? toolUseId;

  HookCallback({
    this.subtype = 'hook_callback',
    required this.callbackId,
    required this.input,
    this.toolUseId,
  });

  factory HookCallback.fromJson(Map<String, dynamic> json) =>
      _$HookCallbackFromJson(json);
  Map<String, dynamic> toJson() => _$HookCallbackToJson(this);
}
