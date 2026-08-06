// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'context.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

GetContextUsage _$GetContextUsageFromJson(Map json) =>
    GetContextUsage(subtype: json['subtype'] as String? ?? 'get_context_usage');

Map<String, dynamic> _$GetContextUsageToJson(GetContextUsage instance) =>
    <String, dynamic>{'subtype': instance.subtype};

GetContextUsageResponse _$GetContextUsageResponseFromJson(Map json) =>
    GetContextUsageResponse(
      totalTokens: (json['totalTokens'] as num).toInt(),
      maxTokens: (json['maxTokens'] as num).toInt(),
      categories: json['categories'] as List<dynamic>?,
      gridRows: json['gridRows'] as List<dynamic>?,
      memoryFiles: json['memoryFiles'] as List<dynamic>?,
      mcpTools: json['mcpTools'] as List<dynamic>?,
      agents: json['agents'] as List<dynamic>?,
      skills: json['skills'] as List<dynamic>?,
      messageBreakdown: (json['messageBreakdown'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e),
      ),
      apiUsage: (json['apiUsage'] as Map?)?.map(
        (k, e) => MapEntry(k as String, e),
      ),
    );

Map<String, dynamic> _$GetContextUsageResponseToJson(
  GetContextUsageResponse instance,
) => <String, dynamic>{
  'totalTokens': instance.totalTokens,
  'maxTokens': instance.maxTokens,
  if (instance.categories case final value?) 'categories': value,
  if (instance.gridRows case final value?) 'gridRows': value,
  if (instance.memoryFiles case final value?) 'memoryFiles': value,
  if (instance.mcpTools case final value?) 'mcpTools': value,
  if (instance.agents case final value?) 'agents': value,
  if (instance.skills case final value?) 'skills': value,
  if (instance.messageBreakdown case final value?) 'messageBreakdown': value,
  if (instance.apiUsage case final value?) 'apiUsage': value,
};
