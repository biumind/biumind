// Package registry holds the data types and CRUD layer for the
// model_relay schema (providers / credentials / models / channels /
// pricing / fx_rates / model_groups / model_group_bindings /
// user_group_memberships / usage_log / route_rules).
//
// This file (types.go) is the type skeleton — pure data shapes mirroring
// the SQL columns in services/model-relay/migrations/00001_model_relay_schema.sql.
// CRUD methods land in sibling files (providers.go, credentials.go, ...).
//
// Conventions:
//   - Struct names are singular (Provider, not Providers); the table name
//     is the plural form. Repository methods use the plural collection.
//   - Field names match SQL columns in PascalCase. JSON tags use snake_case
//     so admin API DTOs can reuse these structs directly.
//   - Cross-schema FK columns (user_id, owner_id) are uuid.UUID or string
//     but NOT FK-constrained at the DB layer (services boundary).
//   - Sensitive fields (Credential.Ciphertext / WrappedDEK / IV / WrapIV)
//     are *never* serialised to JSON; they have json:"-" tags.
//     The admin DTO uses CredentialSafe (see credentials.go) instead.
package registry

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ─── Enum-like string aliases ──────────────────────────────────────
// Mirrors the CHECK constraints in 00001_model_relay_schema.sql. We
// don't generate Go enums because Go's idiomatic pattern is typed
// strings + a Valid() helper if needed.

type ProviderProtocol string

const (
	ProtocolOpenAICompat ProviderProtocol = "openai_compat"
	ProtocolAnthropic    ProviderProtocol = "anthropic"
	// ProtocolDashScope — 阿里云 DashScope 私有协议. cosyvoice / paraformer /
	// wanx / qwen-image / wanx-video 等 AIGC 模型走 dashscope native API
	// (非 OpenAI-compat 形态). chat 模型仍可用 openai_compat. 见 00011 迁移.
	ProtocolDashScope ProviderProtocol = "dashscope"
	// ProtocolVolcEngine — 火山引擎豆包 Ark AIGC 协议 (段3.6).
	// Seedream 文生图 (同步 /images/generations) + Seedance 文生视频
	// (异步 /contents/generations/tasks). Doubao chat 仍走 openai_compat.
	ProtocolVolcEngine ProviderProtocol = "volcengine"
)

type EntityStatus string

const (
	StatusActive       EntityStatus = "active"
	StatusDisabled     EntityStatus = "disabled"
	StatusDeprecated   EntityStatus = "deprecated"    // models only
	StatusInvalid      EntityStatus = "invalid"       // credentials only (auto-flagged)
	StatusAutoDisabled EntityStatus = "auto_disabled" // channels only
	StatusArchived     EntityStatus = "archived"      // model_groups only
)

// Plan mirrors identity/billing.Plan literal values. We keep the alias
// here rather than importing identity to preserve the
// model-relay-doesn't-depend-on-identity boundary documented in
// services/model-relay/internal/plan/plan.go:9-15.
type Plan string

const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
	PlanTeam Plan = "team"
)

type Currency string

const (
	CurrencyCNY Currency = "CNY"
	CurrencyUSD Currency = "USD"
)

type RoutingStrategy string

const (
	StrategyWeighted      RoutingStrategy = "weighted"
	StrategyLowestLatency RoutingStrategy = "lowest_latency" // P2
	StrategyLeastBusy     RoutingStrategy = "least_busy"     // P2
	StrategyLowestTPMRPM  RoutingStrategy = "lowest_tpm_rpm" // P2
	StrategyCostAware     RoutingStrategy = "cost_aware"     // P3
)

// ModelMode mirrors the CHECK constraint in
// 00006_multimodal_extension.sql:33-43. We keep it as plain string
// (not a typed alias) because Model.Mode is also string for Scan
// simplicity; this just gives callers a vocabulary for the 8 valid
// values and a validator.
const (
	ModeChat               = "chat"
	ModeEmbedding          = "embedding"
	ModeImageGeneration    = "image_generation"
	ModeVideoGeneration    = "video_generation"
	ModeDigitalHuman       = "digital_human"
	ModeAudioSpeech        = "audio_speech"
	ModeAudioTranscription = "audio_transcription"
	ModeHotparse           = "hotparse"
	// v0.3 新增 (migration 0009) — 详见
	// docs/BiuMind-Multimodal-Gateway-Design.md §6.1.
	ModeRerank    = "rerank"    // RAG 排序 (bge-reranker / cohere / jina)
	ModeResponses = "responses" // OpenAI Stateful Responses API
)

