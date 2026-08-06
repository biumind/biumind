package translate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// fakeProvider always returns a fixed text via a one-frame stream.
type fakeProvider struct {
	out string
	got llm.ChatRequest
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Frame, error) {
	f.got = req
	ch := make(chan llm.Frame, 2)
	go func() {
		defer close(ch)
		ch <- llm.Frame{Kind: llm.KindDelta, Text: f.out}
		ch <- llm.Frame{Kind: llm.KindEnd}
	}()
	return ch, nil
}

func TestTranslate_HappyPath(t *testing.T) {
	fp := &fakeProvider{out: "Hello"}
	a := New(fp, "claude-opus-4-7")
	if err := a.Init(context.Background(), biuapp.Deps{}); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, err := a.Invoke(context.Background(), "translate",
		json.RawMessage(`{"text":"你好","target":"en"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	o := out.(Output)
	if o.Text != "Hello" || o.Target != "en" || o.Source != "auto" || o.Model != "claude-opus-4-7" {
		t.Errorf("bad output: %+v", o)
	}
	// System prompt should reflect target language.
	if !strings.Contains(fp.got.System, "Translate the user's text into en") {
		t.Errorf("system prompt: %s", fp.got.System)
	}
}

func TestTranslate_RejectsEmptyText(t *testing.T) {
	a := New(&fakeProvider{}, "")
	_ = a.Init(context.Background(), biuapp.Deps{})
	if _, err := a.Invoke(context.Background(), "translate",
		json.RawMessage(`{"text":""}`)); err == nil ||
		!strings.Contains(err.Error(), "empty text") {
		t.Errorf("want empty-text err, got %v", err)
	}
}

func TestTranslate_RequiresProvider(t *testing.T) {
	a := New(nil, "")
	if err := a.Init(context.Background(), biuapp.Deps{}); err == nil ||
		!strings.Contains(err.Error(), "provider is nil") {
		t.Errorf("want provider-nil err, got %v", err)
	}
}
