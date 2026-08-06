// Avatar 元数据 — provider id / 模型 id → (label, color)。
//
// 设计取舍:
//   * **不引入图片资源**。每个 provider 给 1-2 字标签 + 品牌色, 圆形
//     渲染。理由:
//      - bundle 体积零增长
//      - 离线 / 首启不需要等网络
//      - 没法律风险 (品牌 logo 商用要授权; 字符不会)
//   * 触底 fallback: 用 model id 的首字符 + 中性紫色, 这样自定义
//     provider / 未知 model 也有视觉占位, 不会塌成空白圆。
//   * 文案 / 数据都不走 i18n — Provider 名字本身就是英文符号
//     (Claude/GPT/Gemini 中英都是这写法)。

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../core/llm/provider_catalog.dart';

class AvatarMeta {
  const AvatarMeta({
    required this.label,
    required this.background,
    required this.foreground,
  });

  final String label;       // 1-2 字符, 显示在圆里
  final Color background;
  final Color foreground;
}

/// 已知 provider → 元数据。新加 provider 时更新这里 (key 跟
/// `BuiltinProvider.id` 对齐)。
/// 品牌色 source-of-truth 在 lib/app/theme/category_colors.dart::ProviderBrand。
const Map<String, AvatarMeta> _knownProviders = {
  'anthropic': AvatarMeta(
    label: 'C',
    background: ProviderBrand.anthropic,
    foreground: Colors.white,
  ),
  'openai': AvatarMeta(
    label: 'G',
    background: ProviderBrand.openai,
    foreground: Colors.white,
  ),
  'google': AvatarMeta(
    label: 'G',
    background: ProviderBrand.google,
    foreground: Colors.white,
  ),
  'deepseek': AvatarMeta(
    label: 'D',
    background: ProviderBrand.deepseek,
    foreground: Colors.white,
  ),
  'qwen': AvatarMeta(
    label: 'Q',
    background: ProviderBrand.qwen,
    foreground: Colors.white,
  ),
  'ollama': AvatarMeta(
    label: '🦙',
    background: ProviderBrand.ollama,
    foreground: Colors.white,
  ),
  'azure': AvatarMeta(
    label: 'A',
    background: ProviderBrand.azure,
    foreground: Colors.white,
  ),
};

/// 给一个 model id 决定渲染哪个 avatar。
///
/// 优先级:
///   1. providerIdForModel(model) 命中已知 provider → 用对应 meta
///   2. heuristic 前缀 (claude- / gpt- / o1 / gemini-) — providerIdForModel
///      内部已经做了, 这里靠它兜底
///   3. fallback: model 首字符大写 + 中性紫色
///
/// model=null/empty 时返回中性 meta (ai 字样), 给"未知模型"兜底。
AvatarMeta resolveAssistantAvatar(String? model) {
  if (model == null || model.isEmpty) {
    return _fallback('AI');
  }
  final providerId = providerIdForModel(model);
  if (providerId != null) {
    final hit = _knownProviders[providerId];
    if (hit != null) return hit;
  }
  // 自定义 provider 或没在已知表里 — 拿 model 首字符。
  return _fallback(model);
}

/// User avatar — 取 email / name 首字符大写, 全空时回退到通用图标
/// (调用方决定是图标还是字符 fallback)。
AvatarMeta resolveUserAvatar({String? email, String? name}) {
  final base = (name != null && name.trim().isNotEmpty) ? name : email;
  if (base == null || base.trim().isEmpty) {
    return AvatarMeta(
      label: '👤',
      background: BiuTokens.surfaceMuted,
      foreground: BiuTokens.textSecondary,
    );
  }
  final first = base.trim().runes.first;
  return AvatarMeta(
    label: String.fromCharCode(first).toUpperCase(),
    background: BiuTokens.purple,
    foreground: Colors.white,
  );
}

AvatarMeta _fallback(String hint) {
  final cleaned = hint.trim();
  final label = cleaned.isEmpty
      ? '?'
      : String.fromCharCode(cleaned.runes.first).toUpperCase();
  return AvatarMeta(
    label: label,
    background: BiuTokens.purpleSoft,
    foreground: BiuTokens.purple,
  );
}
