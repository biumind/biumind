// messages_billing.go — chat completion 的计费 / BYOK 预后置处理.
//
// 流程:
//   1. ServeHTTP 入口 → preflightBilling 决定 (是 BYOK / 走 Hold / 都跳过)
//   2. 把 *requestState 放到 ctx
//   3. fireComplete 内部把 (usage, success, errCode) 写入 state
//   4. defer finalizeBilling 根据 state 调 Settle / Release / TouchUsed / IncrementFailure
//
// 设计: docs/BiuMind-Billing-Redesign.md §5.2 / §5.4

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/billing"
	mrbyok "github.com/biumind/biumind/services/model-relay/internal/byok"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// requestState — 单次请求的计费状态. 通过 ctx 在 fireComplete 与 defer 之间传递.
type requestState struct {
	UserID       string
	ProviderName string
	ModelName    string

	// BYOK 路径
	IsBYOK bool

	// 平台 hold/settle 路径
	HoldID  string
	Pricing *billing.PricingEntry

	// fireComplete 写入: 最终结果
	Usage   provider.Usage
	Success bool
	ErrCode string
}

// settleCredits 是这次请求扣的标价积分: BYOK / 未 hold / 失败 / 未配价 → 0;
// 否则按最终 usage 走 CalculateChat. 唯一公式落点 —— finalizeBilling 用它做
// Settle 金额、fireComplete 用它填 usage_log.credits_charged, 两处永不漂移.
// (settle 网络失败→release 的极少数情形会让 usage_log 略高报, 接受: 真相账本
//
//	始终是 identity.credit_logs, usage_log 的 credits 是"这次调用的标价"视角.)
func (st *requestState) settleCredits() int64 {
	if st == nil || st.IsBYOK || st.HoldID == "" || !st.Success || st.Pricing == nil {
		return 0
	}
	return st.Pricing.CalculateChat(
		int64(st.Usage.PromptTokens),
		int64(st.Usage.CompletionTokens),
		int64(st.Usage.CacheReadTokens),
		int64(st.Usage.CacheWriteTokens),
	)
}

type stateCtxKey struct{}

func ctxWithState(ctx context.Context, st *requestState) context.Context {
	return context.WithValue(ctx, stateCtxKey{}, st)
}

// byokMatchedKey — P1: CredsResolver 在 catalog 失败时命中 identity BYOK
// match, 标记此 ctx 让 preflightBilling 跳过 Get/Hold (creds 已由 fallback 设).
type byokMatchedKey struct{}

func ctxWithBYOKMatched(ctx context.Context) context.Context {
	return context.WithValue(ctx, byokMatchedKey{}, true)
}

func byokMatchedFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(byokMatchedKey{}).(bool)
	return v
}

// byokAdaptorName — identity BYOK 的 protocol → Registry.GetChat 的 adaptor
// name. openai_compat/google/dashscope/volcengine chat 都走 openai 兼容
// adaptor; anthropic 走 anthropic adaptor.
func byokAdaptorName(protocol string) string {
	if protocol == "anthropic" {
		return "anthropic"
	}
	return "openai"
}

func stateFromCtx(ctx context.Context) *requestState {
	if v, ok := ctx.Value(stateCtxKey{}).(*requestState); ok {
		return v
	}
	return nil
}

