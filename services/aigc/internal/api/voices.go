package api

// voices.go — 数字人音色字典 endpoint (P5-4 voices 子节).
//
//   GET /v1/voices?provider=volcengine
//
// 音色目前是静态字典 (各家 TTS 厂商音色 ID 列表). 没用 DB 表是因为:
//   - 数据量小 (每家几十到几百个), 改动频率低 (季度级)
//   - 各厂商 voice_id 互不通用, 用 provider 维度区分够用
// 后续如果用户/团队上传 voice clone (训练自己的音色), 再迁到 aigc.voices 表.

import (
	"net/http"
)

// VoiceEntry — 单个音色条目, 给前端选择器用.
type VoiceEntry struct {
	ID           string `json:"id"`            // 厂商 voice_id (如 "BV001_streaming")
	Name         string `json:"name"`          // 中文显示名
	Provider     string `json:"provider"`      // volcengine / dashscope / azure
	Language     string `json:"language"`      // zh-CN | en-US | ...
	Gender       string `json:"gender"`        // male | female | neutral
	Style        string `json:"style"`         // 标签: 温柔 / 活泼 / 商务 / ...
	SampleURL    string `json:"sample_url"`    // 试听 URL (可选)
}

// 内置音色 — MVP 列表, 后续从 zhiying-portal 或厂商最新文档同步.
var defaultVoices = []VoiceEntry{
	// VolcEngine 通用音色 (节选, 按用户使用频率排)
	{ID: "BV001_streaming", Name: "灿灿", Provider: "volcengine", Language: "zh-CN", Gender: "female", Style: "温柔"},
	{ID: "BV002_streaming", Name: "炀炀", Provider: "volcengine", Language: "zh-CN", Gender: "male", Style: "醇厚"},
	{ID: "BV007_streaming", Name: "亲切女声", Provider: "volcengine", Language: "zh-CN", Gender: "female", Style: "亲切"},
	{ID: "BV056_streaming", Name: "阳光男声", Provider: "volcengine", Language: "zh-CN", Gender: "male", Style: "活泼"},
	{ID: "BV701_streaming", Name: "擎苍", Provider: "volcengine", Language: "zh-CN", Gender: "male", Style: "新闻"},
	{ID: "BV705_streaming", Name: "悠悠", Provider: "volcengine", Language: "zh-CN", Gender: "female", Style: "新闻"},

	// 英文音色
	{ID: "en_male_corey_mars_bigtts", Name: "Corey", Provider: "volcengine", Language: "en-US", Gender: "male", Style: "narrator"},
	{ID: "en_female_anna_mars_bigtts", Name: "Anna", Provider: "volcengine", Language: "en-US", Gender: "female", Style: "narrator"},

	// DashScope CosyVoice (节选)
	{ID: "longxiaochun", Name: "龙小淳", Provider: "dashscope", Language: "zh-CN", Gender: "female", Style: "温柔"},
	{ID: "longwan", Name: "龙婉", Provider: "dashscope", Language: "zh-CN", Gender: "female", Style: "亲切"},
	{ID: "longxiaobai", Name: "龙小白", Provider: "dashscope", Language: "zh-CN", Gender: "female", Style: "活泼"},
}

func (s *Server) handleListVoices(w http.ResponseWriter, r *http.Request) {
	provider := firstQ(r.URL.Query(), "provider")
	out := make([]VoiceEntry, 0, len(defaultVoices))
	for _, v := range defaultVoices {
		if provider != "" && v.Provider != provider {
			continue
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"voices": out})
}
