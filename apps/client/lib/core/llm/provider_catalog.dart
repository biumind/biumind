// Provider-level helpers for client-side BYOK + model dispatch.
//
// P6: 删静态 builtin catalog (BuiltinProvider / CatalogModel /
// builtinProviders / builtinProviderById) —— global 模型清单改读
// model-relay GET /v1/me/models (relay_catalog_client.dart)。此文件只留
// 两个纯函数:
//   - providerIdForModel: model id → provider slug 启发式 (无精确表)
//   - protocolForProviderSlug: standard slug → client-side 直连协议
// 之前静态 catalog 还服务 Settings UI 预置 anthropic/openai/google 行,
// 现 API Keys 页用户自填 provider, 不再需要预置行。

/// Map a model id to its provider slug (启发式: 按前缀). 返 null = 自定义 /
/// 未知 (custom provider 可用任意 id)。
String? providerIdForModel(String modelId) {
  if (modelId.startsWith('claude-')) return 'anthropic';
  // OpenAI 系列: gpt-* / o1 / o3 / o4 推理线 / chatgpt-* 别名. 仅头像图标用,
  // 路由靠 thread.providerId 不靠此 (补 o3/o4/chatgpt 防 fallback 默认图标).
  if (modelId.startsWith('gpt-') ||
      modelId.startsWith('o1') ||
      modelId.startsWith('o3') ||
      modelId.startsWith('o4') ||
      modelId.startsWith('chatgpt')) {
    return 'openai';
  }
  if (modelId.startsWith('gemini-')) return 'google';
  return null;
}

/// Map a standard provider slug → client-side direct-connect wire protocol
/// shape. Used by client_side_resolver._effectiveProtocol + the API Keys test
/// path to pick the right SSE parser. Standard providers have a fixed shape
/// (Anthropic = /v1/messages, Google = :streamGenerateContent, everything
/// else = OpenAI-compatible /v1/chat/completions). `custom` is excluded —
/// its shape is user-chosen (stored on the record) so callers must branch
/// on `provider == 'custom'` before calling this. Matches server-side
/// model-relay P1 slug-driven adaptor selection.
String protocolForProviderSlug(String slug) {
  switch (slug) {
    case 'anthropic':
      return 'anthropic';
    case 'google':
      return 'google';
    default: // openai / deepseek / doubao / dashscope / qwen / moonshot /
      //      baichuan / volcengine / azure_openai — all OpenAI-compatible.
      return 'openai_compat';
  }
}
