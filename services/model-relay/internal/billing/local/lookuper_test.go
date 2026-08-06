// Lookuper 把 model_relay.pricing (numeric 原币种) 翻成 billing.PricingEntry
// (millicents). 这里覆盖每条 refType 的字段映射 + USD/CNY 换算 + 各种边界
// (空字段 / nil pointer / 0 markup fallback / 全 0 输入).
//
// Cache 部分用 stub 避免真实 DB 依赖. PricingRepo 部分需要 DB,这里只测
// convert 的纯函数路径 — 直接把 *registry.Pricing 喂进 convert(...) 跳过
// repo. 集成路径在 model-relay 端到端测试覆盖.

package local

import (
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/billing"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func ptrFloat(v float64) *float64 { return &v }

func TestConvert_ChatUSD_ToMillicents(t *testing.T) {
	// haiku 价格: $1/$5/$0.1/$1.25 per Mtok. 7.2 USD/CNY → millicents:
	//   in:    1 × 7.2 × 1000 = 7200
	//   out:   5 × 7.2 × 1000 = 36000
	//   read:  0.1 × 7.2 × 1000 = 720
	//   write: 1.25 × 7.2 × 1000 = 9000
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:          registry.CurrencyUSD,
		InputPerMTok:      1.0,
		OutputPerMTok:     5.0,
		CacheReadPerMTok:  0.1,
		CacheWritePerMTok: 1.25,
		MarkupRatio:       3.0,
		MinCharge:         1000,
	}
	got, err := l.convert("chat", "claude-haiku-4-5", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.CostInputPerUnit != 7200 || got.CostOutputPerUnit != 36000 {
		t.Errorf("input/output: %d / %d", got.CostInputPerUnit, got.CostOutputPerUnit)
	}
	if got.CostCacheRead != 720 || got.CostCacheWrite != 9000 {
		t.Errorf("cache: %d / %d", got.CostCacheRead, got.CostCacheWrite)
	}
	if got.CostBasis != "per_mtok" {
		t.Errorf("cost_basis: %s", got.CostBasis)
	}
	if got.MarkupRatio != 3.0 || got.MinCharge != 1000 {
		t.Errorf("markup/min: %v / %d", got.MarkupRatio, got.MinCharge)
	}
}

func TestConvert_ChatCNY_NoFxConversion(t *testing.T) {
	// deepseek-chat 标 CNY ¥1/¥2 per Mtok → millicents 直接 *1000:
	//   in:  1 × 1000 = 1000
	//   out: 2 × 1000 = 2000
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:      registry.CurrencyCNY,
		InputPerMTok:  1.0,
		OutputPerMTok: 2.0,
		MarkupRatio:   2.5,
	}
	got, err := l.convert("chat", "deepseek-chat", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.CostInputPerUnit != 1000 || got.CostOutputPerUnit != 2000 {
		t.Errorf("CNY 不该走 fxRate: %+v", got)
	}
}

func TestConvert_Embedding_OnlyInput(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:      registry.CurrencyUSD,
		InputPerMTok:  0.1,
		OutputPerMTok: 999, // 不该被取
		MarkupRatio:   3.0,
	}
	got, err := l.convert("embedding", "text-embedding-3", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.CostInputPerUnit != 720 {
		t.Errorf("input: %d (want 720 = 0.1*7200)", got.CostInputPerUnit)
	}
	if got.CostOutputPerUnit != 0 {
		t.Errorf("embedding 不该有 output: %d", got.CostOutputPerUnit)
	}
}

func TestConvert_Rerank_UsesSearchUnit(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:          registry.CurrencyUSD,
		CostPerSearchUnit: 0.002, // $2/1000 search units
		MarkupRatio:       3.0,
	}
	got, err := l.convert("rerank", "rerank-v3", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.CostBasis != "per_search_unit" {
		t.Errorf("cost_basis: %s", got.CostBasis)
	}
	rate := 0.002
	want := int64(rate * 7200)
	if got.CostInputPerUnit != want {
		t.Errorf("search_unit: %d (want %d)", got.CostInputPerUnit, want)
	}
}

func TestConvert_AigcImage_FromCostPerImage(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:     registry.CurrencyUSD,
		CostPerImage: ptrFloat(0.04), // $0.04/image
		MarkupRatio:  3.0,
	}
	got, err := l.convert("aigc_image", "dall-e-3", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	rate := 0.04
	want := int64(rate * 7200)
	if got.CostBasis != "per_call" || got.CostInputPerUnit != want {
		t.Errorf("image entry: %+v (want input=%d)", got, want)
	}
}

func TestConvert_AigcImage_NilCostPerImage_NotFound(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:    registry.CurrencyUSD,
		MarkupRatio: 3.0,
		// CostPerImage = nil
	}
	_, err := l.convert("aigc_image", "no-image-model", p)
	if err != billing.ErrPricingNotFound {
		t.Errorf("want ErrPricingNotFound, got %v", err)
	}
}

