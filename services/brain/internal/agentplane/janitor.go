// Janitor（S3-7）—— 把 last_seen_at 过老的 environment 标 offline。
//
// 不需要回收 in-flight work —— 那是 JetStream 的事（AckWait + MaxDeliver
// 让死 worker 持有的消息超时后自动 redeliver 给同 durable 的下个 consumer）。
// Janitor 只管 environment **状态展示**（dashboard 上"online"/"offline"）。
//
// 阈值：
//   - heartbeatTTL = 90s：超过这么久没 heartbeat 标 offline。worker 默认每
//     30s 发一次，留 3x 容忍网络抖动；超过基本是 worker 已死/重启
//   - sweepInterval = 30s：扫描间隔。比 TTL 短，状态切换最多滞后 30s
//
// 实现：单条 SQL 全表扫，没必要做 LIMIT 分批 —— online environments 数量
// 通常 ≤ 10⁴ 量级（biu_daemon 个数 ≤ 用户数；runtime 副本数 ≤ K8s pod
// 数）；扫一次毫秒级。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
)

// 默认参数。导出为变量让测试能临时缩短。
var (
	// R8：sweepInterval 15s + heartbeatTTL 45s。worker 心跳周期 15s（见
	// agentplane.WorkerConfig.defaults），45s = 3× 容忍网络抖动。最坏检测
	// 延迟 = TTL + sweepInterval ≈ 60s（此前 90+30 ≈ 120s）—— agent 执行设备
	// 崩溃后客户端更快收到失败帧停止 spinner。
	JanitorSweepInterval = 15 * time.Second
	JanitorHeartbeatTTL  = 45 * time.Second
	// JanitorOfflineGCAge: offline 行超过这么久就物理删,防止 worker 的
	// re-register 风暴堆积无限多 stale 行(实测一次 brain 抖动可在 1 小时
	// 内堆 396 万行,把 listEnvironments 全表排序炸到 30s timeout)。
	// 7 天比 dashboard 历史回看需要长得多,但比 PG 自动 vacuum 周期短。
	JanitorOfflineGCAge = 7 * 24 * time.Hour
)

// Janitor 是后台 sweep worker。Run(ctx) 阻塞直到 ctx 取消。
type Janitor struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	// queue 用于在把 session 标 failed 时,向 biu.session.<id>.out 推一帧
	// SDKResultError —— 否则正在 /stream 的客户端收不到任何终止信号,spinner
	// 永远转(R8 修复)。可空:dev / 无 NATS 时退化为只改 DB 状态(同 R8 前
	// 行为),不阻塞 janitor。
	queue *Queue

	// stats 累计计数（debug / metrics 用）。读取时非原子但只有 logger 用，
	// 不需要加锁。
	stats JanitorStats
}

// JanitorStats 是累计统计 —— 启动后到当前的 tick 数 + 标记数。
type JanitorStats struct {
	Sweeps         int64 // 总扫描次数
	OfflineMarked  int64 // 累计标 offline 的行数
	OfflineDeleted int64 // 累计 GC 删除的 offline 行数
	OrphanFailed   int64 // 累计因 environment offline 而被标 failed 的孤儿 session
	Errors         int64 // 累计 SQL 错误次数
}

// NewJanitor 构造一个未启动的 Janitor。logger 可空（用 slog.Default）。
// queue 可空（无 NATS 的 dev / 测试）—— 为空时只改 DB 状态,不推失败帧。
func NewJanitor(pool *pgxpool.Pool, logger *slog.Logger, queue *Queue) *Janitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Janitor{pool: pool, logger: logger, queue: queue}
}

// Stats 返回累计计数快照。给 /metrics 或 admin endpoint 用。
func (j *Janitor) Stats() JanitorStats { return j.stats }

