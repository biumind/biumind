// OAuthRefreshTransport — cloud 模式 token 来自 OAuth store 时包在
// provider 的 http.Client 上的一层 RoundTripper（方案 D5-3）：
//
//   - 每次请求用 TokenFn() 注入最新缓存的 bearer（刷新后旧请求链
//     自动换新车票）；
//   - 收到 401 时调 RefreshFn 强刷一次、重放原请求一次（处理时钟
//     偏移与服务端提前吊销）；仍 401 或刷新失败则把原始 401 / 刷新
//     错误透给上层，由上层渲染 "run 'biu auth login'" 引导。
//
// token 来自 flag/env/config 时不挂这层（静态 token 没有刷新语义）。

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// OAuthRefreshTransport implements http.RoundTripper.
type OAuthRefreshTransport struct {
	// Base 是底层 transport；nil → http.DefaultTransport。
	Base http.RoundTripper

	// TokenFn 返回当前应注入的 bearer token（要求无 I/O，读进程内
	// 缓存）。空串 → 不改 Authorization header。
	TokenFn func() string

	// RefreshFn 在 401 时被调用一次，返回刷新后的 token。失败则
	// RoundTrip 直接返回该错误（调用方会看到 "auth: login expired"
	// 一类的引导文案）。
	RefreshFn func(ctx context.Context) (string, error)
}

func (t *OAuthRefreshTransport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *OAuthRefreshTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.TokenFn != nil {
		if tok := t.TokenFn(); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := t.base().RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || t.RefreshFn == nil {
		return resp, err
	}
	// body 不可重放（没有 GetBody）→ 不重试，原样上交 401。
	if req.GetBody == nil {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()

	newTok, rerr := t.RefreshFn(req.Context())
	if rerr != nil {
		return nil, fmt.Errorf("auth refresh after 401: %w", rerr)
	}
	body, berr := req.GetBody()
	if berr != nil {
		return nil, berr
	}
	retry := req.Clone(req.Context())
	retry.Body = body
	retry.Header.Set("Authorization", "Bearer "+newTok)
	return t.base().RoundTrip(retry)
}
