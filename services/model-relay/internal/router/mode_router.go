// v0.3 全模态网关 — ModeRouter 是按 model.mode 分发到对应 modality
// adaptor interface 的中心.
//
// 设计:
//   - 包装现有 Resolver (resolver.go), Resolver 不动 (它本身是
//     mode-agnostic 的解析器)
//   - 在 Resolver 输出之上做 type assertion: 按调用方期望的 modality 把
//     adaptor cast 成具体接口 (ChatAdaptor / SpeechAdaptor / ...)
//   - 不匹配时返回明确错误 (ErrModeMismatch / ErrModalityNotSupported)
//   - 灰度开关 (env MODEL_RELAY_NEW_ADAPTOR_INTERFACE) 让 chat 路径
//     在 M0.3 期间灰度切到新接口验证零回归; M2 末删除老路径
//
// 见 docs/BiuMind-Multimodal-Gateway-Design.md §3 / §4.

package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// 错误哨兵 — 调用方可用 errors.Is 区分:
//   - ErrModeMismatch:           model.Mode 与期望的不一致 (例如用 /v1/audio/speech
//                                调一个 mode='chat' 的 model)
//   - ErrModalityNotSupported:   provider 不支持该 modality (adaptor 没实现对应 interface)
var (
	ErrModeMismatch         = errors.New("model mode mismatch with requested endpoint")
	ErrModalityNotSupported = errors.New("provider adaptor does not support this modality")
)

// ModeRouter 把 (model code) 解析成 (resolved channel + 类型安全的 adaptor 接口).
type ModeRouter struct {
	Resolver *Resolver
	Adaptors *provider.Registry
}

func NewModeRouter(r *Resolver, adaptors *provider.Registry) *ModeRouter {
	if r == nil {
		panic("ModeRouter: Resolver required")
	}
	if adaptors == nil {
		panic("ModeRouter: Adaptors required")
	}
	return &ModeRouter{Resolver: r, Adaptors: adaptors}
}