// preflightBilling — 在 chat 流之前决定 BYOK / Hold. 返回是否应继续 (false = 已写
// 402 / 错误响应, 调用方直接 return).
//
// 调用顺序: 在 CredsResolver 成功之后 (此时已知 providerName), 但在请求上游之前.
//
// BYOK 命中时 *creds 会被替换 (覆盖 APIKey + 可能的 BaseURL).
//
// promptTok / maxCompletionTok 由调用方按 canon 给, 用于估 hold 上限.
func (h *MessagesHandler) preflightBilling(
	w http.ResponseWriter,
	r *http.Request,
	creds *provider.Credentials,
	modelName, providerName string,
	promptTok, maxCompletionTok int64,
) (*requestState, bool) {
	st := &requestState{ModelName: modelName, ProviderName: providerName}
	if c, ok := bauth.ClaimsFrom(r.Context()); ok {
		st.UserID = c.UserID
	}

	// P1: CredsResolver BYOK fallback 命中 (catalog 失败 → identity match) →
	// IsBYOK, 不再 Get (creds 已设), 跳过 Hold (用户 key 不扣平台积分).
	if byokMatchedFromCtx(r.Context()) {
		st.IsBYOK = true
		return st, true
	}

	// 1. BYOK 优先 — 命中即跳过 Hold
	if h.BYOK != nil && st.UserID != "" {
		if k, err := h.BYOK.Get(r.Context(), st.UserID, providerName); err == nil && k != nil {
			st.IsBYOK = true
			creds.APIKey = k.APIKey
			// config_json 解析 base_url / 其他覆盖留 v2 (Azure 等)
		} else if err != nil && !errors.Is(err, mrbyok.ErrKeyNotFound) {
			// 网络抖动 — 不阻断, 走平台路径; 日志后续观察
			if h.Logger != nil {
				h.Logger.Warn("byok lookup failed (falling back to platform)",
					"err", err, "user", st.UserID, "provider", providerName)
			}
		}
	}

	// 2. 平台路径 → 估价 + Hold
	if !st.IsBYOK && h.Billing != nil && st.UserID != "" {
		entry, perr := h.Billing.LookupPrice(r.Context(), "chat", modelName)
		if perr != nil {
			// pricing 不存在 — 老模型 / 未配价格. 不阻断, 留旧路径 (relay 不计费)
			if h.Logger != nil {
				h.Logger.Warn("pricing not found; chat will not be billed",
					"err", perr, "model", modelName)
			}
			return st, true
		}
		st.Pricing = entry

		if maxCompletionTok <= 0 {
			maxCompletionTok = 4096
		}
		_, maxCost := entry.EstimateChatRange(promptTok, maxCompletionTok)
		if maxCost <= 0 {
			return st, true
		}

		hold, herr := h.Billing.Hold(r.Context(), billing.HoldArgs{
			UserID:       st.UserID,
			MaxAmount:    maxCost,
			RefType:      "chat_message",
			RefID:        r.Header.Get("X-Request-Id"),
			ModelCode:    modelName, // W3-7: dashboard 按模型分布用
			ProviderCode: providerName,
		})
		if errors.Is(herr, billing.ErrInsufficient) {
			writeJSONErr(w, http.StatusPaymentRequired, "insufficient_credits",
				"chat 余额不足, 请充值")
			return nil, false
		}
		if herr != nil {
			// hold 网络异常 — 不阻断 chat (优先用户体验, 留 audit 警告)
			if h.Logger != nil {
				h.Logger.Warn("hold failed; chat will not be billed",
					"err", herr, "user", st.UserID, "model", modelName)
			}
			return st, true
		}
		st.HoldID = hold.ID
	}
	return st, true
}

