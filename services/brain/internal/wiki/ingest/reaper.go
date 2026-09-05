// Ingest task reaper — 回收永远不会自行推进的任务并重发给 wiki-llm。
//
// 背景：POST /ingest 创建任务时 publish 失败不回滚行（api.go 注释"留给
// operator 级 reaper 重发"），worker 中途死亡也不会有人察觉。本 reaper
// 就是那个缺失的回收者：
//
//   - 'pending' 超过 PendingStale：创建时 publish 失败或消息丢失 → 重发
//   - 'running'/'partial' 超过 ActiveStale：worker 崩溃 → 重置 pending 重发
//     （wiki-llm 的 streaming partial-save 让重跑幂等：已落地页按
//     seen_paths 去重，见 subscriber.go）
//
// 防毒丸：progress.requeue_count 超过 MaxRequeue 的任务标 failed 不再重发。
// updated_at 即惰性心跳（worker 每次推进都刷新），无需专用心跳字段。
// processor='client' 的镜像任务不归这里（W2 云端接管另行处理）。
package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
)

type ReaperConfig struct {
	Interval     time.Duration // default 60s
	PendingStale time.Duration // default 2min
	ActiveStale  time.Duration // default 10min
	ClientStale  time.Duration // default 10min；client 镜像任务无心跳超此值即接管
	MaxRequeue   int           // default 5；超过标 failed
	Logger       *slog.Logger
}

type Reaper struct {
	store   *Store
	sources *sources.Store // client 接管时复位 source 解析状态；nil → 只标任务
	pub     publisher.Publisher
	cfg     ReaperConfig
	logger  *slog.Logger
}

