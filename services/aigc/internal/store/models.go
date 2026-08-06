package store

// models.go — Phase 4 段 3.6 cutover.
//
// 历史: 此文件曾承担 aigc.providers / aigc.models 的 CRUD. 段 3.6 后
// AIGC 字典统一在 model_relay.* schema (model_relay.models 加 mode 字段
// 区分 chat / image_generation / video_generation / digital_human /
// hotparse). aigc.providers / aigc.models 表已 DROP.
//
// 本文件保留是因为 services/aigc/internal/api/generations.go 仍然走
// store.GetModel(code) 检查 model 是否存在 + 拿 type/provider_code, 是
// 客户端 POST /v1/generations 路径的关键. 所以保留 GetModel / ListModels
// 函数签名不变, 但 SELECT 改成查 model_relay.models, mode → type 反向
// 映射 (image_generation → image 等).
//
// CRUD (Upsert / Delete) 已删 — 写入只在 model-relay 的 /v1/admin/models
// 单源, 不再有 aigc 自己的写路径.

import (
	"context"
	"strings"
	"time"
)

// UpsertModelArgs / UpsertProviderArgs 保留是为了**测试 seed**.
// 段 3.6 后 aigc 服务自身不再有写字典的代码路径 (adminapi 已删, mirror
// 已删); 但测试用例需要 seed 数据 (orchestrator_test / api_test /
// store_test 等), 所以保留 helper. 内部直接写 model_relay.*.

// UpsertModelArgs 给测试 seed model_relay.models 用.
type UpsertModelArgs struct {
	Code         string
	Type         string // image | video | digital_human | hotparse
	DisplayName  string
	ProviderCode string
	PriceCredits int64
	PricingRule  []byte
	Config       []byte
	Enabled      bool
	SortOrder    int
}

// UpsertProviderArgs 给测试 seed model_relay.providers 用.
// 段 3.6 后 model_relay.providers 不再有 base_url / credentials_ref 字段
// (这些在 model_relay.credentials 一行), 所以这里这些字段被忽略.
type UpsertProviderArgs struct {
	Code           string
	Name           string
	BaseURL        string // 忽略 (test compat)
	CredentialsRef string // 忽略
	Priority       int    // 忽略
	Enabled        bool
	Config         []byte // 忽略 (model_relay.providers 无此字段)
}

