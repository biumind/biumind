package api

// mp_common.go — 9 端小程序登录的共享底座.
//
// 所有 *_mp.go 平台 handler 调 resolveOrCreateUserByProvider 完成
// "外部账号 → BiuMind user" 的标准合并路径; 平台差异 (各自的 code →
// (openid, unionid) 调用) 留在每个平台文件里.
//
// 路径文档:
//   - 命中 (provider, provider_user_id) → 直接复用 user
//   - 未命中但 unionid 匹配微信生态既有 row → 合并到既有 user, 新建 row
//   - 都没命中 → 新建 user (placeholder email) + 新建 row
//
// 跨厂商合并 (微信 ↔ 支付宝) 不在这里处理 — 走 me 页面手动绑定.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/biumind/biumind/services/identity/internal/store"
)

// mpLoginRequest — 所有平台小程序登录请求体的公共字段.
// 平台特殊字段 (如微信的 encrypted_data / iv) 走 raw map 里的额外 key,
// 由具体 handler 解析.
type mpLoginRequest struct {
	Code           string `json:"code"`
	InstallationID string `json:"installation_id"`
	DeviceName     string `json:"device_name"`
}

// providerProfile — 平台 code 换出来的最终身份信息. 各平台 handler
// 自己实现"调外部 API" 部分, 把结果填到这里再交给共享路径.
type providerProfile struct {
	Provider       string  // store.ProviderWechatMP 等
	ProviderUserID string  // openid
	UnionID        *string // 仅微信生态非空
	RawProfile     []byte  // jsonb, 至少含 nickname / avatar_url 给 me 页面用
}

// resolveOrCreateUserByProvider 三段式登录路径:
//
//  1. 已绑过 — 直接拿 user
//  2. 微信生态 unionid 命中 — 合并到既有 user, 追加一条 provider 记录
//  3. 全新 — 创建 placeholder user + 绑定记录
//
// 失败统一返 error; 调用方负责包成 HTTP 响应.
func (s *Server) resolveOrCreateUserByProvider(
	ctx context.Context, prof providerProfile,
) (*store.User, *store.IdentityProvider, error) {
	// (1) 已绑
	binding, err := s.Store.FindIdentityProvider(ctx, prof.Provider, prof.ProviderUserID)
	if err == nil {
		u, err := s.Store.GetUserByID(ctx, binding.UserID)
		if err != nil {
			return nil, nil, fmt.Errorf("get user by binding: %w", err)
		}
		_ = s.Store.TouchIdentityProviderLogin(ctx, binding.ID)
		return u, binding, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, fmt.Errorf("find binding: %w", err)
	}

	// (2) 微信生态 unionid 合并
	if prof.UnionID != nil && *prof.UnionID != "" && store.IsWechatEcosystem(prof.Provider) {
		existing, err := s.Store.FindIdentityProviderByUnionID(ctx, *prof.UnionID)
		if err == nil {
			u, err := s.Store.GetUserByID(ctx, existing.UserID)
			if err != nil {
				return nil, nil, fmt.Errorf("get user by unionid: %w", err)
			}
			binding, err := s.Store.CreateIdentityProvider(
				ctx, u.ID, prof.Provider, prof.ProviderUserID, prof.UnionID, prof.RawProfile,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("create binding (unionid merge): %w", err)
			}
			return u, binding, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, nil, fmt.Errorf("find by unionid: %w", err)
		}
	}

	// (3) 全新用户. email 留 placeholder (NOT NULL 约束) — 用户后续可在
	// me 页面绑定真邮箱; 占位邮箱不发任何邮件.
	placeholderEmail := mpPlaceholderEmail(prof.Provider, prof.ProviderUserID)
	displayName := mpDisplayNameFromProfile(prof.RawProfile, prof.Provider)
	u, err := s.Store.CreateUser(ctx, placeholderEmail, "", displayName)
	if err != nil {
		return nil, nil, fmt.Errorf("create user: %w", err)
	}
	// 第三方登录视为已验证 — 不走邮件验证流程.
	if err := s.Store.MarkUserEmailVerified(ctx, u.ID); err != nil {
		return nil, nil, fmt.Errorf("mark verified: %w", err)
	}
	binding, err = s.Store.CreateIdentityProvider(
		ctx, u.ID, prof.Provider, prof.ProviderUserID, prof.UnionID, prof.RawProfile,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create binding (new user): %w", err)
	}
	// 重新拉一次拿到 email_verified_at 已写入的最新状态
	u, err = s.Store.GetUserByID(ctx, u.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("reload user: %w", err)
	}
	return u, binding, nil
}

// mpPlaceholderEmail 给第三方登录新用户生成的占位邮箱. 域名固定
// no-mail.biumind.local 表示"永远不会发邮件到这", 不和真实主体域冲突.
// provider_user_id 直接拼 — 微信 openid 是 28 位字符, 支付宝 user_id 16 位,
// 都不会撞 email 长度上限.
func mpPlaceholderEmail(provider, providerUserID string) string {
	short := strings.ToLower(strings.TrimSuffix(provider, "_mp"))
	return fmt.Sprintf("%s_%s@no-mail.biumind.local", short, providerUserID)
}

// mpDisplayNameFromProfile 从 raw_profile_json 抽 nickname; 抽不到给个默认.
// 各平台 nickname 字段差异由 handler 在写 RawProfile 前归一成 "nickname" key.
func mpDisplayNameFromProfile(raw []byte, provider string) string {
	if len(raw) == 0 {
		return defaultDisplayName(provider)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return defaultDisplayName(provider)
	}
	if v, ok := m["nickname"].(string); ok && v != "" {
		return v
	}
	return defaultDisplayName(provider)
}

func defaultDisplayName(provider string) string {
	switch provider {
	case store.ProviderWechatMP, store.ProviderWechatOA, store.ProviderWechatOpen:
		return "微信用户"
	case store.ProviderAlipayMP:
		return "支付宝用户"
	case store.ProviderToutiaoMP:
		return "抖音用户"
	case store.ProviderBaiduMP:
		return "百度用户"
	case store.ProviderQQMP:
		return "QQ 用户"
	case store.ProviderKuaishouMP:
		return "快手用户"
	case store.ProviderJDMP:
		return "京东用户"
	case store.ProviderLarkMP:
		return "飞书用户"
	}
	return "BiuMind 用户"
}

// mpRespondWithTokens 复用 issueTokensAndRespond 的 token 颁发路径,
// 不破坏既有 device installation 不变量.
func (s *Server) mpRespondWithTokens(
	w http.ResponseWriter, ctx context.Context,
	u *store.User, deviceName, installationID string,
) {
	if deviceName == "" {
		deviceName = "miniapp"
	}
	s.issueTokensAndRespond(w, ctx, u, deviceName, installationID)
}

