// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'common.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

AnthropicMessage _$AnthropicMessageFromJson(Map json) =>
    AnthropicMessage(role: json['role'] as String, content: json['content']);

Map<String, dynamic> _$AnthropicMessageToJson(AnthropicMessage instance) =>
    <String, dynamic>{
      'role': instance.role,
      if (instance.content case final value?) 'content': value,
    };

ModelUsage _$ModelUsageFromJson(Map<String, dynamic> json) => ModelUsage(
  inputTokens: (json['inputTokens'] as num).toInt(),
  outputTokens: (json['outputTokens'] as num).toInt(),
  cacheReadInputTokens: (json['cacheReadInputTokens'] as num).toInt(),
  cacheCreationInputTokens: (json['cacheCreationInputTokens'] as num).toInt(),
  webSearchRequests: (json['webSearchRequests'] as num).toInt(),
  costUSD: (json['costUSD'] as num).toDouble(),
  contextWindow: (json['contextWindow'] as num).toInt(),
  maxOutputTokens: (json['maxOutputTokens'] as num).toInt(),
);

Map<String, dynamic> _$ModelUsageToJson(ModelUsage instance) =>
    <String, dynamic>{
      'inputTokens': instance.inputTokens,
      'outputTokens': instance.outputTokens,
      'cacheReadInputTokens': instance.cacheReadInputTokens,
      'cacheCreationInputTokens': instance.cacheCreationInputTokens,
      'webSearchRequests': instance.webSearchRequests,
      'costUSD': instance.costUSD,
      'contextWindow': instance.contextWindow,
      'maxOutputTokens': instance.maxOutputTokens,
    };
