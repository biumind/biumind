import 'package:json_annotation/json_annotation.dart';

part 'context.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class GetContextUsage {
  final String subtype;
  GetContextUsage({this.subtype = 'get_context_usage'});
  factory GetContextUsage.fromJson(Map<String, dynamic> json) =>
      _$GetContextUsageFromJson(json);
  Map<String, dynamic> toJson() => _$GetContextUsageToJson(this);
}

/// 大型嵌套响应 —— 各端只关心自己用的部分，所以用 Map 保 raw。
@JsonSerializable(includeIfNull: false, anyMap: true)
class GetContextUsageResponse {
  final int totalTokens;
  final int maxTokens;
  final List<dynamic>? categories;
  final List<dynamic>? gridRows;
  final List<dynamic>? memoryFiles;
  final List<dynamic>? mcpTools;
  final List<dynamic>? agents;
  final List<dynamic>? skills;
  final Map<String, dynamic>? messageBreakdown;
  final Map<String, dynamic>? apiUsage;

  GetContextUsageResponse({
    required this.totalTokens,
    required this.maxTokens,
    this.categories,
    this.gridRows,
    this.memoryFiles,
    this.mcpTools,
    this.agents,
    this.skills,
    this.messageBreakdown,
    this.apiUsage,
  });

  factory GetContextUsageResponse.fromJson(Map<String, dynamic> json) =>
      _$GetContextUsageResponseFromJson(json);
  Map<String, dynamic> toJson() => _$GetContextUsageResponseToJson(this);
}