// Run 是主循环。阻塞，直到 ctx 取消。RunOnce 暴露给测试 + 启动时立刻
// 跑一次（避免等满 sweepInterval）。
func (j *Janitor) Run(ctx context.Context) {
	j.logger.Info("agentplane janitor started",
		"sweep_interval", JanitorSweepInterval,
		"heartbeat_ttl", JanitorHeartbeatTTL)
	// 启动时跑一次，让重启后立刻把死 environment 标对，避免等 30s
	j.RunOnce(ctx)
	t := time.NewTicker(JanitorSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			j.logger.Info("agentplane janitor stopped",
				"sweeps", j.stats.Sweeps,
				"offline_marked", j.stats.OfflineMarked)
			return
		case <-t.C:
			j.RunOnce(ctx)
		}
	}
}

// RunOnce 执行一次扫描 + GC。返回标记 offline 的行数;error 不向上传 ——
// 一次失败不应该停掉整个 worker。调用方(包括 Run loop)当 fire-and-forget。
//
// Phase 1: 扫 state='online' AND last_seen_at < cutoff —— online 之外的
//
//	状态(offline/draining)不动。draining 是 graceful shutdown 状态,
//	让它自己 finish。
//
// Phase 2: 删 state='offline' AND last_seen_at < gcCutoff —— 物理回收防
//
//	止 stale 行无限堆积撑爆 listEnvironments。
func (j *Janitor) RunOnce(ctx context.Context) int64 {
	j.stats.Sweeps++
	cutoff := time.Now().Add(-JanitorHeartbeatTTL)
	const markQ = `
		UPDATE agent_environments
		   SET state = 'offline'
		 WHERE state = 'online'
		   AND last_seen_at < $1
	`
	tag, err := j.pool.Exec(ctx, markQ, cutoff)
	if err != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor sweep failed", "err", err)
		return 0
	}
	n := tag.RowsAffected()
	if n > 0 {
		j.stats.OfflineMarked += n
		j.logger.Info("agentplane janitor marked offline",
			"count", n,
			"cutoff", cutoff.Format(time.RFC3339))
	}

	// Phase 1.5: 孤儿 session 收尾 — environment offline 后,把它名下还在
	// active 的 chat session 标 failed。daemon 崩溃时 in-flight 的对话不
	// 会再有人响应,前端 spinner 永远转(实测 daemon 在 Bash 执行后突然
	// 退出,session 卡住)。这里兜底,让前端通过 ChatRunner 的 session 状
	// 态轮询/finalize 路径触发 spinner 结束 + 错误展示。
	// RETURNING session_id：active/paused → failed 只命中一次（下次 sweep
	// state 已是 failed,不再匹配）—— 天然幂等,保证失败帧只推一次。
	const orphanQ = `
		UPDATE agent_sessions
		   SET state = 'failed', updated_at = NOW()
		 WHERE state IN ('active', 'paused')
		   AND environment_id IN (
		     SELECT environment_id FROM agent_environments
		      WHERE state = 'offline' AND last_seen_at < $1
		   )
		RETURNING session_id
	`
	if ids, err := j.failSessions(ctx, orphanQ, cutoff); err != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor orphan-session sweep failed", "err", err)
	} else if len(ids) > 0 {
		j.stats.OrphanFailed += int64(len(ids))
		j.logger.Warn("agentplane janitor: marked orphan sessions failed",
			"count", len(ids),
			"reason", "environment offline (heartbeat TTL exceeded)")
		j.notifyFailed(ctx, ids, "执行设备已离线，任务已中断，请重试")
	}

	// R6.1: GC 过期 / 已消费的配对（agent_pairings 短命，5min TTL）。控住
	// UNAUTH pair/request 的 DB 增长面。
	if pTag, pErr := j.pool.Exec(ctx,
		`DELETE FROM agent_pairings WHERE expires_at < now() OR status = 'consumed'`); pErr != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor: pairing GC failed", "err", pErr)
	} else if pN := pTag.RowsAffected(); pN > 0 {
		j.logger.Debug("agentplane janitor: swept expired pairings", "count", pN)
	}

	// R7: 过期挂起 agent 任务收尾。设备 7 天没上线 → 把 pending session 标
	// failed（前端展示"任务超时未送达"）再删 pending 行。先 UPDATE 后 DELETE：
	// pending 行还在时才能定位到对应 session。
	const expirePendQ = `
		UPDATE agent_sessions SET state = 'failed', updated_at = now()
		 WHERE state = 'pending'
		   AND session_id IN (SELECT session_id FROM agent_pending_work WHERE expires_at < now())
		RETURNING session_id
	`
	if ids, peErr := j.failSessions(ctx, expirePendQ); peErr != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor: expire pending sessions failed", "err", peErr)
	} else if len(ids) > 0 {
		j.logger.Warn("agentplane janitor: marked expired pending tasks failed",
			"count", len(ids), "reason", "device offline past pending TTL")
		// pending 客户端不一定在流;重连时 ingress 从 .out replay(since_seq=0)仍能拿到。
		j.notifyFailed(ctx, ids, "任务超时未送达：设备长时间未上线，请重试")
	}
	if _, dpErr := j.pool.Exec(ctx,
		`DELETE FROM agent_pending_work WHERE expires_at < now()`); dpErr != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor: delete expired pending work failed", "err", dpErr)
	}

	// Phase 2: GC 老 offline 行。每次 sweep 都跑(SQL 扫索引很快,DELETE
	// 一般 0 行)。批量 LIMIT 5000 防一次扫全表锁太久。
	gcCutoff := time.Now().Add(-JanitorOfflineGCAge)
	const gcQ = `
		DELETE FROM agent_environments
		 WHERE environment_id IN (
		   SELECT environment_id FROM agent_environments
		    WHERE state = 'offline' AND last_seen_at < $1
		    LIMIT 5000
		 )
	`
	gcTag, gcErr := j.pool.Exec(ctx, gcQ, gcCutoff)
	if gcErr != nil {
		j.stats.Errors++
		j.logger.Error("agentplane janitor GC failed", "err", gcErr)
		return n
	}
	gcN := gcTag.RowsAffected()
	if gcN > 0 {
		j.stats.OfflineDeleted += gcN
		j.logger.Info("agentplane janitor GC deleted",
			"count", gcN,
			"gc_cutoff", gcCutoff.Format(time.RFC3339))
	}
	return n
}

