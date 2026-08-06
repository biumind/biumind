package agentplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	providerspkg "github.com/biumind/biumind/services/brain/internal/chat/providers"
)

// fakeBYOKResolver mirrors fakeProviderResolver but lives in the same
// package so we can re-use without pulling cross-test types. Returns
// provider metadata only (P3: key no longer on *Provider).
type fakeBYOKResolver struct {
	provider *providerspkg.Provider
	err      error
}

func (f *fakeBYOKResolver) GetByProviderID(
	_ context.Context, _ uuid.UUID, _ string,
) (*providerspkg.Provider, error) {
	return f.provider, f.err
}

// fakeKeyResolver stands in for providerspkg.IdentityBYOKClient — returns
// a plaintext key + endpoint fetched "from identity" (P3).
type fakeKeyResolver struct {
	key *providerspkg.IdentityBYOKKey
	err error
}

func (f *fakeKeyResolver) Get(_ context.Context, _ uuid.UUID, _ string,
) (*providerspkg.IdentityBYOKKey, error) {
	return f.key, f.err
}

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 空 providerID → 不查表,UseBYOK=false。
func TestResolveBYOKCreds_EmptyProviderID(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{}, &fakeKeyResolver{}, uuid.New(), "", nopLogger())
	if r.UseBYOK {
		t.Errorf("empty providerID should not BYOK")
	}
}

// nil resolver → UseBYOK=false。
func TestResolveBYOKCreds_NilResolver(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		nil, &fakeKeyResolver{}, uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("nil resolver should not BYOK")
	}
}

// not-found → 平台 fallback,不抛。
func TestResolveBYOKCreds_NotFound(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{err: providerspkg.ErrNotFound},
		&fakeKeyResolver{}, uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("not-found should not BYOK")
	}
}

// 其他 DB 错 → 平台 fallback + 应该有 warn(用 logger 拦截可验证;
// 这里只验语义,log 内容不强校)。
func TestResolveBYOKCreds_OtherErr(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{err: errors.New("db down")},
		&fakeKeyResolver{}, uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("DB err should not BYOK")
	}
}

// official source → 不 BYOK(BiuMind Cloud 走平台池)。
func TestResolveBYOKCreds_Official(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: providerspkg.SourceOfficial, Enabled: true,
		}},
		&fakeKeyResolver{}, uuid.New(), "biumind-official", nopLogger())
	if r.UseBYOK {
		t.Errorf("official should not BYOK")
	}
}

// disabled provider → fallback。
func TestResolveBYOKCreds_Disabled(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: false,
		}},
		&fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: "sk"}},
		uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("disabled provider should not BYOK")
	}
}

// nil keyResolver (dev / 未配 IDENTITY_URL) → fallback 到平台。
func TestResolveBYOKCreds_NilKeyResolver(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: true,
		}},
		nil, uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("nil keyResolver should not BYOK")
	}
}

// identity 查询出错 / 无 key → fallback。
func TestResolveBYOKCreds_NoIdentityKey(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: true,
		}},
		&fakeKeyResolver{err: providerspkg.ErrIdentityKeyNotFound},
		uuid.New(), "anthropic", nopLogger())
	if r.UseBYOK {
		t.Errorf("no identity key should not BYOK")
	}
}

// happy path: builtin + enabled + identity key + base_url → UseBYOK, 两者都返。
func TestResolveBYOKCreds_HappyPathFull(t *testing.T) {
	url := "https://api.byok.example/v1"
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: true,
		}},
		&fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{
			APIKey: "sk-byok-real", BaseURL: url,
		}},
		uuid.New(), "anthropic", nopLogger())
	if !r.UseBYOK {
		t.Fatalf("expect UseBYOK=true")
	}
	if r.APIKey != "sk-byok-real" {
		t.Errorf("APIKey=%q", r.APIKey)
	}
	if r.BaseURL != url {
		t.Errorf("BaseURL=%q", r.BaseURL)
	}
}

// happy path 但 identity 无 base_url → APIKey 返, BaseURL 空。
func TestResolveBYOKCreds_HappyPathNoBase(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: true,
		}},
		&fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: "sk-byok-real"}},
		uuid.New(), "anthropic", nopLogger())
	if !r.UseBYOK || r.APIKey != "sk-byok-real" {
		t.Errorf("expect BYOK+APIKey set; got %+v", r)
	}
	if r.BaseURL != "" {
		t.Errorf("BaseURL should be empty; got %q", r.BaseURL)
	}
}

// identity base_url 空 → 回退 brain Provider.BaseURL。
func TestResolveBYOKCreds_BaseURLFallbackToProvider(t *testing.T) {
	provURL := "https://from-brain-row.example/v1"
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{provider: &providerspkg.Provider{
			Source: "builtin", Enabled: true, BaseURL: &provURL,
		}},
		&fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: "sk"}},
		uuid.New(), "anthropic", nopLogger())
	if !r.UseBYOK {
		t.Fatalf("expect UseBYOK=true")
	}
	if r.BaseURL != provURL {
		t.Errorf("BaseURL should fall back to provider row; got %q", r.BaseURL)
	}
}

// nil logger 不 panic — agent/task router 的非生产路径 logger 可能 nil。
func TestResolveBYOKCreds_NilLoggerSafe(t *testing.T) {
	r := ResolveBYOKCreds(context.Background(),
		&fakeBYOKResolver{err: providerspkg.ErrNotFound},
		&fakeKeyResolver{}, uuid.New(), "anthropic", nil)
	if r.UseBYOK {
		t.Errorf("not-found should not BYOK")
	}
}
