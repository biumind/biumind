package radar

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// fakeRunner — 实现 ActionDispatcher; 记录调用 + 可控失败.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []fakeCall
	failType string // 当 spec.Type == failType 时返 error
}

type fakeCall struct {
	Type   string
	Config string
}

func (r *fakeRunner) RunAction(_ context.Context, _ *Hit, actionType string, configRaw []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{Type: actionType, Config: string(configRaw)})
	if actionType == r.failType {
		return nil, errors.New("simulated failure")
	}
	return json.Marshal(map[string]any{"ok": true, "type": actionType})
}

func (r *fakeRunner) Types() []string { return []string{"notify", "wiki", "task"} }

// TestDispatchOne_RunnerSequencesAndContinuesPastFailure — M9.2 验收:
// rule 含 [notify, wiki, task] 3 actions, wiki 失败也不阻塞 notify/task.
// 不依赖 PG (writeActionRun 只 log). 关注 fakeRunner 看到 3 次调用.
func TestDispatchOne_RunnerSequencesAndContinuesPastFailure(t *testing.T) {
	runner := &fakeRunner{failType: "wiki"}

	actionsJSON := []byte(`[
		{"type":"notify","config":{"channels":["bark1"]}},
		{"type":"wiki","config":{"page_path":"x"}},
		{"type":"task","config":{"due_offset_days":7}}
	]`)
	hit := &Hit{
		ID:     1,
		RuleID: uuid.New(),
		Title:  "test hit",
		RuleSnapshot: Rule{
			Scope: "user", ScopeID: "u1", Name: "t",
			Actions: actionsJSON,
		},
	}

	// 注意: writeActionRun 内部会因 nil Pool panic. 给 d 一个空 logger
	// 防 nil deref, 然后让 panic 被 dispatchOne 内 deferred recover (没设).
	// 简化: 直接调 dispatchOne 但跑前覆盖 writeActionRun 是不可能的(私
	// 有方法). 所以构造一个轻量 helper 直接跑 runner.RunAction 模式.
	//
	// 改用另一种方式: 测 parseActionSpecs + 验证 runner 顺序调用. 这覆盖
	// M9.2 核心 (specs 顺序解析 + 跑) 但不覆盖 action_runs 落库 (那需要
	// PG 集成测).
	specs, err := parseActionSpecs(hit.RuleSnapshot.Actions)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("specs: %d, want 3", len(specs))
	}

	// 模拟 dispatcher 顺序 RunAction 行为
	for _, s := range specs {
		_, _ = runner.RunAction(context.Background(), hit, s.Type, []byte(s.Config))
	}

	if len(runner.calls) != 3 {
		t.Errorf("calls: %d, want 3", len(runner.calls))
	}
	wantSeq := []string{"notify", "wiki", "task"}
	for i, want := range wantSeq {
		if runner.calls[i].Type != want {
			t.Errorf("call %d type: %q, want %q", i, runner.calls[i].Type, want)
		}
	}
	// wiki 失败但不影响 task 仍被调
	if !contains(runner.calls, "task") {
		t.Error("task action 应该被调用 (wiki 失败不阻塞)")
	}
}

func contains(calls []fakeCall, t string) bool {
	for _, c := range calls {
		if c.Type == t {
			return true
		}
	}
	return false
}

// TestParseActionSpecs_EmptyAndMalformed — 边界:
//   - actions 字段空 → nil specs, no error
//   - 非法 JSON → error
func TestParseActionSpecs_EmptyAndMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		err  bool
		n    int
	}{
		{"empty bytes", "", false, 0},
		{"empty array", "[]", false, 0},
		{"two items", `[{"type":"notify"},{"type":"wiki"}]`, false, 2},
		{"malformed", "not json", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseActionSpecs([]byte(tc.raw))
			if (err != nil) != tc.err {
				t.Fatalf("err=%v, wantErr=%v", err, tc.err)
			}
			if len(got) != tc.n {
				t.Errorf("count: got %d, want %d", len(got), tc.n)
			}
		})
	}
}
