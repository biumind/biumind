// Supervisor wires the auto-disable / auto-recover feedback loop.
//
// Two halves:
//
//   1. RecordFailure / RecordSuccess — called from the live request path.
//      A 5xx or 429 from upstream calls RecordFailure; a 200 calls
//      RecordSuccess. The repo layer (channels.RecordFailure /
//      RecordSuccess) handles the threshold logic — supervisor is just
//      the place that ties business path + probe path to a single
//      counter contract.
//
//   2. Background sweep — every SweepInterval (default 5 min) Supervisor
//      looks at auto_disabled channels older than Cooldown, runs a hello
//      probe, and on success flips them back to active. This is the
//      recovery path: an upstream that 503'd for 30 seconds is back
//      online by the next sweep.
//
// Cron design: a single ticker, in-process. No DB-level lock — for MVP
// we run one model-relay replica; if multi-replica becomes a thing,
// switch to advisory lock or pgx LISTEN-driven leader election. The
// SQL itself is idempotent (RecordSuccess on an already-active channel
// is a no-op write), so duplicate sweeps in a multi-replica future
// are at worst extra probe traffic.

package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// SupervisorConfig governs cadence and thresholds.
type SupervisorConfig struct {
	// FailThreshold is the consecutive-failure count that flips a channel
	// to auto_disabled. Default 5; aligns with new-api convention.
	FailThreshold int

	// SweepInterval is how often Supervisor scans auto_disabled rows.
	// Default 5 min.
	SweepInterval time.Duration

	// Cooldown is the minimum age of an auto_disabled row before we
	// re-probe. Default 1 min — stops a freshly-failed channel from
	// being probed within the same sweep that disabled it.
	//
	// NOTE: zero value is treated as "use default". Pass a tiny duration
	// (e.g. 1 * time.Microsecond) if you want effectively-no cooldown
	// (mostly useful for tests).
	Cooldown time.Duration

	// PerSweepLimit caps how many channels a single sweep probes. Tens
	// of probes per minute is fine; hundreds saturates upstream rate
	// limits. Default 20.
	PerSweepLimit int

	Logger *slog.Logger
}

func (c *SupervisorConfig) defaults() {
	if c.FailThreshold == 0 {
		c.FailThreshold = 5
	}
	if c.SweepInterval == 0 {
		c.SweepInterval = 5 * time.Minute
	}
	if c.Cooldown == 0 {
		c.Cooldown = 1 * time.Minute
	}
	if c.PerSweepLimit == 0 {
		c.PerSweepLimit = 20
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Supervisor coordinates RecordFailure/Success + sweep. Construct via
// NewSupervisor; Start launches the cron goroutine; Close stops it.
type Supervisor struct {
	probe *Probe
	store *registry.Store
	cfg   SupervisorConfig

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func NewSupervisor(probe *Probe, store *registry.Store, cfg SupervisorConfig) *Supervisor {
	if probe == nil {
		panic("health.NewSupervisor: probe required")
	}
	if store == nil {
		panic("health.NewSupervisor: store required")
	}
	cfg.defaults()
	return &Supervisor{
		probe: probe,
		store: store,
		cfg:   cfg,
		done:  make(chan struct{}),
	}
}

// Start launches the sweep goroutine. Safe to call once. Returns
// without blocking; sweeps run on cfg.SweepInterval.
func (s *Supervisor) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	go s.sweepLoop(ctx)
}

// Close stops the sweep loop and waits for the goroutine to exit.
func (s *Supervisor) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		<-s.done
	})
}

// FailureKind 是上游失败的分类（R4-B）。决定 auto-disable 阈值 + 恢复 cooldown。
type FailureKind int