// ResolveForChat — 解析模型走 chat path.
// 1. Resolver 拿 channel + creds + provider
// 2. 校验 model.Mode == "chat" (空值兼容老数据当作 chat)
// 3. 从 registry 拿 adaptor, type assert 成 ChatAdaptor
// 4. 任一步失败返回明确错误, 不继续
func (mr *ModeRouter) ResolveForChat(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.ChatAdaptor, error) {
	out, err := mr.Resolver.Resolve(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if out.Model.Mode != "" && out.Model.Mode != registry.ModeChat {
		return nil, nil, fmt.Errorf("%w: model %s mode=%s but chat endpoint requested",
			ErrModeMismatch, out.Model.Code, out.Model.Mode)
	}
	adp, ok := mr.lookupAdaptor(out.Provider)
	if !ok {
		return nil, nil, fmt.Errorf("%w: no adaptor for provider %s (protocol=%s)",
			ErrModalityNotSupported, out.Provider.Code, out.Provider.Protocol)
	}
	chatA, ok := adp.(provider.ChatAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement ChatAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, chatA, nil
}

// lookupAdaptor 按多重优先级查 adaptor:
//   1. provider.Code 精确匹配 (例: "openai", "dashscope-bailian")
//   2. provider.Protocol 名 (例: "openai_compat", "anthropic")
//   3. protocol="openai_compat" 时 fallback 到 "openai" (现实兼容: 大部分
//      OpenAI 兼容上游都用 openai adaptor)
// 与 health.probe.lookupAdaptor 保持一致, 见 probe.go:297.
func (mr *ModeRouter) lookupAdaptor(prov *registry.Provider) (provider.BaseAdaptor, bool) {
	if a, ok := mr.Adaptors.Get(prov.Code); ok {
		return a, true
	}
	if a, ok := mr.Adaptors.Get(string(prov.Protocol)); ok {
		return a, true
	}
	if prov.Protocol == registry.ProtocolOpenAICompat {
		if a, ok := mr.Adaptors.Get("openai"); ok {
			return a, true
		}
	}
	if prov.Protocol == registry.ProtocolAnthropic {
		if a, ok := mr.Adaptors.Get("anthropic"); ok {
			return a, true
		}
	}
	if prov.Protocol == registry.ProtocolDashScope {
		if a, ok := mr.Adaptors.Get("dashscope"); ok {
			return a, true
		}
	}
	return nil, false
}

// ResolveForEmbed — 解析模型走 embedding path. M2 启用.
func (mr *ModeRouter) ResolveForEmbed(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.EmbedAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeEmbedding)
	if err != nil {
		return nil, nil, err
	}
	embedA, ok := adp.(provider.EmbedAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement EmbedAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, embedA, nil
}

// ResolveForSpeech — TTS path. M1 启用.
func (mr *ModeRouter) ResolveForSpeech(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.SpeechAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeAudioSpeech)
	if err != nil {
		return nil, nil, err
	}
	speechA, ok := adp.(provider.SpeechAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement SpeechAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, speechA, nil
}

// ResolveForTranscribe — ASR path. M3 启用.
func (mr *ModeRouter) ResolveForTranscribe(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.TranscribeAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeAudioTranscription)
	if err != nil {
		return nil, nil, err
	}
	transA, ok := adp.(provider.TranscribeAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement TranscribeAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, transA, nil
}

// ResolveForImage — 同步 image generation path. M3 启用.
func (mr *ModeRouter) ResolveForImage(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.ImageAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeImageGeneration)
	if err != nil {
		return nil, nil, err
	}
	imageA, ok := adp.(provider.ImageAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement ImageAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, imageA, nil
}

// ResolveForVideo — 视频生成 path. M4 启用 (wanx-video / kling).
// 大部分 provider 走 async, caller 拿到 VideoAdaptor 后再用 type assertion
// 升级到 AsyncVideoAdaptor 走 submit+poll 循环.
func (mr *ModeRouter) ResolveForVideo(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.VideoAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeVideoGeneration)
	if err != nil {
		return nil, nil, err
	}
	videoA, ok := adp.(provider.VideoAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement VideoAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, videoA, nil
}

// ResolveForRerank — RAG 排序. M2 启用.
func (mr *ModeRouter) ResolveForRerank(ctx context.Context, in ResolveInput) (*ResolveOutput, provider.RerankAdaptor, error) {
	out, adp, err := mr.resolveAndLookup(ctx, in, registry.ModeRerank)
	if err != nil {
		return nil, nil, err
	}
	rerankA, ok := adp.(provider.RerankAdaptor)
	if !ok {
		return nil, nil, fmt.Errorf("%w: adaptor %s does not implement RerankAdaptor",
			ErrModalityNotSupported, adp.Name())
	}
	return out, rerankA, nil
}

// resolveAndLookup — 内部 helper: 调 Resolver 拿 ResolveOutput, 校验
// model.Mode 等于期望值, 然后从 registry 拿 adaptor (按 provider.Code 优先,
// fallback 到 protocol 名). 不做 type assertion — 留给 caller 各自做.
func (mr *ModeRouter) resolveAndLookup(
	ctx context.Context, in ResolveInput, expectedMode string,
) (*ResolveOutput, provider.BaseAdaptor, error) {
	out, err := mr.Resolver.Resolve(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if out.Model.Mode != expectedMode {
		return nil, nil, fmt.Errorf("%w: model %s mode=%s, endpoint expects %s",
			ErrModeMismatch, out.Model.Code, out.Model.Mode, expectedMode)
	}
	adp, ok := mr.lookupAdaptor(out.Provider)
	if !ok {
		return nil, nil, fmt.Errorf("%w: no adaptor for provider %s",
			ErrModalityNotSupported, out.Provider.Code)
	}
	return out, adp, nil
}
