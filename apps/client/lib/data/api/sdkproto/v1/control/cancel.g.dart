// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'cancel.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

CancelAsyncMessage _$CancelAsyncMessageFromJson(Map json) => CancelAsyncMessage(
  subtype: json['subtype'] as String? ?? 'cancel_async_message',
  messageUuid: json['message_uuid'] as String,
);

Map<String, dynamic> _$CancelAsyncMessageToJson(CancelAsyncMessage instance) =>
    <String, dynamic>{
      'subtype': instance.subtype,
      'message_uuid': instance.messageUuid,
    };

CancelAsyncMessageResponse _$CancelAsyncMessageResponseFromJson(Map json) =>
    CancelAsyncMessageResponse(cancelled: json['cancelled'] as bool);

Map<String, dynamic> _$CancelAsyncMessageResponseToJson(
  CancelAsyncMessageResponse instance,
) => <String, dynamic>{'cancelled': instance.cancelled};