const (
	// FailureTransient：5xx / 网络 / 读取错误 —— 连续 FailThreshold 次后
	// auto_disable，cooldown 指数退避（瞬态故障，会自愈）。
	FailureTransient FailureKind = iota
	// FailureRateLimit：429 —— 立即 auto_disable，cooldown = Retry-After
	// （上游指定）或默认短窗。不需要连续 5 次。
	FailureRateLimit
	// FailureAuth：401 / 403 —— 凭据失效，立即 auto_disable + 长 cooldown
	// （等人工换 key / 轮换；瞎重试只会继续 401）。
	FailureAuth
	// FailureBilling：402 —— 余额/计费问题，立即 auto_disable + 长 cooldown。
	FailureBilling
)

// cooldown 计算常量。
const (
	backoffBase         = 30 * time.Second // 指数退避基数（第一档）
	backoffCap          = 30 * time.Minute // 退避封顶
	rateLimitFallback   = 30 * time.Second // 429 无 Retry-After 时的默认 cooldown
	authBillingCooldown = 30 * time.Minute // 401/402/403 的长 cooldown（人工介入级）
)

// expBackoff 按超过阈值的失败档数算退避：base × 2^n，封顶 backoffCap。
func expBackoff(level int) time.Duration {
	if level < 0 {
		level = 0
	}
	if level > 10 { // 防左移溢出（2^10 already > cap）
		level = 10
	}
	d := backoffBase << uint(level)
	if d <= 0 || d > backoffCap {
		return backoffCap
	}
	return d
}

// ClassifyUpstreamStatus 把上游 HTTP status 映射成 FailureKind。0 / 其他
// （网络错误无 status）→ Transient。
func ClassifyUpstreamStatus(status int) FailureKind {
	switch status {
	case 429:
		return FailureRateLimit
	case 401, 403:
		return FailureAuth
	case 402:
		return FailureBilling
	default:
		return FailureTransient
	}
}

// RecordFailure 是 RecordFailureKind 的瞬态默认封装（向后兼容）。
func (s *Supervisor) RecordFailure(ctx context.Context, channelID uuid.UUID, cause error) (int, registry.EntityStatus, error) {
	return s.RecordFailureKind(ctx, channelID, cause, FailureTransient, 0)
}

// RecordFailureKind 按错误类型记失败 + 设恢复 cooldown（R4-B）。429/auth/billing
// 立即 auto_disable（阈值=1）；瞬态走 FailThreshold。channel 翻到 auto_disabled
// 时按 kind 计算并 SetCooldownUntil：retryAfter（429，>0 优先）/ 长冷却（auth/
// billing）/ 指数退避（瞬态）。errMsg 记上 channel 行供诊断。
func (s *Supervisor) RecordFailureKind(ctx context.Context, channelID uuid.UUID, cause error, kind FailureKind, retryAfter time.Duration) (int, registry.EntityStatus, error) {
	threshold := s.cfg.FailThreshold
	if kind == FailureRateLimit || kind == FailureAuth || kind == FailureBilling {
		threshold = 1 // 立即 auto_disable，不等连续 5 次
	}
	msg := truncate(cause.Error(), 500)
	fc, status, err := s.store.Channels.RecordFailure(ctx, channelID, msg, threshold)
	if err != nil {
		s.cfg.Logger.Warn("supervisor: record_failure failed",
			"channel", channelID, "err", err.Error())
		return 0, "", err
	}
	if status == registry.StatusAutoDisabled {
		until := s.cooldownDeadline(kind, retryAfter, fc)
		if err := s.store.Channels.SetCooldownUntil(ctx, channelID, until); err != nil {
			s.cfg.Logger.Warn("supervisor: set_cooldown_until failed",
				"channel", channelID, "err", err.Error())
		}
		s.cfg.Logger.Warn("supervisor: channel auto-disabled",
			"channel", channelID, "kind", int(kind),
			"failure_count", fc, "cooldown_until", until.Format(time.RFC3339))
	}
	return fc, status, nil
}

