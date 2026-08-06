// publicmodels.go — GET /v1/me/models  (公开读端点, 普通用户 JWT 即可)
//
// P6: 端点从 /v1/models 改名 /v1/me/models —— site nginx /v1/models 已被
// aigc 占用 (web/site/nginx.conf, aigc:7012), model-relay 同名端点只在
// docker 内网被 brain 调, client 直读会撞 aigc。改名与 /v1/me/usage 同 NS。
//
// 跟 /v1/admin/models 的区别:
//   - 不要求 models:read perm (admin endpoint 限 admin/support/viewer 等
//     5 个 role); 这里只走 authMiddleware 校验 JWT 真实即可。
//   - 字段精简: 只返 client picker 渲染需要的字段
//     {code, display_name, family, context_window, capabilities, mode,
//      min_plan, max_output, pricing}, 不暴露 channel / sort_order /
//     upstream_ref / routing_strategy / dispatch_mode / manual_override /
//     fallback_models / status 等 admin 内部信息。
//   - pricing 是 **markup 后实际计费单价** (用户看到 = 实际扣费), 不含
//     MarkupRatio/MinCharge/MaxCharge (内部加价/钳制不暴露)。
//   - 默认 status=active, 只列当前可用模型 — 用户视角"我能选哪些"。
//
// 调用方:
//   - client (apps/client relay_catalog_client): P6 起官方模型直读, 跳过
//     brain 一跳。miniapp 同。
//   - brain (services/brain/internal/chat/providers): P6 删 relay_client.go
//     后不再批量同步 official, 此端点主要服务 client。
//
// 透传 / 计费:此端点是免费查询,不消耗 quota,不限流,无 BYOK header 概念。

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/google/uuid"
)

// modelLister 是 PublicModelsHandler 对 ModelRepo 的最小依赖切面 — 接口
// 形态便于单测 fake。生产实现是 *registry.ModelRepo。
type modelLister interface {
	List(ctx context.Context, f registry.ModelFilter) ([]registry.Model, error)
}

// modelPricer 批量取每个 model 最近 pricing (markup 后价算原料)。
// nil → DTO 不带 pricing 字段 (picker 仍能用, 只是无价 chip)。
// 生产实现是 *registry.PricingRepo.BatchLatest。
type modelPricer interface {
	BatchLatest(ctx context.Context, modelIDs []uuid.UUID) (map[uuid.UUID]registry.Pricing, error)
}

// PublicModelsHandler 持有最小依赖 — 不引 admin Server 的全套
// (Store/Vault/Cache/...) 确保边界清晰。Pricer 可选 (nil 时 pricing 缺失)。
type PublicModelsHandler struct {
	Models modelLister
	Pricer modelPricer // 可选
	Logger *slog.Logger
}

// publicModelDTO 是返给用户的 sanitized model 视图。**字段固定,不要随
// 意加 admin 字段** — 加之前问自己: 这个字段对终端用户有意义吗? 是否
// 暴露供应商/计费等内部信息? 拿不准的字段都不该出现在这里。
type publicModelDTO struct {
	Code          string                `json:"code"`
	DisplayName   string                `json:"display_name"`
	Family        string                `json:"family"`
	ContextWindow int                   `json:"context_window"`
	Capabilities  registry.Capabilities `json:"capabilities"`
	// Mode (chat / embedding / rerank / audio_speech / audio_transcription /
	// image_generation / video_generation / ...)。客户端据此把模型分到对话
	// picker vs 创作/检索链路。
	Mode string `json:"mode"`
	// MinPlan: 用该模型所需的最低 plan (pro/team); free = 所有人可用,省略。
	MinPlan   string            `json:"min_plan,omitempty"`
	MaxOutput int               `json:"max_output,omitempty"`
	Pricing   *publicPricingDTO `json:"pricing,omitempty"`
}

// publicPricingDTO — markup 后实际计费单价 (per_mtok, 原币种)。用户看到 =
// 实际扣费单价。不含 MarkupRatio/MinCharge/MaxChargePerRequest (内部加价
// 倍数 + 单次钳制, 不暴露给终端用户)。
type publicPricingDTO struct {
	Currency      string  `json:"currency"`
	InputPerMTok  float64 `json:"input_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
}

// ServeHTTP 实现 http.Handler。仅 GET。
//
// Query 参数:
//   - status (可选): 默认 'active'; 显式传 'all' 时返所有 status
//     (包括 deprecated/disabled), 但默认行为只返 active。
//
// 不分页 — picker 列表常驻菜单,active 模型总数 << 200,一次返完简化客户端。
// 极端情况(>200) Repo.List 用默认 limit 截断 + warning log,picker 会显
// 示前 N 条。
func (h *PublicModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	if h.Models == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "models_unavailable", "")
		return
	}

	status := r.URL.Query().Get("status")
	filter := registry.ModelFilter{
		Limit:  publicModelsMaxLimit,
		Offset: 0,
	}
	if status != "all" {
		// 默认 / 显式 active 都走 active filter
		filter.Status = registry.StatusActive
	}

	items, err := h.Models.List(r.Context(), filter)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	// 批量取 pricing (可选)。失败只 warn, DTO.pricing 缺失不阻断 picker。
	prices := map[uuid.UUID]registry.Pricing{}
	if h.Pricer != nil && len(items) > 0 {
		ids := make([]uuid.UUID, 0, len(items))
		for _, m := range items {
			ids = append(ids, m.ID)
		}
		if p, perr := h.Pricer.BatchLatest(r.Context(), ids); perr == nil {
			prices = p
		} else if h.Logger != nil {
			h.Logger.Warn("public models: batch pricing failed, pricing chip omitted", "err", perr)
		}
	}

	out := make([]publicModelDTO, 0, len(items))
	for _, m := range items {
		dto := publicModelDTO{
			Code:          m.Code,
			DisplayName:   m.DisplayName,
			Family:        m.Family,
			ContextWindow: m.ContextWindow,
			Capabilities:  m.Capabilities,
			Mode:          m.Mode,
			MaxOutput:     m.MaxOutput,
		}
		// free = 所有人可用, 不显 min_plan (避免噪音); pro/team 显提示升级。
		if m.MinPlan != "" && m.MinPlan != registry.PlanFree {
			dto.MinPlan = string(m.MinPlan)
		}
		if p, ok := prices[m.ID]; ok {
			dto.Pricing = &publicPricingDTO{
				Currency:      string(p.Currency),
				InputPerMTok:  p.EffectiveInputPerMTok(),
				OutputPerMTok: p.EffectiveOutputPerMTok(),
			}
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// publicModelsMaxLimit 是单次返回的硬上限。registry.ModelRepo.List 的
// 内部默认更大,但对外端点截断防止 active model 数量异常大时压垮 client。
const publicModelsMaxLimit = 200
