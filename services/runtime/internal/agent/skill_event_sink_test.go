// SkillEventSink 单测（S11-4 后修剪）—— PublisherEventSink + 老
// publisher 路径在 S11-4 删了；只保留 FrameEventSink + NopEventSink +
// SkillToolDeps.emit 的 sink dispatch 行为。

package agent

import (
	"context"
	"sync"
	"testing"
)

// recordingSink 记录 Sink.Emit 调用。
type recordingSink struct {
	mu    sync.Mutex
	calls []recordedSinkCall
}

type recordedSinkCall struct {
	EventType string
	Payload   map[string]any
}

func (r *recordingSink) Emit(_ context.Context, eventType string, payload map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedSinkCall{EventType: eventType, Payload: payload})
}

func TestFrameEventSink_Emit(t *testing.T) {
	var got map[string]any
	sink := &FrameEventSink{
		SessionID: "sess-1",
		Emit_: func(_ context.Context, frame map[string]any) {
			got = frame
		},
	}
	sink.Emit(context.Background(), "biumind.runtime.skill.exec_started",
		map[string]any{"skill": "demo", "tool_use_id": "tu-1"})

	if got["type"] != "system" {
		t.Errorf("frame type=%v want system", got["type"])
	}
	if got["subtype"] != "status" {
		t.Errorf("frame subtype=%v want status", got["subtype"])
	}
	if got["name"] != "biumind.runtime.skill.exec_started" {
		t.Errorf("frame name=%v want biumind.runtime.skill.exec_started", got["name"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data field missing/wrong type: %+v", got["data"])
	}
	if data["skill"] != "demo" {
		t.Errorf("data lost: %+v", data)
	}
	if got["session_id"] != "sess-1" {
		t.Errorf("session_id=%v", got["session_id"])
	}
	if got["uuid"] == nil || got["uuid"] == "" {
		t.Errorf("frame missing uuid")
	}
}

func TestFrameEventSink_NilEmitter(t *testing.T) {
	sink := &FrameEventSink{SessionID: "x", Emit_: nil}
	sink.Emit(context.Background(), "x", nil) // 静默不 panic
}

func TestNopEventSink_Emit(t *testing.T) {
	NopEventSink{}.Emit(context.Background(), "x", map[string]any{"a": 1})
	var s SkillEventSink = NopEventSink{}
	s.Emit(context.Background(), "y", nil)
}

func TestSkillToolDeps_EmitGoesToSink(t *testing.T) {
	sink := &recordingSink{}
	deps := &SkillToolDeps{EventSink: sink}
	deps.emit(context.Background(), "biumind.runtime.skill.activated",
		map[string]any{"k": "v"})
	if len(sink.calls) != 1 {
		t.Errorf("sink calls=%d want 1", len(sink.calls))
	}
}

func TestSkillToolDeps_EmitSilentWhenNoSink(t *testing.T) {
	// 没 sink → 静默不 panic（S11-4 删了 publisher fallback）
	deps := &SkillToolDeps{}
	deps.emit(context.Background(), "x", nil)
}

// 接口编译期确认 —— 防 sink 接口签名漂走
func TestSinkInterfaceConformance(t *testing.T) {
	var _ SkillEventSink = (*FrameEventSink)(nil)
	var _ SkillEventSink = NopEventSink{}
}
