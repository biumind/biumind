// Package orchestrator 订阅 NATS aigc.task.update (workers/aigc 发出),
// 把进度 / 完成 / 失败事件写回 PG, 并把面向客户端的事件 fan-out 到
// services/realtime 让 SSE 多 topic 推给桌面/移动/Web 客户端.
//
// 流向:
//
//	workers/aigc → NATS aigc.task.update → 本 orchestrator
//	  ├─→ store.UpdateTaskStatus / CreateTaskOutput / AddLineageEdge
//	  └─→ NATS biumind.<env>.aigc.task.realtime  (services/realtime 转 SSE)
//
// 失败处理:
//   - status=failed/blocked 且 worker 报告 refunded_credits>0 时本 orchestrator
//     不再调 billing.Refund (worker 已通过自己的 billing client 退过); 这里只
//     把 refunded_credits 落库供前端展示.
//   - status=failed 但 worker 没退款 (RefundedCredits=0): orchestrator 兜底退款
//     (按 task.cost_credits 全额退). 这套兜底只在 P3 阶段 worker 不接 billing
//     时启用; 后续若 worker 自管退款就从 store.UpdateTaskStatus 里读 task.cost_credits
//     做参考即可.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// ─── Wire schema ──────────────────────────────────────

// TaskUpdateEvent 是 workers/aigc 发出的事件 (与 packages/proto/biumind/aigc/v1
// /aigc.proto 的 TaskUpdateEvent 字段对齐).
type TaskUpdateEvent struct {
	TaskID          string        `json:"task_id"`
	Status          string        `json:"status"`
	Progress        int16         `json:"progress"`
	Outputs         []OutputEntry `json:"outputs,omitempty"`
	ErrorCode       string        `json:"error_code,omitempty"`
	ErrorMessage    string        `json:"error_message,omitempty"`
	RefundedCredits int64         `json:"refunded_credits,omitempty"`
	ExternalTaskID  string        `json:"external_task_id,omitempty"`
	CacheHit        bool          `json:"cache_hit,omitempty"`
	UpdatedAt       *time.Time    `json:"updated_at,omitempty"`
	Seq             int64         `json:"seq,omitempty"`
}

