// Reaper for refresh_tokens — background cleanup of revoked + absolute-expired
// rows. Runs every ReaperInterval (default 1h),deletes up to ReaperBatchLimit
// rows per tick to keep tx 短小 + vacuum 友好。
//
// 删除条件 (BiuMind-Identity-Session-Design §4.2):
//   - revoked_at < now() - 30d   reuse detection 保留窗口外, 物理回收
//   - absolute_expires_at < now() - 7d  绝对过期且过了 7 天保留观察期
//
// 单实例足够:DELETE...LIMIT 是幂等的,多副本并发跑只会某副本捞 0 行,
// 不会重复操作。

package token

import (
	"context"
	"log/slog"
	"time"
)

// ReaperConfig 调优参数;零值有合理默认。
type ReaperConfig struct {
	// Interval 两次 reap 之间的间隔。零值 = 1h。
	Interval time.Duration
	// RevokedRetention revoked_at 在多久前的行才删。零值 = 30 天。
	// 必须 ≥ rotation 期望的最大延迟 (Settings 设备列表显示 revoke 状态需要这条记录)。
	RevokedRetention time.Duration
	// AbsoluteExpiredRetention absolute_expires_at 在多久前的行才删。零值 = 7 天。
	AbsoluteExpiredRetention time.Duration
	// BatchLimit 单次 DELETE 上限。零值 = 500。
	BatchLimit int
}

func (c *ReaperConfig) applyDefaults() {
	if c.Interval <= 0 {
		c.Interval = 1 * time.Hour
	}
	if c.RevokedRetention <= 0 {
		c.RevokedRetention = 30 * 24 * time.Hour
	}
	if c.AbsoluteExpiredRetention <= 0 {
		c.AbsoluteExpiredRetention = 7 * 24 * time.Hour
	}
	if c.BatchLimit <= 0 {
		c.BatchLimit = 500
	}
}

// Reaper 抽象出 store 依赖,方便单测注入 fake。
type ReaperStore interface {
	ReapRefreshTokens(ctx context.Context, revokedRetention, absExpiredRetention time.Duration, batch int) (int64, error)
}

// RunReaper 阻塞 goroutine,直到 ctx.Done。每个 tick 调一次 ReapRefreshTokens,
// 失败仅日志。
func RunReaper(ctx context.Context, store ReaperStore, cfg ReaperConfig, logger *slog.Logger) {
	cfg.applyDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.ReapRefreshTokens(ctx,
				cfg.RevokedRetention, cfg.AbsoluteExpiredRetention, cfg.BatchLimit)
			if err != nil {
				logger.Warn("refresh token reaper", "err", err)
				continue
			}
			if n > 0 {
				logger.Info("refresh token reaper", "deleted", n)
			}
		}
	}
}
