// Package actions — pluggable runner for "what to do when a rule fires".
//
// 设计:
//   - 每个 Action 实现 Run(ctx, hit, configRaw) (Result, error)
//   - Runner 持有 type → Action 注册表
//   - Dispatcher 解析 rule.Actions JSON ([]ActionSpec) 顺序跑
//   - 每次 Run 写一行 rss.action_runs (status / duration_ms / error / result)
//
// 失败策略:
//   默认 stop_on_error=false (M9.2 验收): 一个 action 失败不阻塞后续.
//   异常 (panic) 被 runner.Run 包裹成 error, 不冒到 dispatcher.
//
// 不在 runner 层做的:
//   - cooldown / aggregate — 已在 store 层和 AggregateForBurst 里
//   - retry — 失败就失败, 用户从 action_runs 历史看到了, 自己决定要不要
//     手动重发. 自动 retry 留到后续 milestone.

package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/services/app_center/internal/radar"
)

// ActionSpec — 一个 rule.Actions JSON 元素.
//
// 格式: {"type": "notify", "config": {...任意, 由 Action 自解...}}
// config 用 RawMessage 让具体 Action 自己 unmarshal, runner 不绑死结构.
type ActionSpec struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

// Result — Action.Run 成功时的输出, 写到 rss.action_runs.result jsonb.
// 用任意 map 让每个 Action 自己描述 (notify_channel_count / wiki_block_id /
// task_id ...). 失败时 caller 写 error 字段, result 留 NULL.
type Result map[string]any

// Action — runner 通过 Type → Action 找到实现.
type Action interface {
	// Type 返回稳定枚举: "notify" / "wiki" / "task" / "skill".
	// 跟 ActionSpec.Type 字段匹配.
	Type() string

	// Run 跑一次 action. configRaw 是用户配置的 jsonb (从 ActionSpec.Config
	// 透传过来). 实现自己 unmarshal 成具体结构.
	Run(ctx context.Context, hit *radar.Hit, configRaw json.RawMessage) (Result, error)
}

// Runner — 持有 type → Action map.
type Runner struct {
	actions map[string]Action
}

// NewRunner 注册一组 Action. 重名 panic — 配置错误应当尽早暴露.
func NewRunner(impls ...Action) *Runner {
	r := &Runner{actions: make(map[string]Action, len(impls))}
	for _, a := range impls {
		t := a.Type()
		if _, exists := r.actions[t]; exists {
			panic(fmt.Sprintf("actions: duplicate registration for type %q", t))
		}
		r.actions[t] = a
	}
	return r
}

// Run 找对应 Action 跑. 不存在的 type 返回 ErrUnknownType, dispatcher
// 据此把这条 spec 标 "unknown_action_type" 并继续后续.
func (r *Runner) Run(ctx context.Context, hit *radar.Hit, spec ActionSpec) (Result, error) {
	a, ok := r.actions[spec.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownType, spec.Type)
	}
	return a.Run(ctx, hit, spec.Config)
}

// Types 返回所有已注册的 type, 主要用于启动日志.
func (r *Runner) Types() []string {
	out := make([]string, 0, len(r.actions))
	for t := range r.actions {
		out = append(out, t)
	}
	return out
}

// ParseSpecs 解析 rule.Actions jsonb. 空数组 / 空字节都返 nil 不报错;
// 真正解析失败返 error 让 dispatcher 整条 rule 标 "actions_malformed".
func ParseSpecs(raw []byte) ([]ActionSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// 空数组 "[]" 也应该返 nil
	var specs []ActionSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("actions: parse rule.actions: %w", err)
	}
	return specs, nil
}

// ErrUnknownType — runner 收到未注册的 spec.Type. 用 errors.Is 检测.
var ErrUnknownType = errUnknownType{}

type errUnknownType struct{}

func (errUnknownType) Error() string { return "unknown action type" }
