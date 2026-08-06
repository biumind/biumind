// model_relay admin DTOs — kept in sync with
// services/model-relay/internal/registry/types.go.
// Source of truth for the wire shape: services/model-relay/internal/adminapi/openapi.yaml.

export type Plan = 'free' | 'pro' | 'team'
export type Currency = 'USD' | 'CNY'
export type ProviderProtocol = 'openai_compat' | 'anthropic' | 'dashscope' | 'volcengine'
export type EntityStatus =
  | 'active'
  | 'disabled'
  | 'deprecated'
  | 'invalid'
  | 'auto_disabled'
  | 'archived'
export type RoutingStrategy =
  | 'weighted'
  | 'lowest_latency'
  | 'least_busy'
  | 'lowest_tpm_rpm'
  | 'cost_aware'
export type GroupOwnerType = 'system' | 'org' | 'user'
export type UsageStatus = 'ok' | 'error' | 'rate_limited' | 'cancelled'

// ─── Provider ───────────────────────────────────────────────────────
export interface Provider {
  id: string
  code: string
  name: string
  protocol: ProviderProtocol
  icon: string
  description: string
  status: EntityStatus
  created_at: string
  updated_at: string
}

export interface ProviderInput {
  code: string
  name: string
  protocol: ProviderProtocol
  icon?: string
  description?: string
  status?: EntityStatus
}

// ─── Credential (always returned in Safe form — no plaintext) ───────
export interface CredentialSafe {
  id: string
  provider_id: string
  label: string
  key_preview: string // "sk-12...abcd"
  base_url: string
  header_override: Record<string, string>
  status: EntityStatus
  last_test_at?: string
  last_test_error: string
  created_at: string
  updated_at: string
}

export interface CredentialCreateRequest {
  provider_id: string
  label: string
  plaintext: string
  base_url?: string
  header_override?: Record<string, string>
  status?: EntityStatus
}

export interface CredentialUpdateRequest {
  label: string
  base_url?: string
  header_override?: Record<string, string>
  status?: EntityStatus
  /** non-empty triggers rotation. */
  plaintext?: string
}

// ─── Capabilities / Model ───────────────────────────────────────────
export interface Capabilities {
  vision?: boolean
  tools?: boolean
  thinking?: boolean
  cache?: boolean
  json_mode?: boolean
  audio?: boolean
}

export interface UpstreamRef {
  vendor_name: string
  name_rule: number
  source_etag: string
  synced_at: string
}

// P4 段 2 加: 8 mode 枚举。
// v0.3 M0.1 又加 rerank + responses (跟 services/model-relay/internal/registry
// /types.go ModeRerank / ModeResponses 对齐).
export type ModelMode =
  | 'chat'
  | 'embedding'
  | 'image_generation'
  | 'video_generation'
  | 'digital_human'
  | 'audio_speech'
  | 'audio_transcription'
  | 'hotparse'
  | 'rerank'
  | 'responses'

export type PricingStrategy = 'token' | 'parameter' | 'fixed'
export type DispatchMode = 'sync' | 'streaming' | 'async'

export interface Model {
  id: string
  code: string
  display_name: string
  family: string
  context_window: number
  max_output: number
  capabilities: Capabilities
  min_plan: Plan
  status: EntityStatus
  sort_order: number
  upstream_ref?: UpstreamRef | null
  manual_override: boolean
  routing_strategy: RoutingStrategy
  // P4 段 2 多模态扩展 (admin Vue 段 4 重构后才会真正消费这些字段)
  mode: ModelMode
  pricing_strategy: PricingStrategy
  dispatch_mode: DispatchMode
  created_at: string
  updated_at: string
}

