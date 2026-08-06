// modality_billing.go — v0.3 M5 多模态 (embedding / rerank / audio_speech /
// image / video) 共用的 BYOK / Hold / Settle 链路.
//
// 跟 messages_billing.go (chat 专用) 同设计但解耦:
//   - chat 的 maxAmount 估算依赖 EstimateChatRange (input + max_completion);
//     新 4 modality 各自维度不同 (token / chars / units / count / seconds),
//     用 caller 算好的 maxAmount 直接传入, helper 不管业务细节
//   - chat 的 actual_amount 在 finalize 通过 ctx 拿 usage 二次计算;
//     新 4 modality 由 caller 在 finalize 前算好填进 state.ActualAmount
//
// 这样 helper 是 modality-agnostic 的纯 BYOK+Hold/Settle 编排.

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/billing"
	mrbyok "github.com/biumind/biumind/services/model-relay/internal/byok"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// ModalityBilling — handler 注入的依赖, 复用 chat 的 Billing + BYOK 客户端.
type ModalityBilling struct {
	Billing *billing.Client
	BYOK    *mrbyok.Client
	Logger  *slog.Logger
}

// modalityState — 单次请求的计费状态. 跟 requestState (chat) 平行结构.
type modalityState struct {
	UserID       string
	ProviderName string
	ModelName    string

	// BYOK 命中即跳过 Hold.
	IsBYOK bool

	// 平台路径
	HoldID  string
	Pricing *billing.PricingEntry

	// 由 caller 在 finalize 之前填.
	Success      bool
	ErrCode      string
	ActualAmount int64
}

// PreflightOpts — preflight 的 caller 输入.
type PreflightOpts struct {
	ModelCode    string
	ProviderCode string

	// PricingRefType — billing.pricing_book.ref_type (查价表用):
	//   embedding / rerank / audio_speech / aigc_image / aigc_video
	PricingRefType string

	// HoldRefType — credit_holds.ref_type (Hold 行的 ref_type):
	//   embedding_request / rerank_request / audio_speech_request /
	//   image_request / video_request
	HoldRefType string

	// MaxAmount — caller 估好的最大可能成本 (millicents). <=0 时跳过 Hold
	// (零成本场景, 例如 prompt 长度估不出来 / pricing 缺失).
	MaxAmount int64

	// IdempotencyKey — 可选, 防重复扣款; 通常用 X-Request-ID.
	IdempotencyKey string
	// RefID — 跟 chat 一样填 X-Request-Id, 让 admin 报表能 join.
	RefID string

	// TTLSeconds — Hold 自动过期时间. video=600, image=300, embed/rerank/speech=60.
	TTLSeconds int
}

