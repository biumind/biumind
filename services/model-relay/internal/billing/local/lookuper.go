// Package local 提供 billing.PriceLookuper 的本地实现 — 走 model_relay.pricing
// 表 (经 registry.Cache + PricingRepo),把 numeric 原币种(USD/CNY)换算成
// billing 协议要求的 millicents.
//
// 历史: W1 期 LookupPrice 走 HTTP 到 identity 的 billing.pricing_book,但
// 这张表跟 admin 后台改的 model_relay.pricing 数据无同步,glm-5.1 等新
// model 配了价但不扣积分. W4 整合 SoT: model_relay.pricing 单一权威,
// 这里做 numeric → millicents 的标准化.
//
// 单位:
//   - 1 CNY = 1000 millicents
//   - 1 USD = fxRateUSDtoCNY * 1000 millicents (默认 7200, 即 7.2 CNY/USD)
//
// PricingEntry.cost_basis 按 refType 选:
//   chat / embedding / rerank → per_mtok (rerank 用 per_search_unit 字段)
//   audio_speech → per_kchar
//   aigc_image → per_call (一张图)
//   aigc_video → per_second
//
// 如果某个 refType 在 pricing 行里没对应字段填(例如 chat 模型走 image
// refType), Lookup 返 ErrPricingNotFound,跟历史 HTTP 实现一致让上层
// 走"不计费"路径.

package local

import (
	"context"
	"errors"
	"fmt"

	"github.com/biumind/biumind/services/model-relay/internal/billing"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Lookuper 用具体类型 *registry.Cache + *registry.PricingRepo — 这层是
// main.go 唯一的 wiring point, 不需要接口隔离 (没有多种实现需求).
type Lookuper struct {
	Cache          *registry.Cache
	Pricing        *registry.PricingRepo
	FxRateUSDtoCNY float64 // e.g. 7.2; 0/负数 → 用默认 7.2
}

// NewLookuper 构造一个 Lookuper. fxRate 0 时用默认 7.2.
func NewLookuper(cache *registry.Cache, pricing *registry.PricingRepo, fxRateUSDtoCNY float64) *Lookuper {
	if fxRateUSDtoCNY <= 0 {
		fxRateUSDtoCNY = 7.2
	}
	return &Lookuper{
		Cache:          cache,
		Pricing:        pricing,
		FxRateUSDtoCNY: fxRateUSDtoCNY,
	}
}

// Lookup 实现 billing.PriceLookuper. 找不到 model / 没价 / refType 字段
// 缺失 → 返 ErrPricingNotFound 让上层走不计费分支.
func (l *Lookuper) Lookup(ctx context.Context, refType, modelCode string) (*billing.PricingEntry, error) {
	model, err := l.Cache.GetModelByCode(ctx, modelCode)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, billing.ErrPricingNotFound
		}
		return nil, fmt.Errorf("lookup model %q: %w", modelCode, err)
	}
	pricing, err := l.Pricing.GetCurrent(ctx, model.ID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, billing.ErrPricingNotFound
		}
		return nil, fmt.Errorf("lookup pricing for %q: %w", modelCode, err)
	}
	return l.convert(refType, modelCode, pricing)
}

// convert 把 registry.Pricing (numeric 原币种) 转成 billing.PricingEntry
// (millicents 标价 unit). 不存在的 refType / 字段 → ErrPricingNotFound.
func (l *Lookuper) convert(refType, modelCode string, p *registry.Pricing) (*billing.PricingEntry, error) {
	// 单位换算因子: 原币种 → millicents
	multiplier := 1000.0
	if p.Currency == registry.CurrencyUSD {
		multiplier = l.FxRateUSDtoCNY * 1000
	}

	entry := &billing.PricingEntry{
		RefType:             refType,
		PricingKey:          modelCode,
		MarkupRatio:         p.MarkupRatio,
		MinCharge:           p.MinCharge,
		MaxChargePerRequest: p.MaxChargePerRequest,
	}
	// MarkupRatio 兜底 — 老 row 可能是 0 (新字段 default 3.0 但历史 row
	// 可能没触发 default 重算); 0 markup 会让 finalize 算出 0,扣不到积分
	if entry.MarkupRatio <= 0 {
		entry.MarkupRatio = 3.0
	}

	switch refType {
	case "chat":
		entry.CostBasis = "per_mtok"
		entry.CostInputPerUnit = int64(p.InputPerMTok * multiplier)
		entry.CostOutputPerUnit = int64(p.OutputPerMTok * multiplier)
		entry.CostCacheRead = int64(p.CacheReadPerMTok * multiplier)
		entry.CostCacheWrite = int64(p.CacheWritePerMTok * multiplier)
	case "embedding":
		entry.CostBasis = "per_mtok"
		entry.CostInputPerUnit = int64(p.InputPerMTok * multiplier)
	case "rerank":
		entry.CostBasis = "per_search_unit"
		entry.CostInputPerUnit = int64(p.CostPerSearchUnit * multiplier)
	case "parse_page":
		// client-docproc W4：wiki 云端解析按页计费。复用 cost_per_search_unit
		// 列（通用按单元价格），价格挂在 pseudo-model 上（wiki-parse-text /
		// wiki-ocr 等），不同处理档位 = 不同 pseudo-model 不同单价。
		entry.CostBasis = "per_page"
		entry.CostInputPerUnit = int64(p.CostPerSearchUnit * multiplier)
	case "audio_speech":
		entry.CostBasis = "per_kchar"
		if p.CostPerCharacter == nil || *p.CostPerCharacter <= 0 {
			return nil, billing.ErrPricingNotFound
		}
		// per_character → per_kchar = char × 1000
		entry.CostInputPerUnit = int64(*p.CostPerCharacter * 1000 * multiplier)
	case "aigc_image":
		entry.CostBasis = "per_call"
		if p.CostPerImage == nil || *p.CostPerImage <= 0 {
			return nil, billing.ErrPricingNotFound
		}
		entry.CostInputPerUnit = int64(*p.CostPerImage * multiplier)
	case "aigc_video":
		entry.CostBasis = "per_second"
		if p.CostPerVideoSecond == nil || *p.CostPerVideoSecond <= 0 {
			return nil, billing.ErrPricingNotFound
		}
		entry.CostInputPerUnit = int64(*p.CostPerVideoSecond * multiplier)
	case "audio_transcription":
		// ASR (paraformer-v2 / sensevoice / Whisper). 复用 cost_per_audio_second
		// 字段 (TTS 也在用), 单位 = 原币种/秒. 标定 cost_basis = per_second.
		// handler 调 CalculateSpeech(audioMs) 时会按 chars/1000 算, 这里乘
		// 1000 把 per_second 换成 per_kunit 让公式契合 (handler 传 ms 而非
		// seconds, ms/1000 = seconds, 再 × cost_per_second = 总成本).
		entry.CostBasis = "per_second"
		if p.CostPerAudioSecond == nil || *p.CostPerAudioSecond <= 0 {
			return nil, billing.ErrPricingNotFound
		}
		entry.CostInputPerUnit = int64(*p.CostPerAudioSecond * multiplier)
	default:
		return nil, fmt.Errorf("unknown ref_type %q for pricing of %q", refType, modelCode)
	}

	// 全 0 输入 = 没配价格,跟 ErrPricingNotFound 等价 (避免下游 EstimateChat
	// 算出 0,然后 Hold/Settle 都跳过反而看上去"通了")
	if entry.CostInputPerUnit == 0 && entry.CostOutputPerUnit == 0 {
		return nil, billing.ErrPricingNotFound
	}

	return entry, nil
}