export interface ModelInput {
  code: string
  display_name: string
  family?: string
  context_window?: number
  max_output?: number
  capabilities?: Capabilities
  min_plan?: Plan
  status?: EntityStatus
  sort_order?: number
  routing_strategy?: RoutingStrategy
  manual_override?: boolean
  // v0.3 全模态网关字段 — 后端 registry.ModelInput 一直支持, 之前 admin
  // 没暴露入参. M2.5 加「手动添加模型」时显式选 mode.
  mode?: ModelMode
  pricing_strategy?: PricingStrategy
  dispatch_mode?: DispatchMode
  fallback_models?: string[]
}

export interface ModelDetail {
  model: Model
  groups: ModelGroup[]
}

// ─── Channel ────────────────────────────────────────────────────────
export interface Channel {
  id: string
  model_id: string
  credential_id: string
  upstream_model: string
  priority: number
  weight: number
  rpm_limit: number
  tpm_limit: number
  status: EntityStatus
  failure_count: number
  last_error_at?: string
  last_error: string
  last_test_at?: string
  latency_p50_ms: number
  extra: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface ChannelInput {
  model_id: string
  credential_id: string
  upstream_model: string
  priority?: number
  weight?: number
  rpm_limit?: number
  tpm_limit?: number
  status?: EntityStatus
  extra?: Record<string, unknown>
}

// ─── Pricing ────────────────────────────────────────────────────────
export interface Pricing {
  id?: string // optional: GET /pricing/{model_id} 在无价格时返回空对象
  model_id: string
  currency: Currency
  input_per_mtok: number
  output_per_mtok: number
  cache_write_per_mtok: number
  cache_read_per_mtok: number
  // P4 段 2 多模态扩展 (nullable, fixed 策略下至少一个非空)
  cost_per_image?: number | null
  cost_per_video_second?: number | null
  cost_per_audio_second?: number | null
  cost_per_character?: number | null
  effective_at?: string
  created_by?: string
  created_at?: string
}

export interface PricingInput {
  currency: Currency
  input_per_mtok?: number
  output_per_mtok?: number
  cache_write_per_mtok?: number
  cache_read_per_mtok?: number
  cost_per_image?: number | null
  cost_per_video_second?: number | null
  cost_per_audio_second?: number | null
  cost_per_character?: number | null
}

// P4 段 4 / F2.1 — pricing_rules 多维乘数 (parameter strategy 用)
export interface PricingRule {
  id: string
  model_id: string
  rule_jsonb: Record<string, unknown>
  effective_at: string
  created_by?: string | null
  created_at: string
}

export interface PricingRuleInput {
  rule_jsonb: Record<string, unknown>
}

// ─── FxRate ─────────────────────────────────────────────────────────
export interface FxRate {
  from_currency: Currency
  to_currency: Currency
  rate: number
  source: 'manual' | 'cron'
  updated_at: string
  updated_by?: string
}

export interface FxRatesEnvelope {
  items: FxRate[]
  total: number
  stalest?: FxRate
  stalest_age_seconds?: number
}

export interface FxRateUpsert {
  from_currency: Currency
  to_currency: Currency
  rate: number
  source?: 'manual' | 'cron'
}

// ─── ModelGroup ─────────────────────────────────────────────────────
export interface ModelGroup {
  id: string
  code: string
  name: string
  owner_type: GroupOwnerType
  owner_id: string
  description: string
  status: 'active' | 'archived'
  created_at: string
  updated_at: string
}

// ─── Probe / Sync ───────────────────────────────────────────────────
export interface ProbeResult {
  ok: boolean
  latency_ms: number
  error_code?: string
  error?: string
  tokens?: number
  status_code?: number
}

export interface SyncResponse {
  added: number
  updated: number
  skipped: number
  total: number
  not_modified: boolean
  synced_at: string
  etag?: string
}

// Generic list envelope used by every collection endpoint.
export interface PageOf<T> {
  items: T[]
  total: number
}

// P4 段 4 / F2.1 — listModels?include_pricing=true 的扩展返回:
// 多一个 pricings: { [model_code]: Pricing }, 一次 SQL 批量拉.
export interface ModelsPage extends PageOf<Model> {
  pricings?: Record<string, Pricing>
}
