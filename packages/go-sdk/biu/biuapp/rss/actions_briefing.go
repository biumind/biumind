// briefing_today_audio — TTS 简报 (M8.4). 走 model-relay /v1/audio/speech,
// 24h cache, 返 base64 mp3 + script + 元数据.
//
// 为什么不直接 stream audio bytes (binary response):
//   - biuapp action 协议是 JSON in/out (Risk/InputSchema/...). binary
//     需要走另一条路径(file ID), 复杂度不值得; client 解码 base64 ~ 100KB
//     一次性是毫秒级开销
//   - 顺便把 script / voice / cached 一起返, client 可以 inspector 显示

package rss

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// BriefingSynth — services 包提供的简报合成器接口. SDK 不绑实现.
//
// app_center 包装一个 *briefing.Synthesizer 满足这个接口, 调用方传入的
// userID 就是 caller scope (rss feed 是 user scope).
type BriefingSynth interface {
	// SynthForUser — 输入 userID + 已计算好的脚本 + 内容签名(headline ids),
	// 输出 audio bytes + cache 元数据. SDK 这层只负责把 picks 转脚本;
	// 实现负责 cache 查找 / TTS / 持久化.
	SynthForUser(
		ctx context.Context,
		userID string,
		scriptText string,
		headlineIDs []string,
		headlineN int,
	) (*BriefingResult, error)
}

// BriefingResult — caller (action handler) 投影成 JSON 返客户端的字段.
type BriefingResult struct {
	Mp3        []byte
	Script     string
	Voice      string
	Model      string
	Characters int
	Cached     bool
	HeadlineN  int
}

// WithBriefingSynth — main.go 注入. nil 时 briefing_today_audio 返
// "briefing not wired" 错误, 客户端隐藏播放按钮即可.
func (a *App) WithBriefingSynth(s BriefingSynth) *App {
	a.briefing = s
	return a
}

func (a *App) invokeBriefingTodayAudio(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.briefing == nil {
		return nil, errors.New("rss: briefing not wired (model-relay url 未配)")
	}
	if a.today == nil {
		return nil, errors.New("rss: today picker not wired")
	}
	scope, scopeID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if scope != "user" {
		// 简报只对个人; org/global 的 today 没意义
		return nil, errors.New("rss: briefing only supports user scope")
	}

	picks, err := a.today.PickFor(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	script := FromPicks(picks)

	ids := make([]string, len(picks.Headline))
	for i, e := range picks.Headline {
		ids[i] = e.ID
	}

	res, err := a.briefing.SynthForUser(ctx, scopeID, script.Text, ids, script.HeadlineN)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"audio_b64":  base64.StdEncoding.EncodeToString(res.Mp3),
		"audio_size": len(res.Mp3),
		"mime":       "audio/mpeg",
		"script":     res.Script,
		"voice":      res.Voice,
		"model":      res.Model,
		"characters": res.Characters,
		"cached":     res.Cached,
		"headline_n": res.HeadlineN,
	}, nil
}

// BriefingScript — 朗读脚本生成结果.
type BriefingScript struct {
	Text       string
	HeadlineN  int
	Characters int
}

// FromPicks — TodayPicks → 朗读文本. 纯函数, 不调 LLM
// (ai_takeaway 已经是 digest worker 算好的, 这里做拼接 + 清洗即可).
//
// 中文 ~ 200 字, 朗读约 30 秒. 取 top 5 headline; missed/trends 不放,
// 避免简报跑出 5 分钟.
func FromPicks(p *TodayPicks) BriefingScript {
	if p == nil || len(p.Headline) == 0 {
		return BriefingScript{Text: "今天暂时没有可推荐的文章。"}
	}
	const (
		maxHeadline      = 5
		maxScriptChars   = 600
		maxTitleChars    = 60
		maxTakeawayChars = 80
	)
	picks := p.Headline
	if len(picks) > maxHeadline {
		picks = picks[:maxHeadline]
	}
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九", "十"}

	var sb strings.Builder
	fmt.Fprintf(&sb, "今天为你挑了 %d 篇文章, 一起来看看吧。", len(picks))
	for i, e := range picks {
		title := clipRunes(stripMD(e.Title), maxTitleChars)
		take := clipRunes(stripMD(e.AITakeaway), maxTakeawayChars)
		feed := clipRunes(stripMD(e.FeedTitle), 30)

		var seg strings.Builder
		num := "其他"
		if i+1 >= 0 && i+1 < len(digits) {
			num = digits[i+1]
		}
		fmt.Fprintf(&seg, "第%s篇", num)
		if feed != "" {
			fmt.Fprintf(&seg, ", 来自《%s》", feed)
		}
		fmt.Fprintf(&seg, ": %s。", title)
		if take != "" {
			fmt.Fprintf(&seg, "%s。", trimTrailingPunct(take))
		}
		if runeCount(sb.String())+runeCount(seg.String()) > maxScriptChars {
			break
		}
		sb.WriteString(seg.String())
	}
	sb.WriteString("以上就是今天的简报, 完整阅读请回到收件箱。")
	text := sb.String()
	return BriefingScript{
		Text:       text,
		HeadlineN:  len(picks),
		Characters: runeCount(text),
	}
}

// stripMD — 去掉 markdown 残留 (粗糙但够用; TTS 朗读时不会读出
// "星号星号粗体星号星号").
func stripMD(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer(
		"**", "", "__", "", "`", "", "*", "", "_", "",
		"[", "", "]", "",
		"#", "",
		"\n", " ", "\r", " ", "\t", " ",
	)
	s = r.Replace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func clipRunes(s string, max int) string {
	if runeCount(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// trimTrailingPunct — takeaway 通常已带句末符号; 重复句号会被读成
// "句号句号", 去掉 trailing 的中英文句末符号.
func trimTrailingPunct(s string) string {
	for {
		runes := []rune(s)
		if len(runes) == 0 {
			return ""
		}
		last := runes[len(runes)-1]
		// 中英文句末符号 (注意 fullwidth 跟 halfwidth 是不同 rune,
		// '！'/'?' 全角 vs '!'/'?' 半角).
		switch last {
		case '。', '.', '!', '?', '！', '？', '…', ';', '；', ' ':
			s = string(runes[:len(runes)-1])
		default:
			return s
		}
	}
}
