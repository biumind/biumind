// model_relay admin REST client. One function per endpoint in
// services/model-relay/internal/adminapi (see openapi.yaml).
//
// Reuses the shared `http` axios instance (auto Bearer + 401 refresh).
// All paths under /v1/admin/* — admin routes mounted on model-relay
// when MODEL_RELAY_ADMIN_DATABASE_URL is set.

import { http } from './http'
import type {
  Channel,
  ChannelInput,
  CredentialCreateRequest,
  CredentialSafe,
  CredentialUpdateRequest,
  FxRateUpsert,
  FxRatesEnvelope,
  Model,
  ModelDetail,
  ModelGroup,
  ModelInput,
  ModelsPage,
  PageOf,
  Pricing,
  PricingInput,
  PricingRule,
  PricingRuleInput,
  ProbeResult,
  Provider,
  ProviderInput,
  SyncResponse,
} from './modelRelay.types'

// ─── providers ────────────────────────────────────────────────────
export async function listProviders(filter: {
  status?: string
  protocol?: string
  q?: string
} = {}) {
  const r = await http.get<PageOf<Provider>>('/v1/admin/providers', { params: filter })
  return r.data
}

export async function getProvider(id: string) {
  const r = await http.get<Provider>(`/v1/admin/providers/${id}`)
  return r.data
}

export async function createProvider(body: ProviderInput) {
  const r = await http.post<Provider>('/v1/admin/providers', body)
  return r.data
}

export async function updateProvider(id: string, body: ProviderInput) {
  const r = await http.patch<Provider>(`/v1/admin/providers/${id}`, body)
  return r.data
}

export async function deleteProvider(id: string) {
  await http.delete(`/v1/admin/providers/${id}`)
}

// ─── credentials ──────────────────────────────────────────────────
export async function listCredentials(filter: {
  provider_id?: string
  status?: string
  q?: string
} = {}) {
  const r = await http.get<PageOf<CredentialSafe>>('/v1/admin/credentials', {
    params: filter,
  })
  return r.data
}

export async function getCredential(id: string) {
  const r = await http.get<CredentialSafe>(`/v1/admin/credentials/${id}`)
  return r.data
}

export async function createCredential(body: CredentialCreateRequest) {
  const r = await http.post<CredentialSafe>('/v1/admin/credentials', body)
  return r.data
}

export async function updateCredential(id: string, body: CredentialUpdateRequest) {
  const r = await http.patch<CredentialSafe>(`/v1/admin/credentials/${id}`, body)
  return r.data
}

export async function deleteCredential(id: string) {
  await http.delete(`/v1/admin/credentials/${id}`)
}

export async function testCredential(id: string, testModel?: string) {
  const r = await http.post<ProbeResult>(
    `/v1/admin/credentials/${id}/test`,
    testModel ? { test_model: testModel } : {},
  )
  return r.data
}

// ─── models ───────────────────────────────────────────────────────
/**
 * P4 follow-up F1: listModels 加 mode 过滤. 单值 / 逗号分隔多值都支持.
 *
 * 对应后端 ModelFilter.Mode (services/model-relay/internal/registry/models.go).
 * 例: { mode: 'image_generation' } 或 { mode: 'image_generation,video_generation' }.
 *
 * 段 4 admin Vue 重构后, AigcModelsTable 切到调本函数 + mode= 过滤,
 * 不再走 /v1/admin/aigc/* compat 层.
 */
export async function listModels(filter: {
  status?: string
  family?: string
  min_plan?: string
  q?: string
  /** P4 follow-up F1: 8 mode 之一,或逗号分隔多值. 空=全部 mode. */
  mode?: string
  /** F2.1: true 时返回多带 pricings: {[code]: Pricing}, 替代 N+1 query. */
  include_pricing?: boolean
  /** 后端 parsePaging: page>=1, 默认 1 (services/model-relay adminapi/models.go). */
  page?: number
  /** 后端 parsePaging: 1..200, 默认 50, cap 200. */
  page_size?: number
} = {}) {
  const r = await http.get<ModelsPage>('/v1/admin/models', {
    params: {
      ...filter,
      include_pricing: filter.include_pricing ? 'true' : undefined,
    },
  })
  return r.data
}

