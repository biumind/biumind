import 'package:json_annotation/json_annotation.dart';

part 'elicitation.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class Elicitation {
  final String subtype;
  @JsonKey(name: 'mcp_server_name')
  final String mcpServerName;
  final String message;
  final String? mode; // form | url
  final String? url;
  @JsonKey(name: 'elicitation_id')
  final String? elicitationId;
  @JsonKey(name: 'requested_schema')
  final Map<String, dynamic>? requestedSchema;

  Elicitation({
    this.subtype = 'elicitation',
    required this.mcpServerName,
    required this.message,
    this.mode,
    this.url,
    this.elicitationId,
    this.requestedSchema,
  });

  factory Elicitation.fromJson(Map<String, dynamic> json) =>
      _$ElicitationFromJson(json);
  Map<String, dynamic> toJson() => _$ElicitationToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ElicitationResponse {
  final String action; // accept | decline | cancel
  final Map<String, dynamic>? content;

  ElicitationResponse({required this.action, this.content});

  factory ElicitationResponse.fromJson(Map<String, dynamic> json) =>
      _$ElicitationResponseFromJson(json);
  Map<String, dynamic> toJson() => _$ElicitationResponseToJson(this);
}
