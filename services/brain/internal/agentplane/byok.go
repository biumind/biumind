// BYOK provider resolution — 决策表:给定 (userID, providerID) 判断要不要
// 走 BYOK,并取回 (APIKey, BaseURL)。历史上由 agent_plane router
// (agent/task 投递前预解析) 与 chat runner (chat 模式进程内直连) 共用;
// 两条链都已下线(见下),当前仅测试引用。
//
// 单一决策表(避免 chat / agent / task 三处各走各的):
//
//   * providerID 空 / resolver nil → 不走 BYOK
//   * provider 查不到(删 / 跨用户)→ 不走 BYOK
//   * provider source==official(BiuMind Cloud,无独立 key)→ 不走 BYOK
//   * provider 未启用 → 不走 BYOK
//   * keyResolver nil / identity 无该 (user,provider) 有效 key → 不走 BYOK
//   * 否则 → BYOK,返回 (APIKey, BaseURL)。BaseURL 可能为空(用户没填),
//            调用方此时应保留自己的 endpoint。
//
// P3: brain 不再存储用户 key (key_vaults_encrypted 已删)。凭据元数据
// (enabled/source/base_url) 仍从 chat.providers 读 (resolver); 明文 key +
// endpoint 改从 identity 现取 (keyResolver)。
//
// P4: agent/task 投递链 (sealBYOK / EncBYOK / WorkPayload.BYOKKey) 已删 ——
// daemon 改用 WorkPayload.UserBearer (委托 user JWT) 打 model-relay, relay
// 按 claims.UserID 原生解析 BYOK。chat 路径的进程内 BYOK 直连也在 chat 去
// env 化时移除(chat 一律 PassThrough, BYOK 由 relay 按 identity protocol
// 选 adaptor 统一接住)。本函数 + Server.KeyResolver 暂保留,当前无生产
// 调用方。
//
// 任何错误都不抛(只 log warn),让上层用平台兜底。

package agentplane

import (
	"context"
	"errors"
	"log/slog"

	providerspkg "github.com/biumind/biumind/services/brain/internal/chat/providers"
	"github.com/google/uuid"
)

// BYOKResult 是 ResolveBYOKCreds 的结果。UseBYOK=false 时 APIKey/BaseURL
// 字段不要看,调用方应该 fall back 到平台默认。
type BYOKResult struct {
	UseBYOK bool
	APIKey  string
	BaseURL string // 可能为空 — 调用方此时保留自己的 endpoint
}

// BYOKKeyResolver 现取用户某 provider 的明文 key + endpoint (P3: 来自
// identity, brain 不再存). providerspkg.IdentityBYOKClient 天然满足此接口
// (Get 签名一致), 测试用 fake 注入. nil → ResolveBYOKCreds 不走 BYOK.
type BYOKKeyResolver interface {
	Get(ctx context.Context, userID uuid.UUID, provider string) (*providerspkg.IdentityBYOKKey, error)
}

// ProviderResolver 是 BYOK 决策对 providers store 的最小依赖,只要能按
// (userID, providerID slug) 返回 *Provider 即可。生产实现是
// *providerspkg.Store(它的 GetByProviderID 已经匹配此签名)。
type ProviderResolver interface {
	GetByProviderID(ctx context.Context, userID uuid.UUID, providerID string) (*providerspkg.Provider, error)
}

// ResolveBYOKCreds 是上面文档说的决策表。logger 可空(测试 / dev)。
//
// P3: 多了 keyResolver 形参 (identity 取 key)。fetch_mode 判断已删
// (chat.providers.fetch_mode 列已 drop)。
func ResolveBYOKCreds(
	ctx context.Context,
	resolver ProviderResolver,
	keyResolver BYOKKeyResolver,
	userID uuid.UUID,
	providerID string,
	logger *slog.Logger,
) BYOKResult {
	if providerID == "" || resolver == nil {
		return BYOKResult{}
	}
	p, err := resolver.GetByProviderID(ctx, userID, providerID)
	if err != nil {
		if logger != nil && !errors.Is(err, providerspkg.ErrNotFound) {
			logger.Warn("byok: providers lookup failed; falling back to platform key",
				"user_id", userID, "provider_id", providerID, "err", err)
		}
		return BYOKResult{}
	}
	if p == nil || p.Source == providerspkg.SourceOfficial {
		return BYOKResult{}
	}
	if !p.Enabled {
		if logger != nil {
			logger.Info("byok: provider disabled; falling back to platform key",
				"user_id", userID, "provider_id", providerID)
		}
		return BYOKResult{}
	}
	// P3: key 不再从 brain Provider 读, 改向 identity 现取。
	if keyResolver == nil {
		if logger != nil {
			logger.Info("byok: no identity key resolver; falling back to platform key",
				"user_id", userID, "provider_id", providerID)
		}
		return BYOKResult{}
	}
	key, kerr := keyResolver.Get(ctx, userID, providerID)
	if kerr != nil || key == nil || key.APIKey == "" {
		if logger != nil {
			logger.Info("byok: no valid identity key; falling back to platform key",
				"user_id", userID, "provider_id", providerID, "err", kerr)
		}
		return BYOKResult{}
	}
	out := BYOKResult{UseBYOK: true, APIKey: key.APIKey}
	// identity 的 base_url 与 model-relay 实际调用同源, 优先用它;
	// 回退 brain Provider.BaseURL (chat.providers.base_url 仍存)。
	if key.BaseURL != "" {
		out.BaseURL = key.BaseURL
	} else if p.BaseURL != nil && *p.BaseURL != "" {
		out.BaseURL = *p.BaseURL
	}
	if logger != nil {
		logger.Debug("byok: resolved",
			"user_id", userID, "provider_id", providerID,
			"has_base_url", out.BaseURL != "")
	}
	return out
}
