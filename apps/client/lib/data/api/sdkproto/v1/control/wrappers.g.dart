// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'wrappers.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

SDKControlRequest _$SDKControlRequestFromJson(Map json) => SDKControlRequest(
  type: json['type'] as String? ?? 'control_request',
  requestId: json['request_id'] as String,
  request: Map<String, dynamic>.from(json['request'] as Map),
);

Map<String, dynamic> _$SDKControlRequestToJson(SDKControlRequest instance) =>
    <String, dynamic>{
      'type': instance.type,
      'request_id': instance.requestId,
      'request': instance.request,
    };

ControlResponseBody _$ControlResponseBodyFromJson(Map json) =>
    ControlResponseBody(
      subtype: json['subtype'] as String,
      requestId: json['request_id'] as String,
      response: (json['response'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e),
      ),
      error: json['error'] as String?,
      pendingPermissionRequests:
          json['pending_permission_requests'] as List<dynamic>?,
    );

Map<String, dynamic> _$ControlResponseBodyToJson(
  ControlResponseBody instance,
) => <String, dynamic>{
  'subtype': instance.subtype,
  'request_id': instance.requestId,
  if (instance.response case final value?) 'response': value,
  if (instance.error case final value?) 'error': value,
  if (instance.pendingPermissionRequests case final value?)
    'pending_permission_requests': value,
};

SDKControlResponse _$SDKControlResponseFromJson(Map json) => SDKControlResponse(
  type: json['type'] as String? ?? 'control_response',
  response: ControlResponseBody.fromJson(
    Map<String, dynamic>.from(json['response'] as Map),
  ),
);

Map<String, dynamic> _$SDKControlResponseToJson(SDKControlResponse instance) =>
    <String, dynamic>{'type': instance.type, 'response': instance.response};

SDKControlCancelRequest _$SDKControlCancelRequestFromJson(Map json) =>
    SDKControlCancelRequest(
      type: json['type'] as String? ?? 'control_cancel_request',
      requestId: json['request_id'] as String,
    );

Map<String, dynamic> _$SDKControlCancelRequestToJson(
  SDKControlCancelRequest instance,
) => <String, dynamic>{'type': instance.type, 'request_id': instance.requestId};