// Model 对应 (现 model_relay.models 反向映射的) 一行字典. 字段集是
// generations.go / api/models.go projectModels 实际用到的子集.
type Model struct {
	Code         string
	Type         string // image | video | digital_human | hotparse (从 mode 反推)
	DisplayName  string
	ProviderCode string // 取自 model_relay.models.family
	PriceCredits int64  // 从 model_relay.pricing 最近一条 cost_per_image / per_video_second 兜底取
	PricingRule  []byte // 从 model_relay.pricing_rules.rule_jsonb (latest) 取
	Config       []byte // 从 model_relay.models.capabilities (透传 jsonb)
	Enabled      bool   // model_relay.models.status = 'active'
	SortOrder    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Provider 保留作 type, 段 3.6 后 aigc 不再有 provider 字典 — 仅 store
// 历史 import 不破坏. 实际 provider 信息用 services/aigc/internal/credentials
// 的 NewModelRelayStore 直接查 model_relay.providers.
type Provider struct {
	Code           string
	Name           string
	BaseURL        string
	CredentialsRef string
	Priority       int
	Enabled        bool
	Config         []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// modeToAigcType 把 model_relay.models.mode 反推到 aigc 风格的 type 字面量.
// 不在 4 选 1 内 (chat / embedding / audio_*) 返回空 — 调用方应过滤掉.
func modeToAigcType(mode string) string {
	switch mode {
	case "image_generation":
		return "image"
	case "video_generation":
		return "video"
	case "digital_human":
		return "digital_human"
	case "hotparse":
		return "hotparse"
	}
	return ""
}

// aigcTypeToMode 把 aigc 风格的 type 翻译到 model_relay.models.mode.
// 反向于 modeToAigcType. 给 UpsertModel 用.
func aigcTypeToMode(typ string) string {
	switch typ {
	case "image":
		return "image_generation"
	case "video":
		return "video_generation"
	case "digital_human":
		return "digital_human"
	case "hotparse":
		return "hotparse"
	}
	return "image_generation"
}

// aigcTypeToModes 反向, 给 ListModels 的 type 过滤用.
func aigcTypeToModes(typ string) []string {
	switch typ {
	case "image":
		return []string{"image_generation"}
	case "video":
		return []string{"video_generation"}
	case "digital_human":
		return []string{"digital_human"}
	case "hotparse":
		return []string{"hotparse"}
	}
	return []string{
		"image_generation", "video_generation",
		"digital_human", "hotparse",
	}
}

// GetModel 按 code 取单模型. 历史调用方: api/generations.go 校验模型存在.
func (s *Store) GetModel(ctx context.Context, code string) (*Model, error) {
	const q = `
		SELECT m.code,
		       m.mode,
		       m.display_name,
		       COALESCE(m.family, ''),
		       COALESCE((
		           SELECT GREATEST(
		               COALESCE(p.cost_per_image, 0),
		               COALESCE(p.cost_per_video_second, 0),
		               COALESCE(p.cost_per_audio_second, 0),
		               COALESCE(p.cost_per_character, 0)
		           )::bigint
		           FROM model_relay.pricing p
		           WHERE p.model_id = m.id
		           ORDER BY p.effective_at DESC LIMIT 1
		       ), 0) AS price_credits,
		       (SELECT pr.rule_jsonb
		        FROM model_relay.pricing_rules pr
		        WHERE pr.model_id = m.id
		        ORDER BY pr.effective_at DESC LIMIT 1) AS pricing_rule,
		       COALESCE(m.capabilities, '{}'::jsonb) AS config,
		       (m.status = 'active') AS enabled,
		       m.sort_order, m.created_at, m.updated_at,
		       m.mode
		FROM model_relay.models m
		WHERE m.code = $1
	`
	row := s.pool.QueryRow(ctx, q, code)
	m := &Model{}
	var mode string
	err := row.Scan(
		&m.Code, &mode, &m.DisplayName, &m.ProviderCode,
		&m.PriceCredits, &m.PricingRule, &m.Config, &m.Enabled,
		&m.SortOrder, &m.CreatedAt, &m.UpdatedAt, &mode,
	)
	if err != nil {
		return rowOrErr[Model](nil, err)
	}
	m.Type = modeToAigcType(mode)
	if m.Type == "" {
		// 不属于 AIGC 的 modality (chat / embedding / audio_*) 视为 not found
		return nil, ErrNotFound
	}
	return m, nil
}

// UpsertProvider — 测试 helper. 写 model_relay.providers (protocol 默认
// 'openai_compat', 与 mirror.go 同款映射). 段 3.6 后生产路径不调.
func (s *Store) UpsertProvider(ctx context.Context, a UpsertProviderArgs) error {
	const q = `
		INSERT INTO model_relay.providers
			(code, name, protocol, icon, description, status)
		VALUES ($1, $2, 'openai_compat', '', '', $3)
		ON CONFLICT (code) DO UPDATE SET
			name       = EXCLUDED.name,
			status     = EXCLUDED.status,
			updated_at = now()
	`
	status := "disabled"
	if a.Enabled {
		status = "active"
	}
	_, err := s.pool.Exec(ctx, q, a.Code, a.Name, status)
	return err
}

// UpsertModel — 测试 helper. 写 model_relay.models, type → mode 映射.
// 价格写入 model_relay.pricing 一行 (cost_per_image, 单位与原 PriceCredits
// 同口径). pricing_rule 写入 model_relay.pricing_rules.
func (s *Store) UpsertModel(ctx context.Context, a UpsertModelArgs) error {
	mode := aigcTypeToMode(a.Type)
	strategy := "fixed"
	if len(a.PricingRule) > 0 && string(a.PricingRule) != "null" {
		strategy = "parameter"
	}
	cfg := a.Config
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	status := "disabled"
	if a.Enabled {
		status = "active"
	}

	const upsertModel = `
		INSERT INTO model_relay.models
			(code, display_name, family, mode, pricing_strategy, dispatch_mode,
			 context_window, max_output, capabilities, status, sort_order, manual_override)
		VALUES ($1, $2, $3, $4, $5, 'async', 0, 0, $6::jsonb, $7, $8, false)
		ON CONFLICT (code) DO UPDATE SET
			display_name     = EXCLUDED.display_name,
			family           = EXCLUDED.family,
			mode             = EXCLUDED.mode,
			pricing_strategy = EXCLUDED.pricing_strategy,
			capabilities     = EXCLUDED.capabilities,
			status           = EXCLUDED.status,
			sort_order       = EXCLUDED.sort_order,
			updated_at       = now()
		WHERE model_relay.models.manual_override = false
		RETURNING id
	`
	var modelID string
	if err := s.pool.QueryRow(ctx, upsertModel,
		a.Code, a.DisplayName, a.ProviderCode, mode, strategy,
		cfg, status, a.SortOrder,
	).Scan(&modelID); err != nil {
		return err
	}

	if a.PriceCredits > 0 {
		// 测试默认写 cost_per_image (image / video 都用此字段, 由 jobs handler
		// fetchBasePrice 兜底逻辑承接).
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO model_relay.pricing (model_id, currency, cost_per_image)
			VALUES ($1, 'CNY', $2)
		`, modelID, a.PriceCredits)
	}
	if strategy == "parameter" {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO model_relay.pricing_rules (model_id, rule_jsonb)
			VALUES ($1, $2::jsonb)
		`, modelID, a.PricingRule)
	}
	return nil
}

// ListModels 按 type 过滤 (空 = 全部 AIGC modality), sort_order 升序.
// includeDisabled 默认 false (前端 GET /v1/models 只看 active).
func (s *Store) ListModels(ctx context.Context, typ string, includeDisabled bool) ([]*Model, error) {
	modes := aigcTypeToModes(typ)
	q := strings.Builder{}
	q.WriteString(`
		SELECT m.code, m.mode, m.display_name, COALESCE(m.family, ''),
		       COALESCE((
		           SELECT GREATEST(
		               COALESCE(p.cost_per_image, 0),
		               COALESCE(p.cost_per_video_second, 0),
		               COALESCE(p.cost_per_audio_second, 0),
		               COALESCE(p.cost_per_character, 0)
		           )::bigint
		           FROM model_relay.pricing p
		           WHERE p.model_id = m.id
		           ORDER BY p.effective_at DESC LIMIT 1
		       ), 0) AS price_credits,
		       (SELECT pr.rule_jsonb
		        FROM model_relay.pricing_rules pr
		        WHERE pr.model_id = m.id
		        ORDER BY pr.effective_at DESC LIMIT 1) AS pricing_rule,
		       COALESCE(m.capabilities, '{}'::jsonb),
		       (m.status = 'active'),
		       m.sort_order, m.created_at, m.updated_at
		FROM model_relay.models m
		WHERE m.mode = ANY($1)`)
	if !includeDisabled {
		q.WriteString(` AND m.status = 'active'`)
	}
	q.WriteString(` ORDER BY m.sort_order, m.code`)

	rows, err := s.pool.Query(ctx, q.String(), modes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Model
	for rows.Next() {
		m := &Model{}
		var mode string
		if err := rows.Scan(
			&m.Code, &mode, &m.DisplayName, &m.ProviderCode,
			&m.PriceCredits, &m.PricingRule, &m.Config, &m.Enabled,
			&m.SortOrder, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.Type = modeToAigcType(mode)
		if m.Type == "" {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