// OutputEntry — worker 完成后转存到 MinIO, 在 status=completed 事件里一并带回.
// services/aigc 落 task_outputs.
type OutputEntry struct {
	Idx        int16  `json:"idx"`
	Kind       string `json:"kind"`
	SHA256     string `json:"sha256"`
	StorageURL string `json:"storage_url"`
	StorageKey string `json:"storage_key"`
	Blurhash   string `json:"blurhash,omitempty"`
	CoverSHA   string `json:"cover_sha,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	// Metadata — 任意结构化产物 (爆款解析拆解结果: 文案/钩子/分镜/标签)。
	// worker 在 kind="hotparse" 时把拆解 JSON 放这里, 透传落 task_outputs.metadata。
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ─── Orchestrator ─────────────────────────────────────

type Orchestrator struct {
	Store  *store.Store
	Bus    bus.Bus // 用于订阅 aigc.task.update + 转发到 realtime
	Env    string  // "dev" / "staging" / "prod" — fan-out subject 用
	Logger *slog.Logger

	sub bus.Subscription
}

// Subjects 是常量 (避免散落硬编码).
const (
	SubjectTaskSubmit = "aigc.task.submit"
	SubjectTaskUpdate = "aigc.task.update"
	SubjectTaskCancel = "aigc.task.cancel"
)

// realtimeSubject 是 services/realtime natsbus 订阅的 wildcard
// "biumind.<env>.*.*.realtime" 的具体匹配项.
func (o *Orchestrator) realtimeSubject() string {
	return "biumind." + o.Env + ".aigc.task.realtime"
}

// Start 起订阅. 返回错误时表示订阅初始化失败 (例如 NoopBus); 调用方决定是否致命.
func (o *Orchestrator) Start(ctx context.Context) error {
	sub, err := o.Bus.Subscribe(SubjectTaskUpdate, o.handle(ctx))
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", SubjectTaskUpdate, err)
	}
	o.sub = sub
	o.Logger.Info("orchestrator started", "subject", SubjectTaskUpdate)

	// graceful drain
	go func() {
		<-ctx.Done()
		_ = o.sub.Drain()
		o.Logger.Info("orchestrator drained")
	}()
	return nil
}

func (o *Orchestrator) handle(ctx context.Context) bus.Handler {
	return func(m *bus.Message) {
		var ev TaskUpdateEvent
		if err := json.Unmarshal(m.Body, &ev); err != nil {
			o.Logger.Warn("orchestrator: bad event", "err", err)
			return
		}
		if err := o.Apply(ctx, &ev); err != nil {
			o.Logger.Error("orchestrator: apply failed",
				"task_id", ev.TaskID, "status", ev.Status, "err", err)
		}
	}
}

// Apply 是核心逻辑, 抽出来便于单测直接调 (不走 bus 中间层).
//
// 顺序:
//  1. UpdateTaskStatus (含 progress / queued_at / started_at / completed_at)
//  2. status=completed 时写 task_outputs + 血缘 edge (parent_sha → child_sha)
//  3. status=failed/blocked 时兜底退款 (worker 没退过的话)
//  4. fan-out 到 realtime (subject biumind.<env>.aigc.task.realtime; topic aigc.user.{uid}.tasks)
func (o *Orchestrator) Apply(ctx context.Context, ev *TaskUpdateEvent) error {
	taskID, err := uuid.Parse(ev.TaskID)
	if err != nil {
		return fmt.Errorf("bad task_id: %w", err)
	}

	// 先取 task 看 owner / parent_sha / cost_credits (退款兜底用).
	task, err := o.Store.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			o.Logger.Warn("orchestrator: task not found", "task_id", taskID)
			return nil // 静默丢弃; 可能是测试 leftover
		}
		return fmt.Errorf("get task: %w", err)
	}

	// 1) 写状态 + 时间戳
	now := time.Now().UTC()
	updArgs := store.UpdateTaskStatusArgs{
		ID:             taskID,
		Status:         ev.Status,
		ErrorCode:      ev.ErrorCode,
		ErrorMessage:   ev.ErrorMessage,
		ExternalTaskID: ev.ExternalTaskID,
	}
	if ev.Progress > 0 {
		p := ev.Progress
		updArgs.Progress = &p
	}
	if ev.RefundedCredits > 0 {
		r := ev.RefundedCredits
		updArgs.RefundedCredits = &r
	}
	if ev.CacheHit {
		hit := true
		updArgs.CacheHit = &hit
	}
	switch ev.Status {
	case "queued":
		updArgs.QueuedAt = &now
	case "running":
		// 第一次进 running 时设 started_at (后续 progress 更新不覆盖, 由 store 的
		// COALESCE 保护)
		updArgs.StartedAt = &now
	case "completed", "failed", "blocked", "cancelled":
		updArgs.CompletedAt = &now
	}
	if err := o.Store.UpdateTaskStatus(ctx, updArgs); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	// 2) outputs + 血缘 (仅 completed)
	if ev.Status == "completed" && len(ev.Outputs) > 0 {
		for _, out := range ev.Outputs {
			if _, err := o.Store.CreateTaskOutput(ctx, store.CreateTaskOutputArgs{
				TaskID:     taskID,
				Idx:        out.Idx,
				Kind:       out.Kind,
				SHA256:     out.SHA256,
				StorageURL: out.StorageURL,
				StorageKey: out.StorageKey,
				Blurhash:   out.Blurhash,
				CoverSHA:   out.CoverSHA,
				MimeType:   out.MimeType,
				FileSize:   out.FileSize,
				Width:      out.Width,
				Height:     out.Height,
				DurationMs: out.DurationMs,
				Metadata:   metadataOrNil(out.Metadata),
			}); err != nil {
				// 可能是重复转存 (sha 唯一约束等), 不致命; 但记 log
				o.Logger.Warn("orchestrator: create output", "task_id", taskID, "idx", out.Idx, "err", err)
				continue
			}
			// 血缘: parent_sha → 本次 output sha (若有)
			if task.ParentSHA != "" {
				op := task.LineageOp
				if op == "" {
					op = "remix"
				}
				if err := o.Store.AddLineageEdge(ctx, store.AddLineageEdgeArgs{
					ChildSHA:    out.SHA256,
					ParentSHA:   task.ParentSHA,
					Op:          op,
					ChildTaskID: &taskID,
				}); err != nil {
					o.Logger.Warn("orchestrator: lineage edge", "err", err)
				}
			}
		}
	}

	// 段3.6: 不再有 orchestrator 兜底退款。计费已归一到 model-relay 同步
	// Hold/Settle — 生成失败时 model-relay 在同一请求内 Release hold(零扣),
	// aigc 侧无需退款。ev.RefundedCredits 仍透传供前端展示(见上 updArgs)。

	// 4) fan-out 到 realtime — services/realtime 收到后转 SSE
	if err := o.fanout(ctx, task, ev); err != nil {
		// realtime 不可达不阻塞主流程 (客户端有 30s 短轮询兜底)
		o.Logger.Warn("orchestrator: fanout failed", "err", err)
	}
	return nil
}

// fanout 把事件包成 realtime wire-format 推到 NATS.
// services/realtime/internal/natsbus 的 wireMsg: {topic, kind, payload}.
func (o *Orchestrator) fanout(ctx context.Context, task *store.Task, ev *TaskUpdateEvent) error {
	topic := fmt.Sprintf("aigc.user.%s.tasks", task.UserID)
	// 简化: 客户端 payload 就是 ev 本身 (包含完整 outputs).
	wire := map[string]any{
		"topic":   topic,
		"kind":    "aigc.task.update",
		"payload": ev,
	}
	return o.Bus.Publish(ctx, o.realtimeSubject(), wire)
}

// metadataOrNil 把空 metadata 归一成 nil interface, 避免把 typed-nil 传给
// store (store 用 a.Metadata != nil 判定, typed-nil slice 包进 any 会变非空 →
// 误写 jsonb)。worker 对普通 image/video output 也会带 metadata={}(dataclass
// 默认值), 故 null / {} 都视为空 —— 仅 hotparse 等真有结构化产物时才落库。
func metadataOrNil(m json.RawMessage) any {
	s := strings.TrimSpace(string(m))
	if s == "" || s == "null" || s == "{}" {
		return nil
	}
	return m
}