// finalizeBilling — defer 调用. 根据 state 决定 Settle / Release / TouchUsed.
// 用 detached ctx (5s 超时) 避免请求 ctx 被 cancel 时漏 settle.
//
// 可观察性:每条 chat 请求的扣费决策(BYOK / Settled / Released / Skipped)
// 必打一条 INFO 日志,运维 + 排查"为啥不扣积分"时直接 grep "billing decision".
// 之前成功 settle 路径完全无声,debug 时只能盲目猜.
func (h *MessagesHandler) finalizeBilling(st *requestState) {
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// BYOK 路径
	if st.IsBYOK {
		if h.BYOK == nil {
			h.logBilling("byok_skip_no_client", st, 0)
			return
		}
		if st.Success {
			_ = h.BYOK.TouchUsed(ctx, st.UserID, st.ProviderName)
			h.logBilling("byok_success", st, 0)
			return
		}
		// 失败: 仅 401/403 类的认证失败才计入 (其它如超时不算 key 问题)
		if isUpstreamAuthFail(st.ErrCode) {
			_, _ = h.BYOK.IncrementFailure(ctx, st.UserID, st.ProviderName)
			h.logBilling("byok_auth_fail", st, 0)
		} else {
			h.logBilling("byok_upstream_fail", st, 0)
		}
		return
	}

	// 平台路径 — 解释为啥没 hold:
	//   - h.Billing == nil           → relay 没接 billing client (启动配置)
	//   - st.HoldID == ""            → preflightBilling 跳过了 hold:
	//       * st.UserID == ""         → JWT 没 user_id (service-to-service 调用)
	//       * h.Billing == nil        → 同上
	//       * pricing not found       → 没配价
	//       * hold network error      → identity 短暂不可用,Hold 失败 silent
	//   每种都是"不扣"的合法原因,这里日志区分,前端能解释为什么余额没动.
	if h.Billing == nil {
		h.logBilling("skip_no_billing_client", st, 0)
		return
	}
	if st.UserID == "" {
		h.logBilling("skip_no_user_id", st, 0)
		return
	}
	if st.HoldID == "" {
		// preflight 没成功 hold (pricing 缺失 / hold failed). 已经在 preflight
		// 日志过原因. 这里再补一行 finalize 视角让 grep "billing decision" 全
		h.logBilling("skip_no_hold", st, 0)
		return
	}
	if !st.Success || st.Pricing == nil {
		_ = h.Billing.Release(ctx, st.HoldID)
		h.logBilling("released_on_failure", st, 0)
		return
	}
	// 真扣 actual_amount = 实际 token 成本 list 价 (与 usage_log 同源)
	actual := st.settleCredits()
	if err := h.Billing.Settle(ctx, st.HoldID, actual, "chat-completion"); err != nil {
		// settle 失败 — release 兜底 (失败重试 / 报警在 v2)
		if h.Logger != nil {
			h.Logger.Error("settle failed, releasing", "err", err, "hold", st.HoldID,
				"actual_millicents", actual)
		}
		_ = h.Billing.Release(ctx, st.HoldID)
		h.logBilling("settle_failed_released", st, actual)
		return
	}
	h.logBilling("settled", st, actual)
}

// logBilling 把每条 chat 请求的计费决策落 INFO 日志. 永远输出一行,让
// grep "billing decision" 能列出全部. decision 取值:
//
//	settled / released_on_failure / settle_failed_released
//	byok_success / byok_auth_fail / byok_upstream_fail / byok_skip_no_client
//	skip_no_billing_client / skip_no_user_id / skip_no_hold
func (h *MessagesHandler) logBilling(decision string, st *requestState, actualMillicents int64) {
	if h.Logger == nil {
		return
	}
	h.Logger.Info("billing decision",
		"decision", decision,
		"user_id", st.UserID,
		"model", st.ModelName,
		"provider", st.ProviderName,
		"hold_id", st.HoldID,
		"is_byok", st.IsBYOK,
		"success", st.Success,
		"err_code", st.ErrCode,
		"prompt_tokens", st.Usage.PromptTokens,
		"completion_tokens", st.Usage.CompletionTokens,
		"actual_millicents", actualMillicents,
	)
}

// isUpstreamAuthFail — 判断是否 BYOK key 无效导致的失败.
// 现有 errCode 字符串集合是 messages.go 的稳定分类.
func isUpstreamAuthFail(errCode string) bool {
	// upstream_status 包含 401/403; 简单通过 errCode 名匹配, 不解析具体码
	// (主目的: 防止网络 5xx 误标 BYOK key 为 invalid)
	return strings.HasPrefix(errCode, "upstream_status_401") ||
		strings.HasPrefix(errCode, "upstream_status_403")
}

// estimatePromptTokensFromCanon — 基于 canon.Messages 字节数 / 4 粗略估 prompt
// tokens. Message.Content 是 json.RawMessage 透传, 字节长度近似 token 上界
// (英文 ≈ 4 char/tok, 中文 1.5 char/tok, 选 4 偏保守).
//
// hold 是上限不要求精确; 真实成本以 settle 为准.
func estimatePromptTokensFromCanon(canon *provider.Request) int64 {
	if canon == nil {
		return 1024
	}
	var n int64
	n += int64(len(canon.System))
	for _, m := range canon.Messages {
		n += int64(len(m.Content))
	}
	if n <= 0 {
		return 1024
	}
	return n / 4
}
