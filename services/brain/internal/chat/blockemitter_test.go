package chat

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeFlusher: httptest.ResponseRecorder doesn't implement Flusher,
// so we wrap one that does (the recorder satisfies it via embedding
// in newer go-http; this is a defensive shim).
type recorderWithFlush struct {
	*httptest.ResponseRecorder
}

func (r *recorderWithFlush) Flush() {}

func newEmitter(t *testing.T) (*BlockEmitter, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	rwf := &recorderWithFlush{ResponseRecorder: rec}
	return NewBlockEmitter(rwf, rwf, uuid.New()), rec
}

// parseSSE returns event/data pairs in order.
func parseSSE(t *testing.T, body string) []sseEvt {
	t.Helper()
	var out []sseEvt
	var event, data string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if event != "" {
				out = append(out, sseEvt{Event: event, Data: data})
				event, data = "", ""
			}
		}
	}
	return out
}

type sseEvt struct {
	Event, Data string
}

func TestTextDeltaEmitsCreateThenDeltaAndLegacy(t *testing.T) {
	e, rec := newEmitter(t)
	e.TextDelta("Hi ")
	e.TextDelta("there")
	e.CloseActiveText()

	evts := parseSSE(t, rec.Body.String())

	// Expect: block.create, block.delta, delta, block.delta, delta,
	// block.complete.
	want := []string{
		EventBlockCreate,
		EventBlockDelta,
		EventLegacyDelta,
		EventBlockDelta,
		EventLegacyDelta,
		EventBlockComplete,
	}
	if len(evts) != len(want) {
		t.Fatalf("expected %d events, got %d: %+v", len(want), len(evts), evts)
	}
	for i, w := range want {
		if evts[i].Event != w {
			t.Errorf("evt[%d]: got %q want %q", i, evts[i].Event, w)
		}
	}
}

func TestPartsJSONAccumulatesText(t *testing.T) {
	e, _ := newEmitter(t)
	e.TextDelta("hello ")
	e.TextDelta("world")
	if e.AccumulatedText() != "hello world" {
		t.Errorf("accumulated text mismatch: %q", e.AccumulatedText())
	}
	e.CloseActiveText()

	var parts []map[string]any
	if err := json.Unmarshal(e.PartsJSON(), &parts); err != nil {
		t.Fatalf("parts json: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "hello world" {
		t.Errorf("unexpected part: %+v", parts[0])
	}
}

func TestMessageDoneEmitsLegacyAndV2(t *testing.T) {
	e, rec := newEmitter(t)
	e.TextDelta("ok")
	e.MessageDone(map[string]any{"reason": "stop"})

	evts := parseSSE(t, rec.Body.String())
	// Last two events should be message.done then legacy done.
	if len(evts) < 2 {
		t.Fatalf("not enough events: %+v", evts)
	}
	last := evts[len(evts)-1].Event
	beforeLast := evts[len(evts)-2].Event
	if beforeLast != EventMessageDone || last != EventLegacyDone {
		t.Errorf("tail events: got %s,%s want %s,%s",
			beforeLast, last, EventMessageDone, EventLegacyDone)
	}
}

func TestToolStartCloseInterleaving(t *testing.T) {
	e, rec := newEmitter(t)
	e.TextDelta("calling tool…")
	id := e.ToolStarted("websearch", map[string]any{"q": "x"})
	e.ToolCompleted(id, []string{"a", "b"}, 42)
	e.TextDelta("done.")
	e.MessageDone(map[string]any{})

	evts := parseSSE(t, rec.Body.String())
	// Verify the order at key boundaries.
	var seen []string
	for _, ev := range evts {
		seen = append(seen, ev.Event)
	}
	// First text block must close before tool fires.
	idxToolCreated := indexOf(seen, EventToolCreated)
	idxFirstClose := indexOf(seen, EventBlockComplete)
	if idxFirstClose < 0 || idxToolCreated < 0 ||
		idxFirstClose >= idxToolCreated {
		t.Errorf("expected text block to close before tool.created; got order %v",
			seen)
	}

	// parts: text, tool_use(success), text
	var parts []map[string]any
	_ = json.Unmarshal(e.PartsJSON(), &parts)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0]["type"] != "text" || parts[1]["type"] != "tool_use" ||
		parts[2]["type"] != "text" {
		t.Errorf("unexpected parts shape: %+v", parts)
	}
	if parts[1]["phase"] != "success" {
		t.Errorf("tool phase: got %v want success", parts[1]["phase"])
	}
}

func TestThinkingDeltaSeparateFromText(t *testing.T) {
	e, rec := newEmitter(t)
	e.ThinkingDelta("reasoning… ")
	e.ThinkingDelta("done thinking.")
	e.TextDelta("answer.")
	e.MessageDone(map[string]any{})

	evts := parseSSE(t, rec.Body.String())
	var seen []string
	for _, ev := range evts {
		seen = append(seen, ev.Event)
	}
	// Must see thinking block.create + 2 deltas + complete BEFORE
	// text block.create.
	idxThinkingCreate := indexOf(seen, EventBlockCreate)
	idxFirstComplete := indexOf(seen, EventBlockComplete)
	idxLastDelta := -1
	for i, s := range seen {
		if s == EventBlockDelta {
			idxLastDelta = i
		}
	}
	if idxFirstComplete < 0 || idxThinkingCreate < 0 ||
		idxFirstComplete >= idxLastDelta {
		t.Errorf("thinking block must close before text deltas; order=%v",
			seen)
	}

	// parts: thinking + text. No legacy `delta` event for thinking
	// (would confuse pre-v2 clients into rendering reasoning inline).
	var parts []map[string]any
	_ = json.Unmarshal(e.PartsJSON(), &parts)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %+v", len(parts), parts)
	}
	if parts[0]["type"] != BlockTypeThinking {
		t.Errorf("first part type: got %v want thinking", parts[0]["type"])
	}
	if parts[0]["text"] != "reasoning… done thinking." {
		t.Errorf("thinking text: %v", parts[0]["text"])
	}
	if parts[1]["type"] != BlockTypeText {
		t.Errorf("second part type: got %v want text", parts[1]["type"])
	}

	// Verify legacy `delta` count: 1 (the answer text only).
	legacyDeltas := 0
	for _, ev := range evts {
		if ev.Event == EventLegacyDelta {
			legacyDeltas++
		}
	}
	if legacyDeltas != 1 {
		t.Errorf("legacy delta count: got %d want 1 (text only)",
			legacyDeltas)
	}
}

func TestPartsJSONEmptyByDefault(t *testing.T) {
	e, _ := newEmitter(t)
	if string(e.PartsJSON()) != "[]" {
		t.Errorf("expected [] got %s", e.PartsJSON())
	}
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
