// RFC 7009 token revocation：`biu auth logout` 删本地凭证前先把
// refresh token 吊销掉，避免泄漏的长期凭证继续可用。网络/服务端
// 失败由调用方降级为 warn —— 本地一定登出。

package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Revoke POSTs token（通常 refresh_token）到 cfg.RevokeURL。
// RevokeURL 为空时返回 error —— 调用方据此跳过上游吊销。
func Revoke(ctx context.Context, cfg Config, token string, hc *http.Client) error {
	if cfg.RevokeURL == "" {
		return fmt.Errorf("oauth: no revoke URL configured")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{}
	form.Set("token", token)
	form.Set("token_type_hint", "refresh_token")
	if cfg.ClientID != "" {
		form.Set("client_id", cfg.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RevokeURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("oauth revoke %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