// Preflight — BYOK 检查 + 平台 Hold. cont=false 时已写 402 等错误响应,
// caller 直接 return.
//
// creds 在 BYOK 命中时会被覆盖 (APIKey 替换成用户自己的 key).
func (mb *ModalityBilling) Preflight(
	w http.ResponseWriter,
	r *http.Request,
	creds *provider.Credentials,
	opts PreflightOpts,
) (*modalityState, bool) {
	st := &modalityState{
		ModelName:    opts.ModelCode,
		ProviderName: opts.ProviderCode,
	}
	if c, ok := bauth.ClaimsFrom(r.Context()); ok {
		st.UserID = c.UserID
	}

	// 1. BYOK 优先 — 命中即跳过 Hold (跟 chat 同语义)
	if mb.BYOK != nil && st.UserID != "" && opts.ProviderCode != "" {
		if k, err := mb.BYOK.Get(r.Context(), st.UserID, opts.ProviderCode); err == nil && k != nil {
			st.IsBYOK = true
			creds.APIKey = k.APIKey
		} else if err != nil && !errors.Is(err, mrbyok.ErrKeyNotFound) {
			mb.warn("byok lookup failed (falling back to platform)",
				"err", err, "user", st.UserID, "provider", opts.ProviderCode)
		}
	}

	// 2. 平台路径 → LookupPrice + Hold
	if !st.IsBYOK && mb.Billing != nil && st.UserID != "" {
		entry, perr := mb.Billing.LookupPrice(r.Context(),
			opts.PricingRefType, opts.ModelCode)
		if perr != nil {
			// pricing 不存在 — 老模型 / 未配价格. 不阻断, 留旧路径 (relay 不计费).
			// 跟 chat 同行为, 灰度上线友好.
			mb.warn("pricing not found; will not be billed",
				"ref_type", opts.PricingRefType, "model", opts.ModelCode, "err", perr)
			return st, true
		}
		st.Pricing = entry

		if opts.MaxAmount <= 0 {
			// caller 没估出 max — 跳过 Hold (request body 太小 / 估算函数返 0)
			return st, true
		}

		ttl := opts.TTLSeconds
		if ttl <= 0 {
			ttl = 60
		}
		hold, herr := mb.Billing.Hold(r.Context(), billing.HoldArgs{
			UserID:         st.UserID,
			MaxAmount:      opts.MaxAmount,
			RefType:        opts.HoldRefType,
			RefID:          opts.RefID,
			IdempotencyKey: opts.IdempotencyKey,
			TTLSeconds:     ttl,
			ModelCode:      opts.ModelCode,
			ProviderCode:   opts.ProviderCode,
		})
		if errors.Is(herr, billing.ErrInsufficient) {
			writeJSONErr(w, http.StatusPaymentRequired, "insufficient_credits",
				"余额不足, 请充值")
			return nil, false
		}
		if herr != nil {
			// hold 网络异常 — 不阻断 (优先用户体验, 留 audit 警告)
			mb.warn("hold failed; will not be billed",
				"err", herr, "user", st.UserID, "model", opts.ModelCode)
			return st, true
		}
		st.HoldID = hold.ID
	}
	return st, true
}

// Finalize — defer 调用. 根据 state.Success / ActualAmount 决定 Settle / Release.
// remark 写入 credit_logs (例: "embedding-bge-m3" / "audio-speech-cosyvoice").
//
// caller 必须在 defer 触发前填 st.Success / st.ActualAmount.
func (mb *ModalityBilling) Finalize(st *modalityState, remark string) {
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// BYOK 路径 — 仅 touch 时间 / 失败计数, 不动 platform credit.
	if st.IsBYOK {
		if mb.BYOK == nil {
			return
		}
		if st.Success {
			_ = mb.BYOK.TouchUsed(ctx, st.UserID, st.ProviderName)
			return
		}
		// 跟 chat 一样: 仅 401/403 类失败才计入 BYOK 失败计数
		if isUpstreamAuthFail(st.ErrCode) {
			_, _ = mb.BYOK.IncrementFailure(ctx, st.UserID, st.ProviderName)
		}
		return
	}

	// 平台路径
	if st.HoldID == "" || mb.Billing == nil {
		return
	}
	if !st.Success {
		// 失败 → Release 全部 hold. ActualAmount 由 caller 决定 (有些半成功
		// 场景 caller 想强行 settle, 不走这里)
		_ = mb.Billing.Release(ctx, st.HoldID)
		return
	}
	// 成功路径: ActualAmount=0 也走 Settle (write 0 行到 credit_logs 留审计)
	if err := mb.Billing.Settle(ctx, st.HoldID, st.ActualAmount, remark); err != nil {
		mb.error("settle failed, releasing", "err", err, "hold", st.HoldID)
		_ = mb.Billing.Release(ctx, st.HoldID)
	}
}

func (mb *ModalityBilling) warn(msg string, args ...any) {
	if mb.Logger != nil {
		mb.Logger.Warn(msg, args...)
	}
}

func (mb *ModalityBilling) error(msg string, args ...any) {
	if mb.Logger != nil {
		mb.Logger.Error(msg, args...)
	}
}
