// CancelAsyncMessage —— 取消正在生成中的某条 user message 触发的 turn。
// 注意跟 wrappers.SDKControlCancelRequest 不同（后者取消正在处理的 control request 本身）。

import 'package:json_annotation/json_annotation.dart';

part 'cancel.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class CancelAsyncMessage {
  final String subtype;
  @JsonKey(name: 'message_uuid')
  final String messageUuid;

  CancelAsyncMessage({
    this.subtype = 'cancel_async_message',
    required this.messageUuid,
  });

  factory CancelAsyncMessage.fromJson(Map<String, dynamic> json) =>
      _$CancelAsyncMessageFromJson(json);
  Map<String, dynamic> toJson() => _$CancelAsyncMessageToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class CancelAsyncMessageResponse {
  final bool cancelled;
  CancelAsyncMessageResponse({required this.cancelled});
  factory CancelAsyncMessageResponse.fromJson(Map<String, dynamic> json) =>
      _$CancelAsyncMessageResponseFromJson(json);
  Map<String, dynamic> toJson() => _$CancelAsyncMessageResponseToJson(this);
}
