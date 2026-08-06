package actions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/biumind/biumind/services/app_center/internal/radar"
	"github.com/google/uuid"
)

// stubAction — 测试用 Action, 可控成功/失败 + 记录调用.
type stubAction struct {
	typeName string
	fail     bool
	calls    []json.RawMessage
}

func (s *stubAction) Type() string { return s.typeName }
func (s *stubAction) Run(_ context.Context, _ *radar.Hit, cfg json.RawMessage) (Result, error) {
	s.calls = append(s.calls, cfg)
	if s.fail {
		return nil, errors.New("stub fail")
	}
	return Result{"ok": true, "type": s.typeName}, nil
}

func TestRunner_RegistersAndDispatches(t *testing.T) {
	notify := &stubAction{typeName: "notify"}
	wiki := &stubAction{typeName: "wiki"}
	r := NewRunner(notify, wiki)

	if got := len(r.Types()); got != 2 {
		t.Errorf("Types(): got %d, want 2", got)
	}

	hit := &radar.Hit{RuleID: uuid.New(), Title: "x"}
	res, err := r.Run(context.Background(), hit,
		ActionSpec{Type: "notify", Config: json.RawMessage(`{"foo":"bar"}`)})
	if err != nil {
		t.Fatalf("notify run: %v", err)
	}
	if res["type"] != "notify" {
		t.Errorf("result type: %v", res["type"])
	}
	if len(notify.calls) != 1 {
		t.Errorf("notify call count: %d", len(notify.calls))
	}
	if string(notify.calls[0]) != `{"foo":"bar"}` {
		t.Errorf("config not passed through: %s", string(notify.calls[0]))
	}
}

func TestRunner_UnknownType(t *testing.T) {
	r := NewRunner(&stubAction{typeName: "notify"})
	_, err := r.Run(context.Background(), &radar.Hit{},
		ActionSpec{Type: "ghost"})
	if !errors.Is(err, ErrUnknownType) {
		t.Errorf("expected ErrUnknownType, got %v", err)
	}
}

func TestRunner_DuplicateTypePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("duplicate registration should panic")
		}
	}()
	NewRunner(
		&stubAction{typeName: "notify"},
		&stubAction{typeName: "notify"},
	)
}

func TestParseSpecs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
		err  bool
	}{
		{"nil", "", 0, false},
		{"empty array", "[]", 0, false},
		{"one", `[{"type":"notify"}]`, 1, false},
		{"three", `[{"type":"notify"},{"type":"wiki"},{"type":"task"}]`, 3, false},
		{"with config", `[{"type":"task","config":{"due_offset_days":7}}]`, 1, false},
		{"malformed", `not json`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSpecs([]byte(tc.raw))
			if (err != nil) != tc.err {
				t.Fatalf("err: %v, wantErr %v", err, tc.err)
			}
			if !tc.err && len(got) != tc.want {
				t.Errorf("count: got %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestStubAction_FailurePropagates(t *testing.T) {
	bad := &stubAction{typeName: "wiki", fail: true}
	r := NewRunner(bad)
	_, err := r.Run(context.Background(), &radar.Hit{}, ActionSpec{Type: "wiki"})
	if err == nil || err.Error() != "stub fail" {
		t.Errorf("want 'stub fail', got %v", err)
	}
}
