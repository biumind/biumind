// SkillEventSink —— S12-2 Skills 事件投递的抽象层。
//
// 旧路径：runtime agent loop 用 publisher.Publisher 把事件 fanout 到
// realtime 服务，realtime 再 SSE 推给客户端。AG-UI 协议（RUN_STARTED /
// TEXT_MESSAGE_* / 自定义 biumind.runtime.skill.*）。
//
// 新路径：runtime 走 brain Agent Plane WS，事件应该是 SDK Protocol v1 帧
// （SDKSystemStatus / 等）发到 biu.session.<sid>.out 让 ingress 转 client。
//
// SkillEventSink 把 SkillToolDeps 跟具体的下游解耦：
//   - PublisherEventSink：包 publisher.Publisher，emit 即调老 AG-UI
//   - FrameEventSink：把 (eventType, payload) 翻译成 SDK Protocol 帧 publish
//   - NopEventSink：null sink，CLI / 单测用，emit 静默吞
//
// SkillToolDeps.emit 通过 SkillEventSink 派发；当 sink == nil 时彻底
// 静默（旧 publisher fallback 在 S11-4 删了）。

package agent

import (
	"context"
)

// SkillEventSink 是 skill 工具发送跑动事件的下游。EventType + payload
// 跟 PS3.5 五个 biumind.runtime.skill.* 事件保持一致；payload 是 free-form
// JSON-able map（具体字段由 emit 调用点定）。
//
// 实现必须容错 —— 一个事件投递失败绝不能让 tool 调用本身炸掉（tool
// result 仍要回到 model）。
type SkillEventSink interface {
	Emit(ctx context.Context, eventType string, payload map[string]any)
}

// FrameEmitterFunc 是 FrameEventSink 把 SDK Protocol 帧推到下游的回调。
// runtime worker（S11-2）实际接 sdkbridge + brain.PublishSessionFrame；
// 抽出 callback 让 sink 不直接依赖 sdkproto / sdkbridge / agentplane Client。
type FrameEmitterFunc func(ctx context.Context, frame map[string]any)

// FrameEventSink 把 SkillEventSink 翻译到 SDK Protocol 帧。
//
// 翻译策略：runtime skill 自定义事件 → SDKSystemStatus 帧（type=system,
// subtype=status, name=<eventType>, data=payload）。客户端按 name 字段
// 渲染对应 UI（"skill activated" / "exec started" / 等）。
//
// 之所以用 system_status 而非新增 schema 类型：
//   - 避开协议层 schema 演化成本（status 已经在 schema/sdk/v1/system.json）
//   - 兼容现有 ingress / Dart ServiceFrame.fromJson dispatcher（无新 case）
//   - 客户端基于 name 字段自由展示，不需要新类型
//
// SessionID 是 brain agent_sessions 主键 —— S12-2 wire 时由 worker.go
// 拿 WorkPayload.SessionID 注入。
type FrameEventSink struct {
	Emit_     FrameEmitterFunc
	SessionID string
}

// Emit 把 (eventType, payload) 包成 system_status SDK 帧推下游。
func (s *FrameEventSink) Emit(ctx context.Context, eventType string, payload map[string]any) {
	if s == nil || s.Emit_ == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	frame := map[string]any{
		"type":       "system",
		"subtype":    "status",
		"name":       eventType,
		"data":       payload,
		"uuid":       newID(),
		"session_id": s.SessionID,
	}
	s.Emit_(ctx, frame)
}

// NopEventSink 不做任何事 —— CLI / 单测 / 没配下游时用。
type NopEventSink struct{}

// Emit 静默吞。
func (NopEventSink) Emit(_ context.Context, _ string, _ map[string]any) {}