// cooldownDeadline 按 kind 算恢复时刻。
func (s *Supervisor) cooldownDeadline(kind FailureKind, retryAfter time.Duration, failureCount int) time.Time {
	now := time.Now()
	switch kind {
	case FailureRateLimit:
		if retryAfter > 0 {
			return now.Add(retryAfter)
		}
		return now.Add(rateLimitFallback)
	case FailureAuth, FailureBilling:
		return now.Add(authBillingCooldown)
	default: // FailureTransient：从越过阈值起算指数退避档
		return now.Add(expBackoff(failureCount - s.cfg.FailThreshold))
	}
}

// RecordSuccess wires a request-path success into the channel's stats.
// latencyMs feeds the EWMA used by future lowest_latency strategy.
func (s *Supervisor) RecordSuccess(ctx context.Context, channelID uuid.UUID, latencyMs int) error {
	if err := s.store.Channels.RecordSuccess(ctx, channelID, latencyMs); err != nil {
		s.cfg.Logger.Warn("supervisor: record_success failed",
			"channel", channelID, "err", err.Error())
		return err
	}
	return nil
}

// ─── Sweep loop ───────────────────────────────────────────────────

func (s *Supervisor) sweepLoop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()

	// Run one sweep immediately on start so a freshly-restarted process
	// doesn't wait 5 min to recover channels.
	s.sweepOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			s.cfg.Logger.Info("supervisor: sweep loop stopped")
			return
		case <-ticker.C:
			s.sweepOnce(ctx)
		}
	}
}

// sweepOnce picks up to PerSweepLimit auto_disabled channels older
// than Cooldown, runs hello probes, and recovers the ones that respond
// healthy. Reported errors don't abort the sweep — one bad channel
// shouldn't prevent the others from being checked.
func (s *Supervisor) sweepOnce(ctx context.Context) {
	candidates, err := s.store.Channels.ListAutoDisabled(ctx, s.cfg.Cooldown)
	if err != nil {
		s.cfg.Logger.Warn("supervisor: list auto_disabled failed",
			"err", err.Error())
		return
	}
	s.cfg.Logger.Debug("supervisor: sweep tick",
		"candidates", len(candidates), "cooldown", s.cfg.Cooldown.String())
	if len(candidates) == 0 {
		return
	}
	if len(candidates) > s.cfg.PerSweepLimit {
		candidates = candidates[:s.cfg.PerSweepLimit]
	}
	s.cfg.Logger.Info("supervisor: probing auto_disabled channels",
		"count", len(candidates))

	for _, ch := range candidates {
		if ctx.Err() != nil {
			return
		}
		s.probeOne(ctx, ch)
	}
}

// probeOne runs a single channel probe and updates state. Logged but
// non-fatal on any error.
func (s *Supervisor) probeOne(ctx context.Context, ch registry.Channel) {
	res := s.probe.RunChannel(ctx, ch.ID)
	if res.OK {
		if err := s.store.Channels.RecordSuccess(ctx, ch.ID, res.LatencyMs); err != nil {
			s.cfg.Logger.Warn("supervisor: recovery RecordSuccess failed",
				"channel", ch.ID, "err", err.Error())
			return
		}
		s.cfg.Logger.Info("supervisor: channel recovered",
			"channel", ch.ID, "latency_ms", res.LatencyMs)
		return
	}
	// Probe failed — re-stamp last_error so the cooldown shifts forward
	// (next sweep won't immediately pick this row again). Don't change
	// failure_count, that's only the request-path's job.
	const q = `
		UPDATE model_relay.channels
		   SET last_error_at = now(),
		       last_error    = $1,
		       last_test_at  = now(),
		       updated_at    = now()
		 WHERE id = $2 AND status = 'auto_disabled'
	`
	_, err := s.store.Pool.Exec(ctx, q, truncate(res.Error, 500), ch.ID)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.cfg.Logger.Warn("supervisor: stamp probe failure failed",
			"channel", ch.ID, "err", err.Error())
	}
	s.cfg.Logger.Debug("supervisor: probe still failing",
		"channel", ch.ID, "error_code", res.ErrorCode, "error", res.Error)
}