export async function getModel(id: string) {
  const r = await http.get<ModelDetail>(`/v1/admin/models/${id}`)
  return r.data
}

export async function createModel(body: ModelInput) {
  const r = await http.post<Model>('/v1/admin/models', body)
  return r.data
}

export async function updateModel(id: string, body: ModelInput) {
  const r = await http.patch<Model>(`/v1/admin/models/${id}`, body)
  return r.data
}

export async function deleteModel(id: string) {
  await http.delete(`/v1/admin/models/${id}`)
}

export async function bindModelGroups(id: string, groupIds: string[]) {
  const r = await http.post<{ model_id: string; groups: ModelGroup[] }>(
    `/v1/admin/models/${id}/bind-groups`,
    { group_ids: groupIds },
  )
  return r.data
}

export async function syncUpstream() {
  // Upstream sync may take a few seconds against basellm.github.io.
  // 60s timeout overrides the http instance's default 30s — first sync
  // after a process restart routinely takes 5s, with headroom for slow
  // networks.
  const r = await http.post<SyncResponse>(
    '/v1/admin/models/sync-upstream',
    {},
    { timeout: 60_000 },
  )
  return r.data
}

// ─── channels ─────────────────────────────────────────────────────
export async function listChannels(filter: {
  model_id?: string
  credential_id?: string
  status?: string
} = {}) {
  const r = await http.get<PageOf<Channel>>('/v1/admin/channels', { params: filter })
  return r.data
}

export async function getChannel(id: string) {
  const r = await http.get<Channel>(`/v1/admin/channels/${id}`)
  return r.data
}

export async function createChannel(body: ChannelInput) {
  const r = await http.post<Channel>('/v1/admin/channels', body)
  return r.data
}

export async function updateChannel(id: string, body: ChannelInput) {
  const r = await http.patch<Channel>(`/v1/admin/channels/${id}`, body)
  return r.data
}

export async function deleteChannel(id: string) {
  await http.delete(`/v1/admin/channels/${id}`)
}

export async function testChannel(id: string) {
  const r = await http.post<ProbeResult>(`/v1/admin/channels/${id}/test`, {})
  return r.data
}

// ─── pricing ──────────────────────────────────────────────────────
export async function getPricing(modelId: string) {
  const r = await http.get<Pricing>(`/v1/admin/pricing/${modelId}`)
  return r.data
}

export async function setPricing(modelId: string, body: PricingInput) {
  const r = await http.post<Pricing>(`/v1/admin/pricing/${modelId}`, body)
  return r.data
}

export async function getPricingHistory(modelId: string) {
  const r = await http.get<PageOf<Pricing>>(`/v1/admin/pricing/${modelId}/history`)
  return r.data
}

// ─── pricing_rules (F2.1) ─────────────────────────────────────────
// parameter strategy 用的多维乘数表 (by_duration / by_resolution etc.).
// append-only, 历史保留. POST 后后端会自动把 model.pricing_strategy
// 升级为 'parameter'.
export async function listPricingRules(modelId: string) {
  const r = await http.get<PageOf<PricingRule>>(
    `/v1/admin/models/${modelId}/pricing-rules`,
  )
  return r.data
}

export async function appendPricingRule(modelId: string, body: PricingRuleInput) {
  const r = await http.post<PricingRule>(
    `/v1/admin/models/${modelId}/pricing-rules`,
    body,
  )
  return r.data
}

// ─── fx-rates ─────────────────────────────────────────────────────
export async function listFxRates() {
  const r = await http.get<FxRatesEnvelope>('/v1/admin/fx-rates')
  return r.data
}

export async function setFxRate(body: FxRateUpsert) {
  const r = await http.put<FxRateUpsert>('/v1/admin/fx-rates', body)
  return r.data
}

// ─── model-groups ─────────────────────────────────────────────────
export async function listModelGroups() {
  const r = await http.get<PageOf<ModelGroup>>('/v1/admin/model-groups')
  return r.data
}
