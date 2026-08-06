// SDKControlRequest / SDKControlResponse / SDKControlCancelRequest 三个壳。
//
// 内部 request 字段是 21 个 ControlRequestInner 之一 —— Dart 这里用 Map<String, dynamic>
// 保 raw（具体类型由调用方按 subtype peek 后用对应 Inner 类的 fromJson 解析）。

import 'package:json_annotation/json_annotation.dart';

part 'wrappers.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKControlRequest {
  final String type;
  @JsonKey(name: 'request_id')
  final String requestId;
  final Map<String, dynamic> request;

  SDKControlRequest({
    this.type = 'control_request',
    required this.requestId,
    required this.request,
  });

  factory SDKControlRequest.fromJson(Map<String, dynamic> json) =>
      _$SDKControlRequestFromJson(json);
  Map<String, dynamic> toJson() => _$SDKControlRequestToJson(this);

  String get subtype => request['subtype'] as String? ?? '';
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ControlResponseBody {
  final String subtype; // success | error
  @JsonKey(name: 'request_id')
  final String requestId;
  final Map<String, dynamic>? response;
  final String? error;
  @JsonKey(name: 'pending_permission_requests')
  final List<dynamic>? pendingPermissionRequests;

  ControlResponseBody({
    required this.subtype,
    required this.requestId,
    this.response,
    this.error,
    this.pendingPermissionRequests,
  });

  factory ControlResponseBody.fromJson(Map<String, dynamic> json) =>
      _$ControlResponseBodyFromJson(json);
  Map<String, dynamic> toJson() => _$ControlResponseBodyToJson(this);

  bool get isSuccess => subtype == 'success';
  bool get isError => subtype == 'error';
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKControlResponse {
  final String type;
  final ControlResponseBody response;

  SDKControlResponse({this.type = 'control_response', required this.response});

  factory SDKControlResponse.fromJson(Map<String, dynamic> json) =>
      _$SDKControlResponseFromJson(json);
  Map<String, dynamic> toJson() => _$SDKControlResponseToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKControlCancelRequest {
  final String type;
  @JsonKey(name: 'request_id')
  final String requestId;

  SDKControlCancelRequest({
    this.type = 'control_cancel_request',
    required this.requestId,
  });

  factory SDKControlCancelRequest.fromJson(Map<String, dynamic> json) =>
      _$SDKControlCancelRequestFromJson(json);
  Map<String, dynamic> toJson() => _$SDKControlCancelRequestToJson(this);
}
