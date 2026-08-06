// pricing — LLM 模型价格表 (input / output / cache 美分/百万 token).
//
// 数据来自 provider 公开 pricing 页面, 2026-05 时点. 跟着 provider 涨跌改
// 这里. 单位用 millicents (千分之一美分) 防浮点累加误差; 显示时 ÷100 转成
// 美分, 再 ÷100 转成美元.
//
// 不在表里的模型 → CostFor 返 0 (零成本不影响业务, 只影响仪表板数字).
//
// 添加新模型: 直接加到 catalog 里. cache 价格 (Anthropic prompt caching
// + OpenAI input cache) 按提供方文档拆 read / write 两栏.

package pricing

import "strings"

// Cost in millicents per million tokens (1 token = price/1_000_000).
//
//	millicents = cents * 1000 → 1 USD = 100_000 millicents
//
// 例: claude-haiku-4-5 input $1/M = 1 USD * 100_000 mc/USD / 1M tok
//
//	= 0.1 mc/tok = 100_000 mc/M tok = 100000 (其实直接存 USD/M * 100_000)
//
// 简化: 直接存 USD per million tokens * 100, 单位是 cents/M.
//
//	claude-haiku-4-5 input $1/M → 100 cents/M
//	claude-sonnet-4-5 input $3/M → 300 cents/M
type Price struct {
	InputCentsPerMillion      int64 // prompt
	OutputCentsPerMillion     int64 // completion
	CacheReadCentsPerMillion  int64 // anthropic cache read / openai cached input
	CacheWriteCentsPerMillion int64 // anthropic cache write
}

// catalog — 当前关注的主流模型. 价格按 USD/M 转 cents/M.
var catalog = map[string]Price{
	// ─── Anthropic ─────────────────────────────────────────
	// 文档: https://docs.anthropic.com/en/docs/about-claude/pricing
	"claude-haiku-4-5":  {InputCentsPerMillion: 100, OutputCentsPerMillion: 500, CacheReadCentsPerMillion: 10, CacheWriteCentsPerMillion: 125},
	"claude-sonnet-4-5": {InputCentsPerMillion: 300, OutputCentsPerMillion: 1500, CacheReadCentsPerMillion: 30, CacheWriteCentsPerMillion: 375},
	"claude-opus-4-5":   {InputCentsPerMillion: 1500, OutputCentsPerMillion: 7500, CacheReadCentsPerMillion: 150, CacheWriteCentsPerMillion: 1875},
	"claude-opus-4-7":   {InputCentsPerMillion: 1500, OutputCentsPerMillion: 7500, CacheReadCentsPerMillion: 150, CacheWriteCentsPerMillion: 1875},
	"claude-3-5-haiku":  {InputCentsPerMillion: 80, OutputCentsPerMillion: 400, CacheReadCentsPerMillion: 8, CacheWriteCentsPerMillion: 100},
	"claude-3-5-sonnet": {InputCentsPerMillion: 300, OutputCentsPerMillion: 1500, CacheReadCentsPerMillion: 30, CacheWriteCentsPerMillion: 375},
	"claude-3-opus":     {InputCentsPerMillion: 1500, OutputCentsPerMillion: 7500},

	// ─── OpenAI ────────────────────────────────────────────
	// 文档: https://openai.com/api/pricing/
	"gpt-4o":      {InputCentsPerMillion: 250, OutputCentsPerMillion: 1000, CacheReadCentsPerMillion: 125},
	"gpt-4o-mini": {InputCentsPerMillion: 15, OutputCentsPerMillion: 60, CacheReadCentsPerMillion: 7},
	"gpt-4-turbo": {InputCentsPerMillion: 1000, OutputCentsPerMillion: 3000},
	"o1":          {InputCentsPerMillion: 1500, OutputCentsPerMillion: 6000, CacheReadCentsPerMillion: 750},
	"o1-mini":     {InputCentsPerMillion: 300, OutputCentsPerMillion: 1200, CacheReadCentsPerMillion: 150},
	"o3":          {InputCentsPerMillion: 1000, OutputCentsPerMillion: 4000, CacheReadCentsPerMillion: 250},
	"o4-mini":     {InputCentsPerMillion: 110, OutputCentsPerMillion: 440, CacheReadCentsPerMillion: 28},
}

// Lookup 模糊匹配 model name. 完整 id (e.g. claude-sonnet-4-5-20250101)
// → 找到 prefix-match 的最长 key. 返 zero Price 表示未知模型.
func Lookup(model string) Price {
	model = strings.ToLower(model)
	if p, ok := catalog[model]; ok {
		return p
	}
	// prefix 匹配最长 key
	var bestKey string
	for k := range catalog {
		if strings.HasPrefix(model, k) && len(k) > len(bestKey) {
			bestKey = k
		}
	}
	if bestKey != "" {
		return catalog[bestKey]
	}
	return Price{}
}

// CostMillicents 给出本次请求的总成本 (千分之一美分).
//
//	prompt+completion+cache 各按 token 数 * (cents/M tok) * 1000 / 1_000_000
//	     = tokens * cents/M / 1000 (放大成 millicents)
func CostMillicents(model string, prompt, completion, cacheRead, cacheWrite int64) int64 {
	p := Lookup(model)
	// cents * tokens / 1_000_000 = USD cents 直接得; 我们要 millicents
	// 即 mc = cents * 1000, 总 mc = tokens * cents/M * 1000 / 1_000_000 = tokens * cents/M / 1000.
	mc := func(tok, centsPerM int64) int64 {
		if tok <= 0 || centsPerM <= 0 {
			return 0
		}
		return tok * centsPerM / 1000
	}
	total := mc(prompt, p.InputCentsPerMillion) +
		mc(completion, p.OutputCentsPerMillion) +
		mc(cacheRead, p.CacheReadCentsPerMillion) +
		mc(cacheWrite, p.CacheWriteCentsPerMillion)
	return total
}

// CostCents 同 CostMillicents, 但单位 cents (整数美分). 仪表板展示用.
func CostCents(model string, prompt, completion, cacheRead, cacheWrite int64) int64 {
	return CostMillicents(model, prompt, completion, cacheRead, cacheWrite) / 1000
}
