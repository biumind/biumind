// estimate.go — POST /v1/chat/estimate.
//
// 客户端 composer 在发送前调一次, 拿到「约 N-M 积分」chip.
//
// Body:  { "model": "...", "messages": [...], "max_tokens": 4096 }
// 200:   { "min_credits": int, "max_credits": int, "byok_active": bool,
//          "provider": "anthropic", "model": "claude-..." }
//
// 计算口径:
//   - 用 messages_billing.estimatePromptTokensFromCanon 估 prompt token 数
//   - max_completion 用 body.max_tokens (空 → 4096 默认)
//   - 调 billing.LookupPrice 拿 pricing
//   - billing.PricingEntry.EstimateChatRange(prompt, max_comp) → (min_list, max_list) 毫分
//   - millicents → 积分: ceil(mc / 1000)
//
// BYOK 命中: byok_active=true, min_credits=max_credits=0.
//
// 不存在的 pricing: 返 200 + min=max=0 + warning (老模型不计费).

package api

import (
	"encoding/json"
	"net/http"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/byok"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// EstimateHandler 单独成一个 handler, 不挂 MessagesHandler 的 quota 中间件
// (estimate 是免费查询, 不算 RPM).
type EstimateHandler struct {
	// CredsResolver 复用 messages 同一个 — 只为了拿 providerName (model → provider).
	// 实际 creds 在 estimate 路径不消费.
	CredsResolver func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error)

	// Billing / BYOK 注入. 与 MessagesHandler 共享.
	Billing *MessagesHandler // 复用上面的 Billing/BYOK 字段, 避免重复配
}

// estimateReq — chat completion 的子集, 只关心 model + messages + max_tokens.
type estimateReq struct {
	Model     string             `json:"model"`
	Messages  []provider.Message `json:"messages"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	System    json.RawMessage    `json:"system,omitempty"`
}

type estimateResp struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BYOKActive  bool   `json:"byok_active"`
	MinCredits  int64  `json:"min_credits"`
	MaxCredits  int64  `json:"max_credits"`
	// 警告 (e.g. pricing not found)
	Warning string `json:"warning,omitempty"`
}

func (h *EstimateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Billing == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "estimate_unavailable", "")
		return
	}

	var body estimateReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.Model == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_model", "")
		return
	}

	// 1. 解析 provider (借 CredsResolver 但不真用 creds)
	providerName, _, _, err := h.CredsResolver(r, body.Model)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "model_unknown", err.Error())
		return
	}

	resp := estimateResp{Provider: providerName, Model: body.Model}

	// 2. BYOK 优先: 命中即 0 积分
	var userID string
	if c, ok := bauth.ClaimsFrom(r.Context()); ok {
		userID = c.UserID
	}
	if h.Billing.BYOK != nil && userID != "" {
		if k, err := h.Billing.BYOK.Get(r.Context(), userID, providerName); err == nil && k != nil {
			resp.BYOKActive = true
			writeJSON(w, http.StatusOK, resp)
			return
		} else if err != nil && err != byok.ErrKeyNotFound && h.Billing.Logger != nil {
			h.Billing.Logger.Warn("estimate byok lookup", "err", err)
		}
	}

	// 3. 平台路径: 查价 + 估区间
	if h.Billing.Billing == nil {
		// 计费没接 → 当作免费
		resp.Warning = "billing not wired"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	canon := &provider.Request{
		Model:    body.Model,
		Messages: body.Messages,
		System:   body.System,
	}
	promptTok := estimatePromptTokensFromCanon(canon)
	maxComp := int64(body.MaxTokens)
	if maxComp <= 0 {
		maxComp = 4096
	}

	entry, perr := h.Billing.Billing.LookupPrice(r.Context(), "chat", body.Model)
	if perr != nil {
		resp.Warning = "pricing not found"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	minList, maxList := entry.EstimateChatRange(promptTok, maxComp)
	// millicents → 积分: ceil(mc / 1000), 防 0 显示
	resp.MinCredits = millicentsToCredits(minList)
	resp.MaxCredits = millicentsToCredits(maxList)
	writeJSON(w, http.StatusOK, resp)
}

// millicentsToCredits — ceil(mc / 1000). 1 积分 = 1000 毫分.
func millicentsToCredits(mc int64) int64 {
	if mc <= 0 {
		return 0
	}
	return (mc + 999) / 1000
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
