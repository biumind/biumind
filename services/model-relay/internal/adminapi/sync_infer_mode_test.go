package adminapi

import (
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func TestInferMode(t *testing.T) {
	cases := []struct {
		name      string
		modelName string
		vendor    string
		tags      string
		want      string
	}{
		// embedding
		{"bge-m3 plain", "bge-m3", "BAAI", "", registry.ModeEmbedding},
		{"baai/bge-m3 namespaced", "baai/bge-m3", "BAAI", "", registry.ModeEmbedding},
		{"workers-ai bge", "workers-ai/@cf/baai/bge-m3", "Cloudflare", "", registry.ModeEmbedding},
		{"text-embedding-3", "text-embedding-3-small", "OpenAI", "", registry.ModeEmbedding},
		{"jina-embeddings-v3", "jina-embeddings-v3", "Jina", "", registry.ModeEmbedding},
		{"voyage-3", "voyage-3", "VoyageAI", "", registry.ModeEmbedding},
		{"e5-mistral", "e5-mistral-7b-instruct", "intfloat", "", registry.ModeEmbedding},
		{"gte-large", "gte-large-en-v1.5", "Alibaba", "", registry.ModeEmbedding},

		// image generation
		{"dall-e-3", "dall-e-3", "OpenAI", "", registry.ModeImageGeneration},
		{"sd-3.5", "sd-3.5-large", "Stability", "", registry.ModeImageGeneration},
		{"stable-diffusion-xl", "stable-diffusion-xl", "Stability", "", registry.ModeImageGeneration},
		{"flux pro", "flux-1.1-pro", "BlackForest", "", registry.ModeImageGeneration},
		{"midjourney", "midjourney-v6", "MJ", "", registry.ModeImageGeneration},
		{"imagen-3", "imagen-3.0", "Google", "", registry.ModeImageGeneration},

		// video generation
		{"sora", "sora-1.0", "OpenAI", "", registry.ModeVideoGeneration},
		{"kling 1.6", "kling-1.6-std", "Kuaishou", "", registry.ModeVideoGeneration},
		{"veo-3", "veo-3", "Google", "", registry.ModeVideoGeneration},

		// audio transcription
		{"whisper-large", "whisper-large-v3", "OpenAI", "", registry.ModeAudioTranscription},
		{"transcribe", "gpt-4o-transcribe", "OpenAI", "", registry.ModeAudioTranscription},

		// audio speech (TTS)
		{"tts-1", "tts-1-hd", "OpenAI", "", registry.ModeAudioSpeech},
		{"elevenlabs", "elevenlabs-multilingual-v2", "ElevenLabs", "", registry.ModeAudioSpeech},

		// chat (default fallback) — 经典 LLM 不应被误分类
		{"gpt-4", "gpt-4-turbo", "OpenAI", "Tools,Vision,128K", registry.ModeChat},
		{"claude-3-5", "claude-3-5-sonnet", "Anthropic", "Tools,Vision,200K", registry.ModeChat},
		{"deepseek-chat", "deepseek-chat", "DeepSeek", "Tools,128K", registry.ModeChat},
		{"qwen-2.5", "qwen-2.5-72b-instruct", "Alibaba", "Tools,32K", registry.ModeChat},

		// 边界: 名字含 "speech-to-text" 应该走 transcription 不是 TTS
		{"speech-to-text edge", "gpt-4o-speech-to-text", "OpenAI", "", registry.ModeChat},

		// ─── B+ 方案 commit B 新增: 国产/小众品牌覆盖 ──────────────────
		// 这 31 条对应 admin DB 已知误分类脏数据 (manual_override=false).

		// TTS — cosyvoice (阿里达摩院) / chattts / fish-speech / spark-tts / indextts
		{"cosyvoice-v1", "cosyvoice-v1", "aliyun-bailian", "", registry.ModeAudioSpeech},
		{"cosyvoice-v3-plus", "cosyvoice-v3-plus", "aliyun-bailian", "", registry.ModeAudioSpeech},
		{"ChatTTS", "2Noise/ChatTTS", "2Noise", "", registry.ModeAudioSpeech},
		{"fish-speech", "fishaudio/fish-speech-1.5", "FishAudio", "", registry.ModeAudioSpeech},
		{"spark-tts", "SparkAudio/Spark-TTS-0.5B", "SparkAudio", "", registry.ModeAudioSpeech},
		{"indextts", "IndexTeam/Index-TTS", "Index", "", registry.ModeAudioSpeech},

		// ASR — paraformer (阿里) / sensevoice / funasr / seed-asr
		{"paraformer-v2", "paraformer-v2", "aliyun-bailian", "", registry.ModeAudioTranscription},
		{"paraformer-realtime", "paraformer-realtime-v2", "aliyun-bailian", "", registry.ModeAudioTranscription},
		{"sensevoice", "FunAudioLLM/SenseVoiceSmall", "FunAudioLLM", "", registry.ModeAudioTranscription},
		{"funasr", "alibaba/funasr-paraformer", "Alibaba", "", registry.ModeAudioTranscription},
		{"seed-asr", "bytedance/seed-asr", "ByteDance", "", registry.ModeAudioTranscription},

		// 图像生成 — qwen-image / hidream / recraft / hunyuan-image / cogview / seedream
		{"qwen-image", "qwen-image", "aliyun-bailian", "", registry.ModeImageGeneration},
		{"qwen-image-edit", "qwen-image-edit", "aliyun-bailian", "", registry.ModeImageGeneration},
		{"qwen/qwen-image", "qwen/qwen-image", "Nvidia", "", registry.ModeImageGeneration},
		{"hidream", "hidream", "BAAI", "", registry.ModeImageGeneration},
		{"recraft-v3", "recraft/recraft-v3", "Recraft", "", registry.ModeImageGeneration},
		{"recraft-v4-pro", "recraft/recraft-v4-pro", "Recraft", "", registry.ModeImageGeneration},
		{"hunyuan-image", "hunyuan-image-3.0", "Tencent", "", registry.ModeImageGeneration},
		{"cogview", "ZhipuAI/cogview-3-plus", "ZhipuAI", "", registry.ModeImageGeneration},
		{"seedream", "bytedance/seedream-3.0", "ByteDance", "", registry.ModeImageGeneration},

		// 视频生成 — bytedance/seedance / hailuo / vidu / minimax-video / wanx
		{"seedance-2.0", "bytedance/seedance-2.0", "ByteDance", "", registry.ModeVideoGeneration},
		{"seedance-v1.5-pro", "bytedance/seedance-v1.5-pro", "ByteDance", "", registry.ModeVideoGeneration},
		{"seedance-lite-i2v", "bytedance/seedance-v1.0-lite-i2v", "ByteDance", "", registry.ModeVideoGeneration},
		{"hailuo", "minimax/hailuo-02", "MiniMax", "", registry.ModeVideoGeneration},
		{"vidu-2", "vidu-2.0", "Vidu", "", registry.ModeVideoGeneration},
		{"minimax-video", "minimax-video-01", "MiniMax", "", registry.ModeVideoGeneration},
		{"wanx-video", "wanx2.1-i2v-turbo", "aliyun", "", registry.ModeVideoGeneration},

		// embedding — m3e / qwen3-embedding / nomic-embed
		{"m3e-base", "m3e-base", "moka-ai", "", registry.ModeEmbedding},
		{"qwen3-embedding", "qwen3-embedding-0.6b", "alibaba", "", registry.ModeEmbedding},
		{"nomic-embed", "nomic-ai/nomic-embed-text-v1.5", "nomic", "", registry.ModeEmbedding},

		// ─── M2.5 rerank: 必须先于 embedding 启发式 (bge-reranker-v2-m3 含
		// "bge-" 又含 "rerank", 顺序错了会归到 embedding).
		{"bge-reranker-v2-m3", "bge-reranker-v2-m3", "BAAI", "", registry.ModeRerank},
		{"bge-reranker-base", "BAAI/bge-reranker-base", "BAAI", "", registry.ModeRerank},
		{"cohere rerank-v3.5", "cohere/rerank-v3.5", "Cohere", "", registry.ModeRerank},
		{"jina-reranker", "jina-reranker-v2-base-multilingual", "Jina", "", registry.ModeRerank},
		{"qwen3-reranker", "qwen3-reranker-4b", "Alibaba", "", registry.ModeRerank},
		{"Qwen3-Reranker", "Qwen/Qwen3-Reranker-0.6B", "Qwen", "", registry.ModeRerank},
		{"voyage-rerank", "voyage-rerank-2", "VoyageAI", "", registry.ModeRerank},
		{"mxbai-rerank", "mixedbread-ai/mxbai-rerank-large-v1", "Mixedbread", "", registry.ModeRerank},
		{"gte-rerank", "gte-rerank-v2", "Alibaba", "", registry.ModeRerank},
		{"nvidia-rerank", "nvidia/llama-nemotron-rerank-vl-1b-v2", "Nvidia", "", registry.ModeRerank},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferMode(tc.modelName, tc.vendor, tc.tags)
			if got != tc.want {
				t.Errorf("inferMode(%q, %q, %q) = %q, want %q",
					tc.modelName, tc.vendor, tc.tags, got, tc.want)
			}
		})
	}
}
