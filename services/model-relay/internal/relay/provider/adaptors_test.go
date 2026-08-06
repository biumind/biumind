package provider_test

// adaptors_test 验证 v0.3 全模态接口的 type assertion 路径正确, 且现有
// openai / anthropic adaptor 满足 ChatAdaptor 接口 (向后兼容护栏).
//
// 这是 M0.2 的核心安全网 — 如果未来某次重构 break 了接口契约, 这些测试
// 会立即报警, 不会等到运行时 502.

import (
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/anthropic"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

// 编译期断言 — 现有 adaptor 必须满足 ChatAdaptor 接口.
// 若某天有人删了 Capabilities() 方法, go build 直接挂.
var (
	_ provider.ChatAdaptor = (*openai.Adaptor)(nil)
	_ provider.ChatAdaptor = (*anthropic.Adaptor)(nil)
	// M2: openai 也满足 EmbedAdaptor (anthropic 没 embedding 端点不实装).
	_ provider.EmbedAdaptor = (*openai.Adaptor)(nil)
	// M2.5: openai 也满足 RerankAdaptor (走 Cohere /v1/rerank shape;
	// bge-reranker-v2-m3 经 SiliconFlow / Jina / 新-API 等 OpenAI-compat 上游).
	_ provider.RerankAdaptor = (*openai.Adaptor)(nil)
	// M6: openai 也满足 TranscribeAdaptor (Whisper / GPT-4o-transcribe).
	_ provider.TranscribeAdaptor = (*openai.Adaptor)(nil)
)

// 编译期断言 — ChatAdaptor 仍然满足老 Adaptor 接口 (BaseAdaptor 也满足).
var (
	_ provider.Adaptor     = (*openai.Adaptor)(nil)
	_ provider.Adaptor     = (*anthropic.Adaptor)(nil)
	_ provider.BaseAdaptor = (*openai.Adaptor)(nil)
	_ provider.BaseAdaptor = (*anthropic.Adaptor)(nil)
)

func TestCapabilitiesDeclared(t *testing.T) {
	cases := []struct {
		name string
		adp  provider.BaseAdaptor
		want []string
	}{
		{"openai", openai.New(), []string{"chat", "embedding", "rerank", "audio_transcription"}},
		{"anthropic", anthropic.New(), []string{"chat"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := tc.adp.Capabilities()
			if len(caps) != len(tc.want) {
				t.Fatalf("caps count: got %v, want %v", caps, tc.want)
			}
			for i := range caps {
				if caps[i] != tc.want[i] {
					t.Errorf("caps[%d]: got %q, want %q", i, caps[i], tc.want[i])
				}
			}
		})
	}
}

// TypeAssertion 验证 — 路由层用 type assertion 决定是否支持某 modality.
// 这里的逻辑模拟 ModeRouter (M0.3) 的核心分发.
func TestModalityTypeAssertion(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(openai.New())
	reg.Register(anthropic.New())

	got, _ := reg.Get("openai")

	// openai 必须满足 ChatAdaptor + EmbedAdaptor (M2) + RerankAdaptor (M2.5).
	if _, ok := got.(provider.ChatAdaptor); !ok {
		t.Error("openai should satisfy ChatAdaptor")
	}
	if _, ok := got.(provider.EmbedAdaptor); !ok {
		t.Error("openai should satisfy EmbedAdaptor (M2 added /v1/embeddings)")
	}
	if _, ok := got.(provider.RerankAdaptor); !ok {
		t.Error("openai should satisfy RerankAdaptor (M2.5 added /v1/rerank)")
	}
	if _, ok := got.(provider.TranscribeAdaptor); !ok {
		t.Error("openai should satisfy TranscribeAdaptor (M6 added Whisper)")
	}

	// openai 暂不应满足 SpeechAdaptor / ImageAdaptor (cosyvoice/wanx 等
	// 走 dashscope 私有协议; openai 自家 TTS=tts-1 / image=dall-e 后续按
	// 需求加).
	if _, ok := got.(provider.SpeechAdaptor); ok {
		t.Error("openai should NOT satisfy SpeechAdaptor yet")
	}
	if _, ok := got.(provider.ImageAdaptor); ok {
		t.Error("openai should NOT satisfy ImageAdaptor yet")
	}

	// anthropic 只 chat, 不应该满足任何非 chat 接口
	got2, _ := reg.Get("anthropic")
	if _, ok := got2.(provider.EmbedAdaptor); ok {
		t.Error("anthropic should NOT satisfy EmbedAdaptor")
	}
	if _, ok := got2.(provider.SpeechAdaptor); ok {
		t.Error("anthropic should NOT satisfy SpeechAdaptor")
	}
}
