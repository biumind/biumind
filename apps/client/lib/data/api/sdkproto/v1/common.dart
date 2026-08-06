// BiuMind SDK Protocol v1 — Common Types
//
// 字段名严格对齐服务端（json tag 同 Go 端）。Dart 类名用 UpperCamelCase；
// JSON 字段名保留服务端原名（snake_case 与 camelCase 混用，跟服务端一致）。
//
// 不要"统一"成一种风格 —— 三端会因此对不上。

import 'package:json_annotation/json_annotation.dart';

part 'common.g.dart';

/// Anthropic Messages API 的 message 对象。content 可以是 string 或 content block 数组。
/// 因为 content block 结构复杂且演化快，这里 content 留 dynamic。
@JsonSerializable(includeIfNull: false, anyMap: true)
class AnthropicMessage {
  final String role;
  final dynamic content;

  AnthropicMessage({required this.role, this.content});

  factory AnthropicMessage.fromJson(Map<String, dynamic> json) =>
      _$AnthropicMessageFromJson(json);
  Map<String, dynamic> toJson() => _$AnthropicMessageToJson(this);
}

/// 对应 ModelUsage schema。所有字段 required。
@JsonSerializable(includeIfNull: false)
class ModelUsage {
  final int inputTokens;
  final int outputTokens;
  final int cacheReadInputTokens;
  final int cacheCreationInputTokens;
  final int webSearchRequests;
  final double costUSD;
  final int contextWindow;
  final int maxOutputTokens;

  ModelUsage({
    required this.inputTokens,
    required this.outputTokens,
    required this.cacheReadInputTokens,
    required this.cacheCreationInputTokens,
    required this.webSearchRequests,
    required this.costUSD,
    required this.contextWindow,
    required this.maxOutputTokens,
  });

  factory ModelUsage.fromJson(Map<String, dynamic> json) =>
      _$ModelUsageFromJson(json);
  Map<String, dynamic> toJson() => _$ModelUsageToJson(this);
}