func NewReaper(store *Store, pub publisher.Publisher, cfg ReaperConfig) *Reaper {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.PendingStale <= 0 {
		cfg.PendingStale = 2 * time.Minute
	}
	if cfg.ActiveStale <= 0 {
		cfg.ActiveStale = 10 * time.Minute
	}
	if cfg.ClientStale <= 0 {
		cfg.ClientStale = 10 * time.Minute
	}
	if cfg.MaxRequeue <= 0 {
		cfg.MaxRequeue = 5
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Reaper{store: store, pub: pub, cfg: cfg, logger: logger}
}

// WithSources 注入 sources store（client 接管需要复位 source 解析状态）。
func (r *Reaper) WithSources(s *sources.Store) *Reaper {
	r.sources = s
	return r
}

func (r *Reaper) Run(ctx context.Context) {
	r.logger.Info("ingest reaper started",
		"interval", r.cfg.Interval,
		"pending_stale", r.cfg.PendingStale,
		"active_stale", r.cfg.ActiveStale,
		"max_requeue", r.cfg.MaxRequeue)
	t := time.NewTicker(r.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("ingest reaper stopped")
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce scans for stuck tasks once. Test entry point.
func (r *Reaper) RunOnce(ctx context.Context) {
	now := time.Now()
	stuck, err := r.store.ListStuck(ctx, "server",
		now.Add(-r.cfg.PendingStale), now.Add(-r.cfg.ActiveStale), 100)
	if err != nil {
		r.logger.Warn("ingest reaper: query failed", "err", err)
		return
	}
	for _, task := range stuck {
		if task.RequeueCount() >= r.cfg.MaxRequeue {
			if err := r.store.MarkTerminal(ctx, task.ID, StatusFailed,
				"requeue limit reached (poison task?)", "system", "ingest-reaper"); err != nil {
				r.logger.Warn("ingest reaper: mark failed",
					"task_id", task.ID, "err", err)
			}
			continue
		}
		requeued, err := r.store.Requeue(ctx, task.ID)
		if err != nil {
			// ErrNotFound = 并发下 worker 刚好推进/完结，无需重发。
			if err != ErrNotFound {
				r.logger.Warn("ingest reaper: requeue",
					"task_id", task.ID, "err", err)
			}
			continue
		}
		r.publish(ctx, requeued)
	}

	// 第二遍：客户端镜像任务（processor=client）。客户端每次 PATCH 刷新
	// updated_at（惰性心跳）；超时无心跳 = 客户端进程死亡 → 云端接管。
	clientStuck, err := r.store.ListStuck(ctx, "client",
		now.Add(-r.cfg.ClientStale), now.Add(-r.cfg.ClientStale), 100)
	if err != nil {
		r.logger.Warn("ingest reaper: client query failed", "err", err)
		return
	}
	for _, task := range clientStuck {
		r.takeOverClient(ctx, task)
	}

	// 第三遍：取消清扫。取消广播是 fire-and-forget —— 广播时无 worker
	// 在线信号即丢，而 ListStuck 又跳过 cancel_requested 的行。超龄未
	// 被 worker 观测的取消任务在这里落终态，否则永远卡 pending/running。
	if n, err := r.store.SweepCancelRequested(ctx, now.Add(-r.cfg.ActiveStale)); err != nil {
		r.logger.Warn("ingest reaper: cancel sweep", "err", err)
	} else if n > 0 {
		r.logger.Info("ingest reaper: cancel sweep", "swept", n)
	}
}

// takeOverClient 接管卡死的客户端镜像任务。客户端解析的产物是 source 的
// extracted_text，因此接管发生在 source 层而非重发 ingest：
//   - source 带 file_id：parse_status 重置 'queued'，wiki-parse tick 下轮
//     真正接管解析；任务标 failed（接管完成，语义上这个镜像任务已终结）
//   - source 无 file_id（P1 本机路径不上传原文件）：云端无文件可解析，
//     source 标 error + 任务标 failed，用户在客户端重试
func (r *Reaper) takeOverClient(ctx context.Context, task *Task) {
	reason := "client task orphaned"
	if r.sources != nil && task.SourceID != nil {
		src, err := r.sources.Get(ctx, *task.SourceID)
		switch {
		case err != nil:
			r.logger.Warn("ingest reaper: source lookup",
				"task_id", task.ID, "source_id", task.SourceID, "err", err)
		case src.FileID != nil:
			if _, uerr := r.sources.UpdateParseStatus(ctx, sources.UpdateParseInput{
				ID:          src.ID,
				ParseStatus: "queued",
			}); uerr != nil {
				r.logger.Warn("ingest reaper: source requeue",
					"task_id", task.ID, "source_id", src.ID, "err", uerr)
			} else {
				reason = "client orphaned; parse taken over by cloud (source re-queued)"
				r.logger.Info("ingest reaper: client task taken over",
					"task_id", task.ID, "source_id", src.ID)
			}
		default:
			if _, uerr := r.sources.UpdateParseStatus(ctx, sources.UpdateParseInput{
				ID:          src.ID,
				ParseStatus: "error",
				ParseError:  "client parse orphaned (no server-side file to take over)",
			}); uerr != nil {
				r.logger.Warn("ingest reaper: source mark error",
					"task_id", task.ID, "source_id", src.ID, "err", uerr)
			}
			reason = "client orphaned; no server-side file to take over"
		}
	}
	if err := r.store.MarkTerminal(ctx, task.ID, StatusFailed, reason, "system", "ingest-reaper"); err != nil {
		r.logger.Warn("ingest reaper: client mark failed",
			"task_id", task.ID, "err", err)
	}
}

// publish 重组与创建时相同的 payload 重发任务（两段式 subject，对齐
// api.go handleCreate 的修复）。
func (r *Reaper) publish(ctx context.Context, t *Task) {
	payload := map[string]any{
		"task_id":    t.ID.String(),
		"project_id": t.ProjectID.String(),
		"owner_id":   t.OwnerID.String(),
		"title":      t.Title,
		"raw_text":   t.RawText,
	}
	if t.SourceID != nil {
		payload["source_id"] = t.SourceID.String()
	}
	pubCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := r.pub.Publish(pubCtx, "wiki.ingest", "requested", payload); err != nil {
		r.logger.Warn("ingest reaper: publish failed",
			"task_id", t.ID, "err", err)
		return
	}
	r.logger.Info("ingest reaper: task requeued",
		"task_id", t.ID, "requeue_count", t.RequeueCount())
}
