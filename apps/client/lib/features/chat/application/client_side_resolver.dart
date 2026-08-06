// client_side_resolver.dart — client-side BYOK 命中判定。
//
// 判定 (providerId, model) 是否命中 client-side 直连: identity 有 is_client_side=true
// 记录 + provider 匹配 → 返 ClientSideTarget; 否则 null。key 不在端侧持 —— 桌面
// daemon 命中时用 user JWT 调 identity 取明文 key 本机直连 (不经 model-relay)。
//
// 含义「is_client_side=true 且 provider 匹配就走桌面 daemon」。手机端无 daemon,
// 走 relay 时 identity store 过滤跳 client-side → 该模型不可用 (UI 标「需桌面端」)。

import '../../../core/llm/provider_catalog.dart';
import '../../settings/data/api_keys_client.dart';

/// client-side 直连目标（由 resolveClientSide 解析后传入 chat_controller）。
/// key 不在端侧持 (daemon 自取 identity), 故只带 record_id/base_url/protocol。
class ClientSideTarget {
  final String recordId;
  final String protocol; // anthropic / google / openai_compat
  final String baseUrl;
  const ClientSideTarget({
    required this.recordId,
    required this.protocol,
    required this.baseUrl,
  });
}

/// client-side BYOK 路由解析。
///
/// 判断 (providerId, model) 是否命中 client-side 直连: 存在 is_client_side=true
/// 的 identity 记录 + provider 匹配 (standard 按 providerId / custom 按 model_globs
/// 匹配 model) → 返 ClientSideTarget; 否则 null (走 cloud). key 由桌面 daemon
/// 命中时调 identity 取, 端侧不持.
ClientSideTarget? resolveClientSide(
  List<ApiKeyEntry> keys,
  String? providerId,
  String model,
) {
  if (model.isEmpty) return null;
  for (final k in keys) {
    if (!k.isClientSide) continue;
    if (!_matchesProvider(k, providerId, model)) continue;
    return ClientSideTarget(
      recordId: k.id,
      protocol: _effectiveProtocol(k),
      baseUrl: k.baseUrl,
    );
  }
  return null;
}

bool _matchesProvider(ApiKeyEntry k, String? providerId, String model) {
  if (k.provider == 'custom') {
    // custom 按 model_globs 匹配 (无 providerId 可对)
    for (final g in k.modelGlobs) {
      if (_globMatch(g, model)) return true;
    }
    return false;
  }
  // standard provider: providerId 匹配 (方案 I 同 slug)
  return providerId != null && providerId == k.provider;
}

bool _globMatch(String g, String model) {
  if (g == '*') return true;
  if (g.endsWith('*')) return model.startsWith(g.substring(0, g.length - 1));
  return g == model;
}

/// 解析 client-side 直连的有效协议 shape. custom 用用户存值 (OpenAI 兼容 /
/// Anthropic / Google 任选); standard provider 按 slug 派生 (anthropic→
/// anthropic, google→google, 其余→openai_compat) —— 防 legacy 条目存了错
/// protocol (dialog 旧默认 openai_compat) 导致 standard google/anthropic
/// 误走 OpenAICompat 端点.
String _effectiveProtocol(ApiKeyEntry k) {
  if (k.provider == 'custom') {
    return k.protocol.isNotEmpty ? k.protocol : 'openai_compat';
  }
  return protocolForProviderSlug(k.provider);
}
