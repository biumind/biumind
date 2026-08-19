package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// refreshTestServer 计数的 refresh endpoint：rt-1 换 at-new-N 且轮转
// refresh token（rt-2），第二次带 rt-1 来的请求会 400——用来证明
// 并发下 refresh 只发生一次。
func refreshTestServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "unknown grant", 400)
			return
		}
		if r.Form.Get("refresh_token") != "rt-1" {
			http.Error(w, "bad refresh", 400)
			return
		}
		n := hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fmt.Sprintf("at-new-%d", n),
			"refresh_token": "rt-2",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
}

func tempStore(t *testing.T, tok Tokens) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "" {
		if err := s.Save(tok); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func expiredTokens() Tokens {
	return Tokens{
		AccessToken: "at-old", RefreshToken: "rt-1", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(-time.Minute).UTC(),
	}
}

// useTempLock 把刷新锁文件指到 t.TempDir()，避免测试写真实
// ~/.biu/auth.lock。
func useTempLock(t *testing.T) {
	t.Helper()
	prev := refreshLockPath
	refreshLockPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "auth.lock"), nil
	}
	t.Cleanup(func() { refreshLockPath = prev })
}

// C2：解析链优先级 —— 静态三级命中即返回，不碰 store（store 为 nil
// 时若被碰会 Open("") 读到真实 keychain，测试直接失败兜底）。
func TestTokenProviderStaticPriority(t *testing.T) {
	store := tempStore(t, expiredTokens())
	cases := []struct {
		name string
		opts ResolveOptions
		want string
		src  TokenSource
	}{
		{"flag wins", ResolveOptions{FlagToken: "f", EnvToken: "e", VirtualKey: "v", Store: store}, "f", SourceFlag},
		{"env over config", ResolveOptions{EnvToken: "e", VirtualKey: "v", Store: store}, "e", SourceEnv},
		{"config over store", ResolveOptions{VirtualKey: "v", Store: store}, "v", SourceConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewTokenProvider(c.opts)
			got, err := p.Token(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want || p.Source() != c.src {
				t.Errorf("got (%q,%s) want (%q,%s)", got, p.Source(), c.want, c.src)
			}
		})
	}
}

// C2：store 为空 → ErrNotLoggedIn（文案含 biu auth login 引导）。
func TestTokenProviderNotLoggedIn(t *testing.T) {
	p := NewTokenProvider(ResolveOptions{Store: tempStore(t, Tokens{})})
	if _, err := p.Token(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
}

// C2：store 里 token 未过期（5min 缓冲外）→ 直接用，不 refresh。
func TestTokenProviderFreshTokenNoRefresh(t *testing.T) {
	var hits atomic.Int32
	srv := refreshTestServer(t, &hits)
	defer srv.Close()
	store := tempStore(t, Tokens{
		AccessToken: "at-good", RefreshToken: "rt-1",
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
	})
	p := NewTokenProvider(ResolveOptions{
		Store:  store,
		Config: Config{TokenURL: srv.URL, ClientID: "test"},
	})
	got, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "at-good" || hits.Load() != 0 {
		t.Errorf("got %q hits=%d; want at-good, no refresh", got, hits.Load())
	}
}

// C2：临期惰性刷新 —— 成功后写回 store 并更新缓存。
func TestTokenProviderLazyRefresh(t *testing.T) {
	useTempLock(t)
	var hits atomic.Int32
	srv := refreshTestServer(t, &hits)
	defer srv.Close()
	store := tempStore(t, expiredTokens())
	p := NewTokenProvider(ResolveOptions{
		Store:  store,
		Config: Config{TokenURL: srv.URL, ClientID: "test"},
	})
	got, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "at-new-1" || hits.Load() != 1 {
		t.Errorf("got %q hits=%d", got, hits.Load())
	}
	saved, _ := store.Load()
	if saved.AccessToken != "at-new-1" || saved.RefreshToken != "rt-2" {
		t.Errorf("store not updated: %+v", saved)
	}
	// 进程内缓存：第二次 Token() 不再读/刷。
	got, err = p.Token(context.Background())
	if err != nil || got != "at-new-1" || hits.Load() != 1 {
		t.Errorf("cached resolve: got=%q err=%v hits=%d", got, err, hits.Load())
	}
}

// C2：refresh 失败且 access 已真过期 → ErrLoginExpired。
func TestTokenProviderRefreshFailsExpired(t *testing.T) {
	useTempLock(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid_grant", 400)
	}))
	defer srv.Close()
	p := NewTokenProvider(ResolveOptions{
		Store:  tempStore(t, expiredTokens()),
		Config: Config{TokenURL: srv.URL, ClientID: "test"},
	})
	if _, err := p.Token(context.Background()); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("want ErrLoginExpired, got %v", err)
	}
}

// C2：refresh 失败但 access 仍在 5min 缓冲内（实际未过期）→ 网络
// 抖动不挡请求，先用现有 token。
func TestTokenProviderRefreshFailsButTokenStillValid(t *testing.T) {
	useTempLock(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily broken", 500)
	}))
	defer srv.Close()
	store := tempStore(t, Tokens{
		AccessToken: "at-soon", RefreshToken: "rt-1",
		ExpiresAt: time.Now().Add(2 * time.Minute).UTC(), // 缓冲内、实际未过期
	})
	p := NewTokenProvider(ResolveOptions{
		Store:  store,
		Config: Config{TokenURL: srv.URL, ClientID: "test"},
	})
	got, err := p.Token(context.Background())
	if err != nil || got != "at-soon" {
		t.Errorf("got (%q,%v); want at-soon,nil", got, err)
	}
}

// C3/D5-3：ForceRefresh 对本地未过期的 token 也强制刷一次（401 路径）。
func TestTokenProviderForceRefresh(t *testing.T) {
	useTempLock(t)
	var hits atomic.Int32
	srv := refreshTestServer(t, &hits)
	defer srv.Close()
	store := tempStore(t, Tokens{
		AccessToken: "at-good", RefreshToken: "rt-1",
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
	})
	p := NewTokenProvider(ResolveOptions{
		Store:  store,
		Config: Config{TokenURL: srv.URL, ClientID: "test"},
	})
	if _, err := p.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := p.ForceRefresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "at-new-1" || hits.Load() != 1 {
		t.Errorf("got %q hits=%d", got, hits.Load())
	}
	if p.CachedToken() != "at-new-1" {
		t.Errorf("cache not updated: %q", p.CachedToken())
	}
}

// C5（方案 D6）：并发解析同一 store 里的过期 token，refresh 只发生
// 一次（锁内 double-check 命中）。
func TestTokenProviderConcurrentRefreshOnce(t *testing.T) {
	useTempLock(t)
	var hits atomic.Int32
	srv := refreshTestServer(t, &hits)
	defer srv.Close()
	store := tempStore(t, expiredTokens())
	oc := Config{TokenURL: srv.URL, ClientID: "test"}

	// 多个 TokenProvider 实例共享同一 store，模拟多进程/多组件并发。
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := NewTokenProvider(ResolveOptions{Store: store, Config: oc})
			got, err := p.Token(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if got != "at-new-1" {
				errs <- fmt.Errorf("got %q", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if hits.Load() != 1 {
		t.Errorf("refresh happened %d times, want exactly 1", hits.Load())
	}
}