var validModes = map[string]struct{}{
	ModeChat: {}, ModeEmbedding: {}, ModeImageGeneration: {},
	ModeVideoGeneration: {}, ModeDigitalHuman: {}, ModeAudioSpeech: {},
	ModeAudioTranscription: {}, ModeHotparse: {},
	ModeRerank: {}, ModeResponses: {},
}

// IsValidMode reports whether s is one of the 8 mode literals accepted
// by the schema CHECK. Empty string is NOT valid here — callers that
// want "let DB DEFAULT take over" should pre-fill ModeChat themselves.
func IsValidMode(s string) bool {
	_, ok := validModes[s]
	return ok
}

type GroupOwnerType string

const (
	OwnerSystem GroupOwnerType = "system"
	OwnerOrg    GroupOwnerType = "org"  // P3
	OwnerUser   GroupOwnerType = "user" // P3
)

// DefaultGroupID is the fixed UUID of the system 'default' group seeded
// in 00003_seed.sql. The Resolver short-circuits group filtering when
// a user has no explicit memberships AND the model is bound here.
var DefaultGroupID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// ─── 1. Provider ────────────────────────────────────────────────────

type Provider struct {
	ID          uuid.UUID        `json:"id"`
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	Protocol    ProviderProtocol `json:"protocol"`
	Icon        string           `json:"icon"`
	Description string           `json:"description"`
	Status      EntityStatus     `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// ─── 2. Credential ──────────────────────────────────────────────────
// Internal-only struct with envelope encryption fields. The admin API
// must serve CredentialSafe (defined in credentials.go) instead.

type Credential struct {
	ID         uuid.UUID `json:"id"`
	ProviderID uuid.UUID `json:"provider_id"`
	Label      string    `json:"label"`

	// Envelope fields — never JSON-serialised. Layout matches
	// keys.EncryptedKey from services/model-relay/internal/keys/envelope.go.
	Ciphertext []byte `json:"-"`
	WrappedDEK []byte `json:"-"`
	IV         []byte `json:"-"`
	WrapIV     []byte `json:"-"`

	KeyPreview     string            `json:"key_preview"` // "sk-...abc1"
	BaseURL        string            `json:"base_url"`
	HeaderOverride map[string]string `json:"header_override"` // jsonb at SQL layer

	Status        EntityStatus `json:"status"`
	LastTestAt    *time.Time   `json:"last_test_at,omitempty"`
	LastTestError string       `json:"last_test_error"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ─── 3. Model ───────────────────────────────────────────────────────

// Capabilities mirrors litellm's supports_* fields. JSON-marshalled
// straight into the models.capabilities jsonb column.
type Capabilities struct {
	Vision   bool `json:"vision,omitempty"`
	Tools    bool `json:"tools,omitempty"`
	Thinking bool `json:"thinking,omitempty"`
	Cache    bool `json:"cache,omitempty"`
	JSONMode bool `json:"json_mode,omitempty"`
	Audio    bool `json:"audio,omitempty"`
}

// UpstreamRef describes where a synced model came from, so re-syncs
// can apply the field whitelist correctly. NULL for hand-created models.
type UpstreamRef struct {
	VendorName string    `json:"vendor_name"`
	NameRule   int       `json:"name_rule"`
	SourceETag string    `json:"source_etag"`
	SyncedAt   time.Time `json:"synced_at"`
}

type Model struct {
	ID            uuid.UUID    `json:"id"`
	Code          string       `json:"code"`
	DisplayName   string       `json:"display_name"`
	Family        string       `json:"family"`
	ContextWindow int          `json:"context_window"`
	MaxOutput     int          `json:"max_output"`
	Capabilities  Capabilities `json:"capabilities"`

	MinPlan         Plan            `json:"min_plan"`
	Status          EntityStatus    `json:"status"`
	SortOrder       int             `json:"sort_order"`
	UpstreamRef     *UpstreamRef    `json:"upstream_ref,omitempty"`
	ManualOverride  bool            `json:"manual_override"`
	RoutingStrategy RoutingStrategy `json:"routing_strategy"`

	// P4.S2.1 多模态扩展 — 8 mode 枚举 / 3 strategy / 3 dispatch.
	// 默认值 (chat/token/streaming) 由 schema CHECK + DEFAULT 兜底.
	Mode            string `json:"mode"`
	PricingStrategy string `json:"pricing_strategy"`
	DispatchMode    string `json:"dispatch_mode"`

	// v0.3 全模态网关 (migration 0010) — 主渠道全部失败时按数组顺序尝试
	// 备用 model code. 仅同 mode 内 fallback. 见
	// docs/BiuMind-Multimodal-Gateway-Design.md §4.3.
	FallbackModels []string `json:"fallback_models"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ─── 4. Channel ─────────────────────────────────────────────────────

// EndpointCapability mirrors the CHECK in 00010_fallback_and_endpoint_capability.sql.
const (
	EndpointStandard    = "standard"    // HTTP 同步/SSE 流, 走 modality adaptor (默认)
	EndpointRealtime    = "realtime"    // WS 双向 (M5 启用)
	EndpointPassthrough = "passthrough" // 不走 adaptor, 原样代理 (M6 启用)
)

type Channel struct {
	ID            uuid.UUID `json:"id"`
	ModelID       uuid.UUID `json:"model_id"`
	CredentialID  uuid.UUID `json:"credential_id"`
	UpstreamModel string    `json:"upstream_model"`

	Priority int `json:"priority"`
	Weight   int `json:"weight"`
	RPMLimit int `json:"rpm_limit"`
	TPMLimit int `json:"tpm_limit"`

	Status EntityStatus `json:"status"`

	FailureCount int        `json:"failure_count"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	LastError    string     `json:"last_error"`
	LastTestAt   *time.Time `json:"last_test_at,omitempty"`
	LatencyP50Ms int        `json:"latency_p50_ms"`

	// Strategy-specific knobs (cooldown_seconds / tags / ...). Reserved
	// for P2 strategies; MVP weighted ignores it.
	Extra map[string]any `json:"extra"`

	// v0.3 全模态网关 (migration 0010) — endpoint 能力声明,
	// "standard" / "realtime" / "passthrough", 见 EndpointXxx 常量.
	EndpointCapability string `json:"endpoint_capability"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ─── 5. Pricing ─────────────────────────────────────────────────────

type Pricing struct {
	ID      uuid.UUID `json:"id"`
	ModelID uuid.UUID `json:"model_id"`

	Currency Currency `json:"currency"`
	// Token-based (chat / embedding)
	InputPerMTok      float64 `json:"input_per_mtok"`
	OutputPerMTok     float64 `json:"output_per_mtok"`
	CacheWritePerMTok float64 `json:"cache_write_per_mtok"`
	CacheReadPerMTok  float64 `json:"cache_read_per_mtok"`

	// P4 段 2 多模态字段超集 (按 model.pricing_strategy='fixed' 选用).
	// nullable — *float64; nil 表示该字段不适用 / 未设.
	CostPerImage       *float64 `json:"cost_per_image,omitempty"`
	CostPerVideoSecond *float64 `json:"cost_per_video_second,omitempty"`
	CostPerAudioSecond *float64 `json:"cost_per_audio_second,omitempty"`
	CostPerCharacter   *float64 `json:"cost_per_character,omitempty"`
	// rerank: per_search_unit (Cohere 标准 1 unit = 1 query × ≤100 docs).
	// W4 SoT 整合时引入,跟其他多模态字段一样原币种存储.
	CostPerSearchUnit float64 `json:"cost_per_search_unit"`

	// 平台加成 + 最低 / 最高单次扣费. 迁自 billing.pricing_book (W4 整合 SoT).
	// MarkupRatio: 标价 = 成本 × MarkupRatio. 默认 3.0 (历史 seed 一致).
	// MinCharge: 单次最低扣费 millicents (防极小请求扣 0). 默认 0.
	// MaxChargePerRequest: 单次封顶 millicents (防恶意 prompt). nil = 不限.
	MarkupRatio         float64 `json:"markup_ratio"`
	MinCharge           int64   `json:"min_charge"`
	MaxChargePerRequest *int64  `json:"max_charge_per_request,omitempty"`

	EffectiveAt time.Time  `json:"effective_at"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// PricingRule — model_relay.pricing_rules 表一行 (parameter strategy 用).
type PricingRule struct {
	ID          uuid.UUID       `json:"id"`
	ModelID     uuid.UUID       `json:"model_id"`
	RuleJSON    json.RawMessage `json:"rule_jsonb"` // by_duration × by_resolution etc.
	EffectiveAt time.Time       `json:"effective_at"`
	CreatedBy   *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ─── 6. FxRate ──────────────────────────────────────────────────────

type FxRate struct {
	FromCurrency Currency   `json:"from_currency"`
	ToCurrency   Currency   `json:"to_currency"`
	Rate         float64    `json:"rate"`
	Source       string     `json:"source"` // "manual" | "cron"
	UpdatedAt    time.Time  `json:"updated_at"`
	UpdatedBy    *uuid.UUID `json:"updated_by,omitempty"`
}

// ─── 7. RouteRule (reserved, MVP unused) ────────────────────────────

type RouteRule struct {
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	MatchExpr map[string]any `json:"match_expr"`
	Action    map[string]any `json:"action"`
	Priority  int            `json:"priority"`
	Status    EntityStatus   `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ─── 8. ModelGroup ──────────────────────────────────────────────────

type ModelGroup struct {
	ID          uuid.UUID      `json:"id"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	OwnerType   GroupOwnerType `json:"owner_type"`
	OwnerID     string         `json:"owner_id"` // arbitrary string; uuid for org/user
	Description string         `json:"description"`
	Status      EntityStatus   `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ─── 9. ModelGroupBinding ───────────────────────────────────────────

type ModelGroupBinding struct {
	GroupID   uuid.UUID `json:"group_id"`
	ModelID   uuid.UUID `json:"model_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── 10. UserGroupMembership (reserved, MVP unused) ─────────────────

type UserGroupMembership struct {
	UserID    uuid.UUID `json:"user_id"`
	GroupID   uuid.UUID `json:"group_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ─── 11. UsageLog ───────────────────────────────────────────────────

type UsageStatus string

const (
	UsageOK          UsageStatus = "ok"
	UsageError       UsageStatus = "error"
	UsageRateLimited UsageStatus = "rate_limited"
	UsageCancelled   UsageStatus = "cancelled"
)

type UsageLog struct {
	ID            int64     `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	ModelID       uuid.UUID `json:"model_id"`
	ChannelID     uuid.UUID `json:"channel_id"`
	ModelCode     string    `json:"model_code"`
	UpstreamModel string    `json:"upstream_model"`
	UserPlan      Plan      `json:"user_plan"`

	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`

	CostOriginCurrency Currency `json:"cost_origin_currency"`
	CostOriginAmount   float64  `json:"cost_origin_amount"`
	CostSettleCurrency Currency `json:"cost_settle_currency"`
	CostSettleAmount   float64  `json:"cost_settle_amount"`
	FxRate             float64  `json:"fx_rate"`

	LatencyMs int `json:"latency_ms"`
	// CreditsCharged — 这次调用实际扣的标价积分 (settled). 0 = BYOK / 失败 /
	// 未配价. 真相账本仍是 identity.credit_logs; 此列是 model-relay 侧用量
	// dashboard 的本地视图 (见 requestState.settleCredits).
	CreditsCharged int64       `json:"credits_charged"`
	Status         UsageStatus `json:"status"`
	ErrorCode      string      `json:"error_code"`
	RequestID      string      `json:"request_id"`
	CreatedAt      time.Time   `json:"created_at"`
}

// ─── Admin DTOs ─────────────────────────────────────────────────────

// CredentialSafe is the Credential view safe to serialise to admin
// clients — the envelope fields are stripped so JSON encoders can't
// accidentally leak them. Always construct via NewCredentialSafe.
type CredentialSafe struct {
	ID             uuid.UUID         `json:"id"`
	ProviderID     uuid.UUID         `json:"provider_id"`
	Label          string            `json:"label"`
	KeyPreview     string            `json:"key_preview"`
	BaseURL        string            `json:"base_url"`
	HeaderOverride map[string]string `json:"header_override"`
	Status         EntityStatus      `json:"status"`
	LastTestAt     *time.Time        `json:"last_test_at,omitempty"`
	LastTestError  string            `json:"last_test_error"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// NewCredentialSafe scrubs envelope fields. Admin handlers MUST use
// this when returning credential data over HTTP.
func NewCredentialSafe(c *Credential) CredentialSafe {
	return CredentialSafe{
		ID:             c.ID,
		ProviderID:     c.ProviderID,
		Label:          c.Label,
		KeyPreview:     c.KeyPreview,
		BaseURL:        c.BaseURL,
		HeaderOverride: c.HeaderOverride,
		Status:         c.Status,
		LastTestAt:     c.LastTestAt,
		LastTestError:  c.LastTestError,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}
