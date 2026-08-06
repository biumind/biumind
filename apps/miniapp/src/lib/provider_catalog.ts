// lib/provider_catalog.ts — provider/model 启发式 helper.
//
// P6: 删静态 builtin catalog (CatalogModel / BuiltinProvider /
// BUILTIN_PROVIDERS / builtinProviderById) —— global 模型清单改读
// model-relay GET /v1/me/models (见 data/api/models.ts loadModelEntries)。
// 此文件只留:
//   - BIUMIND_OFFICIAL_MODEL: official 渠道哨兵 model id (fallback 用)
//   - providerIdForModel / modelDisplayName / providerDisplayName: 启发式

/** Official 渠道 (BiuMind 后端配的官方账号) 的哨兵 model id. */
export const BIUMIND_OFFICIAL_MODEL = 'biumind-default';

/** 反查 model id 属于哪个 provider slug (启发式: 前缀). undefined = 自定义. */
export function providerIdForModel(modelId: string): string | undefined {
  if (modelId.startsWith('claude-')) return 'anthropic';
  if (modelId.startsWith('gpt-') || modelId.startsWith('o1')) return 'openai';
  if (modelId.startsWith('gemini-')) return 'google';
  return undefined;
}

/** 给定 model id 找 displayName — P6 删 catalog 后原样返回 modelId
 *  (实际 display_name 由 /v1/me/models 提供)。 */
export function modelDisplayName(modelId: string): string {
  return modelId;
}

/** 给定 provider slug 找 displayName — 首字母大写兜底. */
export function providerDisplayName(slug: string): string {
  return slug.charAt(0).toUpperCase() + slug.slice(1);
}
