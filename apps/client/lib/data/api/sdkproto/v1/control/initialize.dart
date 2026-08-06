import 'package:json_annotation/json_annotation.dart';

part 'initialize.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class Initialize {
  final String subtype;
  final dynamic hooks;
  final dynamic sdkMcpServers;
  final dynamic jsonSchema;
  final String? systemPrompt;
  final String? appendSystemPrompt;
  final dynamic agents;
  final bool? promptSuggestions;
  final bool? agentProgressSummaries;

  Initialize({
    this.subtype = 'initialize',
    this.hooks,
    this.sdkMcpServers,
    this.jsonSchema,
    this.systemPrompt,
    this.appendSystemPrompt,
    this.agents,
    this.promptSuggestions,
    this.agentProgressSummaries,
  });

  factory Initialize.fromJson(Map<String, dynamic> json) =>
      _$InitializeFromJson(json);
  Map<String, dynamic> toJson() => _$InitializeToJson(this);
}
