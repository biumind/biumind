// Dispatcher — turns saved hits into user-visible signals: badge
// events on the user's realtime topic + (optionally) notify.send
// fanout to configured channels.
//
// Splitting writes from dispatch lets us back-fill missed
// notifications: anyone with read access to rss.watch_hits can
// re-render badge state, and a separate worker can sweep
// notified=false rows to retry external pushes if the in-line
// dispatch fails.

package radar

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/app_center/internal/events"
)

const dispatchAggregateThreshold = 5

// Notifier is the optional outbound surface for notify.send
// channels (Feishu / Bark / email / …). v0.1 has no concrete
// implementation — the rss app's permission model already
// accepts notify.send, but the notify-channels table + client
// haven't shipped yet. Dispatcher tolerates a nil Notifier so
// the badge / event pipeline still works.
type Notifier interface {
	Send(ctx context.Context, channelID string, msg NotifyMessage) error
}

type NotifyMessage struct {
	Title    string
	Body     string
	URL      string
	Severity string // info|warn|error
	RuleName string
}

type Dispatcher struct {
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	Notifier Notifier // optional; nil => skip notify.send fanout

	// Runner — M9.2: 解析 rule.Actions JSON 后顺序跑. nil 时退化到 v2
	// hardcoded notify+event 路径(向后兼容现网无 actions[] 的 rule).
	// 注入后, dispatcher 优先走 actions runner.
	Runner ActionDispatcher
}

// ActionDispatcher — actions.Runner 的子集接口, 让 dispatcher 不直接
// import actions 包(actions 已经 import radar 了, 反向引用会成环).
// main.go 用一个适配器把 *actions.Runner 包成这个接口.
type ActionDispatcher interface {
	// RunAction 跑一个 spec, 返 (resultJSON, err).
	// resultJSON 写到 rss.action_runs.result.
	RunAction(ctx context.Context, hit *Hit, actionType string, configRaw []byte) (resultJSON []byte, err error)
	// Types 返回已注册的所有类型, 启动日志用.
	Types() []string
}

func NewDispatcher(pool *pgxpool.Pool) *Dispatcher {
	return &Dispatcher{Pool: pool, Logger: slog.Default()}
}

// Dispatch processes a batch of newly-written hits.
//
// M9.2: 当 d.Runner 注入时, 走新路径:
//  1. 解析 rule.Actions []byte → []ActionSpec
//  2. 顺序跑每个 action, 失败不阻塞 (stop_on_error=false 默认)
//  3. 每次执行写一行 rss.action_runs (status / duration_ms / error / result)
//
// d.Runner 为 nil 时退化到 v2 hardcoded notify+event 路径(老 rule 没有
// actions[] 字段时仍工作).
//
// 所有路径都写 hits.notified=true (老 v2 行为), 让 radar tab 不再 unread.
func (d *Dispatcher) Dispatch(ctx context.Context, hits []Hit) error {
	if len(hits) == 0 {
		return nil
	}

	notified := make([]int64, 0, len(hits))
	for i := range hits {
		h := &hits[i]
		ok := d.dispatchOne(ctx, h)
		if ok {
			notified = append(notified, h.ID)
		}
	}

	if len(notified) > 0 {
		store := &Store{pool: d.Pool}
		if err := store.MarkHitNotified(ctx, notified); err != nil {
			d.Logger.Warn("radar: mark notified", "err", err.Error())
		}
	}
	return nil
}

// dispatchOne — 处理单个 hit, 返 ok=true 表示算 "已通知" (写
// hits.notified=true). 注意: ok 不代表所有 action 都成功; 一个 action
// 失败不影响整体 ok 返回 — action_runs 表自有更细粒度记录.
func (d *Dispatcher) dispatchOne(ctx context.Context, h *Hit) bool {
	// New path: rule.Actions[] 由 runner 跑.
	if d.Runner != nil {
		specs, err := parseActionSpecs(h.RuleSnapshot.Actions)
		if err != nil {
			d.Logger.Warn("radar: rule.actions malformed",
				"rule_id", h.RuleID, "err", err.Error())
			// 跑 fallback 路径以免单条坏 rule 全失声
			return d.dispatchLegacy(ctx, h)
		}
		if len(specs) == 0 {
			// 用户未配 actions[] — 退化到 legacy notify+event
			return d.dispatchLegacy(ctx, h)
		}
		anyRan := false
		for seq, s := range specs {
			d.runOne(ctx, h, seq, s)
			anyRan = true
		}
		return anyRan
	}
	// Legacy path (Runner 未注入时).
	return d.dispatchLegacy(ctx, h)
}

