package adminapi

import (
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// 字典覆盖度的最小验证: LiteLLM 字典必须包含足够的非-chat 主流模型,
// 否则 commit Litellm 字典就没有意义. 这些 case 全部应该命中 (lookup
// 不返回 "").
func TestLookupLiteLLMMode_KnownEntries(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		// embedding
		{"text-embedding-3-small", registry.ModeEmbedding},
		{"text-embedding-3-large", registry.ModeEmbedding},
		{"text-embedding-ada-002", registry.ModeEmbedding},

		// audio speech (TTS)
		{"tts-1", registry.ModeAudioSpeech},
		{"tts-1-hd", registry.ModeAudioSpeech},
		{"gpt-4o-mini-tts", registry.ModeAudioSpeech},

		// audio transcription (ASR)
		{"whisper-1", registry.ModeAudioTranscription},
		{"gpt-4o-mini-transcribe", registry.ModeAudioTranscription},

		// image generation
		{"dall-e-3", registry.ModeImageGeneration},
		{"dall-e-2", registry.ModeImageGeneration},
		{"gpt-image-1", registry.ModeImageGeneration},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			got := lookupLiteLLMMode(tc.code)
			if got != tc.want {
				t.Errorf("lookupLiteLLMMode(%q) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// 测 strip-prefix 路径: LiteLLM 收录 "dall-e-3" 这种短名, 但上游有时
// 同步进来的是 "azure/dall-e-3" 这种带 vendor 前缀的命名. lookup 应当
// 在 exact miss 后剥前缀再试.
func TestLookupLiteLLMMode_StripPrefix(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"openai/dall-e-3", registry.ModeImageGeneration},
		{"azure/text-embedding-3-small", registry.ModeEmbedding},
		{"some-vendor/tts-1-hd", registry.ModeAudioSpeech},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			got := lookupLiteLLMMode(tc.code)
			if got != tc.want {
				t.Errorf("lookupLiteLLMMode(%q) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// 不命中场景: chat 模型 LiteLLM 字典里**故意不收**, 让 schema DEFAULT
// 兜底; 国产小众模型 LiteLLM 也没收录, 应该返回 "" 让启发式接管.
func TestLookupLiteLLMMode_Misses(t *testing.T) {
	cases := []string{
		"gpt-4o-mini",          // chat — LiteLLM 字典故意不收
		"claude-haiku-4-5",     // chat — 同上
		"deepseek-chat",        // chat — 同上
		"cosyvoice-v1",         // 国产 TTS, LiteLLM 没收 → 走启发式
		"paraformer-v2",        // 国产 ASR
		"qwen-image",           // 国产 image
		"bytedance/seedance-2.0", // 国产 video
		"definitely-not-a-real-model-12345", // 完全不存在
	}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			got := lookupLiteLLMMode(code)
			if got != "" {
				t.Errorf("lookupLiteLLMMode(%q) = %q, want \"\" (miss)", code, got)
			}
		})
	}
}

// inferMode 整合验证: LiteLLM 命中的优先返回字典结果, 其它走启发式.
func TestInferMode_LiteLLMTakesPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		want      string
	}{
		// LiteLLM 字典命中 (无启发式关键字也照样判对)
		{"dall-e-3 via litellm", "dall-e-3", registry.ModeImageGeneration},
		{"tts-1 via litellm", "tts-1", registry.ModeAudioSpeech},
		// 启发式 fallback (LiteLLM 没收录的国产模型)
		{"bge-m3 via heuristic", "bge-m3", registry.ModeEmbedding},
		{"whisper-large-v3 via heuristic", "whisper-large-v3", registry.ModeAudioTranscription},
		// chat fallback
		{"gpt-4 chat default", "gpt-4-turbo", registry.ModeChat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferMode(tc.modelName, "", "")
			if got != tc.want {
				t.Errorf("inferMode(%q) = %q, want %q", tc.modelName, got, tc.want)
			}
		})
	}
}
