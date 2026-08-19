// Token 解析链（方案 D5）：cloud 模式请求 model-relay 的 bearer
// token 按 `--token` flag > BIUMIND_TOKEN env > [model-relay].virtual_key
// > OAuth store（keychain→file）顺序解析。前三级命中即返回、不碰
// store（CI 零行为变化）；OAuth 分支支持临期惰性刷新（Expired 带
// 5min 缓冲）+ 401 强刷（ForceRefresh），解析结果进程内缓存避免每
// 次请求都 spawn keychain 进程。

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// TokenSource 标识解析出的 token 来自哪一级，供 doctor / 401 重试
// 判断（只有 SourceStore 的 token 才走强刷）。
type TokenSource string

const (
	SourceNone   TokenSource = "none"
	SourceFlag   TokenSource = "flag"
	SourceEnv    TokenSource = "env"
	SourceConfig TokenSource = "config"
	SourceStore  TokenSource = "oauth-store"
)

// 报错文案与方案 D5-4 对齐；errors.Is 可判定，供 wiring 层决定 hint。
var (
	ErrNotLoggedIn  = errors.New("auth: not logged in — run 'biu auth login' (or set BIUMIND_TOKEN for CI)")
	ErrLoginExpired = errors.New("auth: login expired — run 'biu auth login'")
)

// ResolveOptions 是 TokenProvider 的全部输入。静态三级（flag/env/
// virtual key）由调用方读好后传入；OAuth 分支需要 Config（推导后的
// token 端点 + client_id）与 Store。
type ResolveOptions struct {
	FlagToken  string // --token flag
	EnvToken   string // BIUMIND_TOKEN
	VirtualKey string // [model-relay].virtual_key

	Config Config       // 推导/合并后的 OAuth 端点（refresh 用）
	Store  *Store       // nil → 首次用到时 Open("")
	HTTP   *http.Client // 可选，refresh 的 HTTP client
}

// TokenProvider 持有一次解析的结果并负责后续刷新。非并发安全部分
// 由内部 mutex 保护，可放心在多 goroutine 间共享。
type TokenProvider struct {
	opts ResolveOptions

	mu     sync.Mutex
	token  string
	source TokenSource
	store  *Store
}

func NewTokenProvider(opts ResolveOptions) *TokenProvider {
	return &TokenProvider{opts: opts}
}

// Token 返回当前可用的 bearer token。
func (p *TokenProvider) Token(ctx context.Context) (string, error) {
	// 静态三级命中即返回，不碰 OAuth store。
	if tok, src := p.static(); tok != "" {
		p.mu.Lock()
		p.token, p.source = tok, src
		p.mu.Unlock()
		return tok, nil
	}
	p.mu.Lock()
	if p.token != "" && p.source == SourceStore {
		defer p.mu.Unlock()
		return p.token, nil
	}
	p.mu.Unlock()
	return p.refreshLocked(ctx, false)
}

// ForceRefresh 是 401 路径：不管本地判断是否临期，都强制走一次
// 刷新（处理时钟偏移与服务端提前吊销），成功后更新进程内缓存。
// 只有 SourceStore 来源的 token 才有意义；静态来源直接返回缓存值。
func (p *TokenProvider) ForceRefresh(ctx context.Context) (string, error) {
	if p.Source() != SourceStore {
		return p.Token(ctx)
	}
	return p.refreshLocked(ctx, true)
}

// Source 返回最近一次成功解析的来源（未解析过时为 SourceNone）。
func (p *TokenProvider) Source() TokenSource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.source
}

// CachedToken 返回进程内缓存的 token（无 I/O）。供 401 重试
// transport 每次请求注入最新值。
func (p *TokenProvider) CachedToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

// Invalidate 清掉进程内缓存（REPL /login /logout 后调用）。
func (p *TokenProvider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.token, p.source = "", SourceNone
}

func (p *TokenProvider) static() (string, TokenSource) {
	switch {
	case p.opts.FlagToken != "":
		return p.opts.FlagToken, SourceFlag
	case p.opts.EnvToken != "":
		return p.opts.EnvToken, SourceEnv
	case p.opts.VirtualKey != "":
		return p.opts.VirtualKey, SourceConfig
	}
	return "", SourceNone
}

func (p *TokenProvider) getStore() (*Store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store != nil {
		return p.store, nil
	}
	if p.opts.Store != nil {
		p.store = p.opts.Store
		return p.store, nil
	}
	s, err := Open("")
	if err != nil {
		return nil, err
	}
	p.store = s
	return s, nil
}

// refreshLocked 读 store →（锁内 double-check）→ 必要时 refresh
// 并写回。force=true 跳过临期判断（401 强刷）。
func (p *TokenProvider) refreshLocked(ctx context.Context, force bool) (string, error) {
	store, err := p.getStore()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	prevToken := p.token
	p.mu.Unlock()

	t, err := store.Load()
	if err != nil {
		return "", err
	}
	if t.AccessToken == "" {
		return "", ErrNotLoggedIn
	}
	if !force && !t.Expired() {
		p.cache(t.AccessToken)
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		// 无 refresh_token：已真过期只能引导重登；未真过期（5min
		// 缓冲内）先用现有 token 顶着。
		if force || !tokenValidNow(t) {
			return "", ErrLoginExpired
		}
		p.cache(t.AccessToken)
		return t.AccessToken, nil
	}

	// 跨进程互斥 + double-check（方案 D6）。
	var out Tokens
	err = withRefreshLock(func() error {
		cur, lerr := store.Load()
		if lerr != nil {
			return lerr
		}
		if cur.AccessToken == "" {
			return ErrNotLoggedIn
		}
		switch {
		case !force && !cur.Expired():
			// 等锁期间别的进程已刷新好，直接用。
			out = cur
			return nil
		case force && cur.AccessToken != prevToken && !cur.Expired():
			// 401 强刷：store 里的 token 已经不是我们用的那张
			// （别的进程刷过了），直接换新。
			out = cur
			return nil
		}
		if cur.RefreshToken == "" {
			return ErrLoginExpired
		}
		next, rerr := p.login().Refresh(ctx, cur)
		if rerr != nil {
			return rerr
		}
		if serr := store.Save(next); serr != nil {
			return fmt.Errorf("oauth: save refreshed tokens: %w", serr)
		}
		out = next
		return nil
	})
	if err != nil {
		// 惰性刷新失败但 access 实际还没到期（缓冲期内、网络抖动）
		// → 先用现有 token，不挡请求。强刷/真过期才报错。
		if !force && tokenValidNow(t) && !errors.Is(err, ErrNotLoggedIn) {
			p.cache(t.AccessToken)
			return t.AccessToken, nil
		}
		if errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrLoginExpired) {
			return "", err
		}
		return "", fmt.Errorf("%w (%v)", ErrLoginExpired, err)
	}
	p.cache(out.AccessToken)
	return out.AccessToken, nil
}

func (p *TokenProvider) login() Login {
	return Login{Config: p.opts.Config, HTTP: p.opts.HTTP}
}

func (p *TokenProvider) cache(tok string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.token, p.source = tok, SourceStore
}

// tokenValidNow 是不带缓冲的真实过期判断（Expired 含 5min 提前量）。
func tokenValidNow(t Tokens) bool {
	return t.AccessToken != "" &&
		(t.ExpiresAt.IsZero() || time.Now().Before(t.ExpiresAt))
}