// failSessions 执行一条 `UPDATE ... SET state='failed' ... RETURNING session_id`
// 并把命中的 session_id 收集返回。用 Query（非 Exec）拿到 RETURNING 行 ——
// 调用方据此向各 session 推失败帧。
func (j *Janitor) failSessions(ctx context.Context, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := j.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// notifyFailed 给每个被标 failed 的 session 向 biu.session.<id>.out 推一帧
// SDKResultError（recoverable=false）—— 复用 chat_runner.publishErrorFrame 的
// 同款构造,客户端 ingress 收到后走 MessageFailed 分支停掉 spinner + 显示错误。
// queue 为空（dev / 无 NATS）→ 跳过,只保留 DB 状态变更。单条 publish 失败仅
// log,不影响其它 session（best-effort 通知）。
func (j *Janitor) notifyFailed(ctx context.Context, ids []uuid.UUID, msg string) {
	if j.queue == nil {
		return
	}
	for _, sid := range ids {
		frame := sdkbridge.ToSDKFrame(biumindkit.Error{
			Err:         errors.New(msg),
			Recoverable: false,
		}, sid.String())
		raw, err := json.Marshal(frame)
		if err != nil {
			j.logger.Warn("agentplane janitor: marshal fail frame", "err", err, "session_id", sid)
			continue
		}
		if err := j.queue.PublishSessionFrame(ctx, sid, raw); err != nil {
			j.logger.Warn("agentplane janitor: publish fail frame",
				"err", err, "session_id", sid)
		}
	}
}