// runOne — 跑一个 spec, 写一行 action_runs.
func (d *Dispatcher) runOne(ctx context.Context, h *Hit, seq int, s actionSpec) {
	start := time.Now()
	resultJSON, err := d.Runner.RunAction(ctx, h, s.Type, []byte(s.Config))
	duration := time.Since(start)
	status := "ok"
	errStr := ""
	if err != nil {
		status = "error"
		errStr = err.Error()
		d.Logger.Warn("radar: action failed",
			"rule_id", h.RuleID, "type", s.Type, "err", err.Error())
	}
	d.writeActionRun(ctx, h, seq, s.Type, status, errStr, resultJSON, duration)
}

func (d *Dispatcher) writeActionRun(ctx context.Context, h *Hit, seq int,
	actionType, status, errStr string, resultJSON []byte, dur time.Duration,
) {
	var resultArg any
	if len(resultJSON) > 0 {
		resultArg = resultJSON
	}
	_, dbErr := d.Pool.Exec(ctx, `
		INSERT INTO rss.action_runs
			(rule_id, hit_id, action_seq, action_type, status, result, error, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		h.RuleID, h.ID, seq, actionType, status, resultArg, errStr,
		int(dur/time.Millisecond))
	if dbErr != nil {
		d.Logger.Warn("radar: action_runs insert", "err", dbErr.Error())
	}
}

// dispatchLegacy — v2 hardcoded notify+event 路径, runner 未注入或 actions[]
// 为空时使用. 不写 action_runs (legacy rule 没规划这个表).
func (d *Dispatcher) dispatchLegacy(ctx context.Context, h *Hit) bool {
	// 1. Event row → realtime fanout.
	payload := map[string]any{
		"hit_id":    h.ID,
		"rule_id":   h.RuleID.String(),
		"rule_name": h.RuleSnapshot.Name,
		"severity":  h.RuleSnapshot.OnHitBadge,
		"source":    h.Source,
		"title":     h.Title,
		"url":       h.URL,
		"hit_at":    h.HitAt,
	}
	if err := events.Write(ctx, d.Pool, events.Event{
		ScopeKind: h.RuleSnapshot.Scope,
		ScopeID:   h.RuleSnapshot.ScopeID,
		ActorType: events.ActorScheduler,
		ActorID:   "radar",
		Type:      events.RadarHitFired,
		Payload:   payload,
	}); err != nil {
		d.Logger.Error("radar: emit event", "hit_id", h.ID, "err", err.Error())
	}

	// 2. notify.send fanout.
	if d.Notifier != nil && len(h.RuleSnapshot.OnHitNotify) > 0 {
		msg := NotifyMessage{
			Title:    fmt.Sprintf("[%s] %s", h.RuleSnapshot.Name, h.Source),
			Body:     h.Title,
			URL:      h.URL,
			Severity: h.RuleSnapshot.OnHitBadge,
			RuleName: h.RuleSnapshot.Name,
		}
		ok := true
		for _, ch := range h.RuleSnapshot.OnHitNotify {
			if err := d.Notifier.Send(ctx, ch, msg); err != nil {
				d.Logger.Warn("radar: notify failed",
					"channel", ch, "hit_id", h.ID, "err", err.Error())
				ok = false
			}
		}
		return ok
	}
	return true
}

// actionSpec — dispatcher.go 内部用, 跟 actions.ActionSpec 同 shape.
// (跨包不能 alias, 但这里只用于 unmarshal — caller 只看 Type / Config.)
type actionSpec struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

func parseActionSpecs(raw []byte) ([]actionSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []actionSpec
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AggregateForBurst caps a per-tick dispatch at N items per rule,
// folding the rest into a single "+N more" payload. Prevents
// channel flooding when one rule matches a giant batch.
func AggregateForBurst(hits []Hit) []Hit {
	if len(hits) <= dispatchAggregateThreshold {
		return hits
	}
	perRule := map[string]int{}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		key := h.RuleID.String()
		if perRule[key] >= dispatchAggregateThreshold {
			continue
		}
		perRule[key]++
		out = append(out, h)
	}
	return out
}
