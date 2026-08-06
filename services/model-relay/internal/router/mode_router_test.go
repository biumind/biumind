// ModeRouter 集成测试 — 验证 v0.3 type assertion 分发路径正确, 以及
// mode mismatch 错误能被识别. 跟 resolver_test.go 共用 fixture, 只在
// DATABASE_URL 设置时跑.
//
// M0.3 验收要点:
//   - chat model 通过 ResolveForChat 走通, 拿到 ChatAdaptor 能调
//     TranslateRequest (跟老路径行为等价)
//   - 同一个 chat model 通过 ResolveForSpeech 必须失败 (ErrModeMismatch),
//     防止有人把 chat model 误绑到 audio endpoint
//   - 不存在的 provider adaptor 必须报 ErrModalityNotSupported

package router

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

func TestModeRouter_ChatHappyPath(t *testing.T) {
	fx := newFixture(t)

	adaptors := provider.NewRegistry()
	// fixture provider.Code 是 "p_resolver_<ns>", protocol = openai_compat.
	// lookupAdaptor 走 fallback chain: code → protocol → "openai" alias.
	// 我们注册名为 "openai" 的 adaptor, 触发第三层 fallback 命中.
	adaptors.Register(openai.New())

	mr := NewModeRouter(fx.resolver, adaptors)
	out, chatA, err := mr.ResolveForChat(context.Background(), ResolveInput{
		ModelCode: fx.model.Code, UserID: uuid.New(),
		UserPlan: fx.model.MinPlan, RequestID: "test-1",
	})
	if err != nil {
		t.Fatalf("ResolveForChat: %v", err)
	}
	if out == nil || chatA == nil {
		t.Fatal("nil result")
	}
	caps := chatA.Capabilities()
	if len(caps) == 0 || caps[0] != "chat" {
		t.Errorf("openai adaptor caps: %v, want [chat...]", caps)
	}
	// 验证 chat path 仍然能走 — 调老接口的 TranslateRequest (ChatAdaptor
	// 嵌入老 Adaptor, 同一个方法).
	creds := &provider.Credentials{APIKey: "sk-test", BaseURL: "https://api.example.com"}
	httpReq, err := chatA.TranslateRequest(context.Background(),
		&provider.Request{Model: out.UpstreamModel, Messages: []provider.Message{
			{Role: "user", Content: provider.JSONString("hi")},
		}}, creds)
	if err != nil {
		t.Fatalf("TranslateRequest via ChatAdaptor: %v", err)
	}
	if httpReq.URL.Path != "/v1/chat/completions" {
		t.Errorf("path: %s", httpReq.URL.Path)
	}
}

func TestModeRouter_ChatModelRejectedByOtherModalities(t *testing.T) {
	fx := newFixture(t)

	adaptors := provider.NewRegistry()
	adaptors.Register(openai.New())

	mr := NewModeRouter(fx.resolver, adaptors)

	// 用 chat model 调 ResolveForSpeech → 必须 ErrModeMismatch
	_, _, err := mr.ResolveForSpeech(context.Background(), ResolveInput{
		ModelCode: fx.model.Code, UserID: uuid.New(),
		UserPlan: fx.model.MinPlan, RequestID: "test-2",
	})
	if !errors.Is(err, ErrModeMismatch) {
		t.Errorf("ResolveForSpeech on chat model: got %v, want ErrModeMismatch", err)
	}

	// 用 chat model 调 ResolveForEmbed → ErrModeMismatch
	_, _, err = mr.ResolveForEmbed(context.Background(), ResolveInput{
		ModelCode: fx.model.Code, UserID: uuid.New(),
		UserPlan: fx.model.MinPlan, RequestID: "test-3",
	})
	if !errors.Is(err, ErrModeMismatch) {
		t.Errorf("ResolveForEmbed on chat model: got %v, want ErrModeMismatch", err)
	}

	// 用 chat model 调 ResolveForRerank → ErrModeMismatch
	_, _, err = mr.ResolveForRerank(context.Background(), ResolveInput{
		ModelCode: fx.model.Code, UserID: uuid.New(),
		UserPlan: fx.model.MinPlan, RequestID: "test-4",
	})
	if !errors.Is(err, ErrModeMismatch) {
		t.Errorf("ResolveForRerank on chat model: got %v, want ErrModeMismatch", err)
	}
}

func TestModeRouter_NoAdaptorForProvider(t *testing.T) {
	fx := newFixture(t)

	// 空 registry — 没注册任何 adaptor
	adaptors := provider.NewRegistry()
	mr := NewModeRouter(fx.resolver, adaptors)

	_, _, err := mr.ResolveForChat(context.Background(), ResolveInput{
		ModelCode: fx.model.Code, UserID: uuid.New(),
		UserPlan: fx.model.MinPlan, RequestID: "test-5",
	})
	if !errors.Is(err, ErrModalityNotSupported) {
		t.Errorf("ResolveForChat with empty registry: got %v, want ErrModalityNotSupported", err)
	}
}