func TestConvert_AudioSpeech_PerCharToPerKchar(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:         registry.CurrencyUSD,
		CostPerCharacter: ptrFloat(0.000015), // $15/M char = $0.000015/char
		MarkupRatio:      3.0,
	}
	got, err := l.convert("audio_speech", "tts-1", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.CostBasis != "per_kchar" {
		t.Errorf("cost_basis: %s", got.CostBasis)
	}
	// per_char × 1000 → per_kchar; × 7200 → millicents
	rate := 0.000015
	want := int64(rate * 1000 * 7200)
	if got.CostInputPerUnit != want {
		t.Errorf("kchar: got %d want %d", got.CostInputPerUnit, want)
	}
}

func TestConvert_UnknownRefType(t *testing.T) {
	l := NewLookuper(nil, nil, 7.2)
	_, err := l.convert("video_translation", "x", &registry.Pricing{})
	if err == nil {
		t.Errorf("expected error for unknown ref_type")
	}
}

func TestConvert_AllZeroInput_NotFound(t *testing.T) {
	// 防御: pricing 字段全 0 (admin 误录) 不该导致 finalize 算出 0,扣 0
	// 而看上去"通了". 直接当 ErrPricingNotFound 走"不计费"分支.
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:    registry.CurrencyUSD,
		MarkupRatio: 3.0,
		// 所有 cost_* 都 0
	}
	_, err := l.convert("chat", "free-model", p)
	if err != billing.ErrPricingNotFound {
		t.Errorf("want ErrPricingNotFound for all-zero price, got %v", err)
	}
}

func TestConvert_ZeroMarkup_FallsBackTo3x(t *testing.T) {
	// 老 row markup=0 (新字段默认 3.0 但历史 row 可能为 0) → 兜底 3.0
	// 防 finalize 把 list 算成 0 (cost × 0 = 0).
	l := NewLookuper(nil, nil, 7.2)
	p := &registry.Pricing{
		Currency:     registry.CurrencyUSD,
		InputPerMTok: 1.0,
		MarkupRatio:  0, // 老数据 / 误录
	}
	got, err := l.convert("chat", "model", p)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.MarkupRatio != 3.0 {
		t.Errorf("markup fallback: got %v want 3.0", got.MarkupRatio)
	}
}

func TestConvert_FxRateDefault(t *testing.T) {
	// NewLookuper(0) 应该 fallback 到 7.2
	l := NewLookuper(nil, nil, 0)
	if l.FxRateUSDtoCNY != 7.2 {
		t.Errorf("default fxRate: got %v want 7.2", l.FxRateUSDtoCNY)
	}
	l2 := NewLookuper(nil, nil, -1)
	if l2.FxRateUSDtoCNY != 7.2 {
		t.Errorf("negative fxRate: got %v want 7.2", l2.FxRateUSDtoCNY)
	}
}
