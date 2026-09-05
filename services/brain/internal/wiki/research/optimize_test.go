// Optimize endpoint unit tests — 纯解析函数 + mock LLMCaller，无需 DB。

package research

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseOptimizeOutput(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		fallback    string
		wantTopic   string
		wantQueries []string
		wantErr     bool
	}{
		{
			"plain json",
			`{"topic": "精确话题", "queries": ["q1", "q2", "q3"]}`,
			"raw",
			"精确话题",
			[]string{"q1", "q2", "q3"},
			false,
		},
		{
			"fenced json tolerated",
			"```json\n{\"topic\": \"t\", \"queries\": [\"a\"]}\n```",
			"raw",
			"t",
			[]string{"a"},
			false,
		},
		{
			"think block stripped",
			`<think>scratch</think>{"topic": "t", "queries": ["a", "b"]}`,
			"raw",
			"t",
			[]string{"a", "b"},
			false,
		},
		{
			"empty topic falls back to user input",
			`{"topic": "  ", "queries": ["a"]}`,
			"用户原话",
			"用户原话",
			[]string{"a"},
			false,
		},
		{
			"queries clamped to 5 and blanks filtered",
			`{"topic": "t", "queries": ["1", "", "2", "3", " ", "4", "5", "6"]}`,
			"raw",
			"t",
			[]string{"1", "2", "3", "4", "5"},
			false,
		},
		{
			"no usable query falls back to [topic]",
			`{"topic": "t", "queries": []}`,
			"raw",
			"t",
			[]string{"t"},
			false,
		},
		{
			"garbage is an error",
			`not json at all`,
			"raw",
			"",
			nil,
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseOptimizeOutput(c.raw, c.fallback)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Topic != c.wantTopic {
				t.Errorf("topic = %q, want %q", got.Topic, c.wantTopic)
			}
			if strings.Join(got.Queries, "|") != strings.Join(c.wantQueries, "|") {
				t.Errorf("queries = %v, want %v", got.Queries, c.wantQueries)
			}
		})
	}
}

type fakeLLM struct {
	resp string
	err  error
	// captured for assertion
	gotOwner  uuid.UUID
	gotSystem string
	gotUser   string
}

func (f *fakeLLM) Chat(ctx context.Context, ownerID uuid.UUID, system, user string) (string, error) {
	f.gotOwner, f.gotSystem, f.gotUser = ownerID, system, user
	return f.resp, f.err
}

// TestOptimizeTopic — Orchestrator 版：LLM 输出解析成 {topic, queries}，
// owner 透传给 caller（计费归 owner）；LLM 错误 / 无 caller 都是 error。
func TestOptimizeTopic(t *testing.T) {
	owner := uuid.New()
	llm := &fakeLLM{resp: `{"topic": "规范话题", "queries": ["q1", "q2", "q3"]}`}
	o := NewOrchestrator(nil, nil, nil, nil, llm, Config{})

	out, err := o.OptimizeTopic(context.Background(), owner, "一句话话题")
	if err != nil {
		t.Fatalf("OptimizeTopic: %v", err)
	}
	if out.Topic != "规范话题" || len(out.Queries) != 3 {
		t.Errorf("got %+v", out)
	}
	if llm.gotOwner != owner {
		t.Errorf("owner = %v, want %v (billing attribution)", llm.gotOwner, owner)
	}
	if !strings.Contains(llm.gotUser, "一句话话题") {
		t.Errorf("user prompt missing raw topic: %q", llm.gotUser)
	}

	// LLM 失败 → error（handler 映射 502）。
	llmErr := &fakeLLM{err: errors.New("model-relay 502")}
	o2 := NewOrchestrator(nil, nil, nil, nil, llmErr, Config{})
	if _, err := o2.OptimizeTopic(context.Background(), owner, "x"); err == nil {
		t.Fatal("expected error from failing LLM, got nil")
	}

	// 无 caller → error（handler 前置 503 之外的兜底）。
	o3 := NewOrchestrator(nil, nil, nil, nil, nil, Config{})
	if _, err := o3.OptimizeTopic(context.Background(), owner, "x"); err == nil {
		t.Fatal("expected error with nil LLM, got nil")
	}
}

// TestOptimizeSystemPromptLanguageGate — optimize prompt 必须带输出语言
// 指令（跟随用户输入语言）。
func TestOptimizeSystemPromptLanguageGate(t *testing.T) {
	if !strings.Contains(optimizeSystemPrompt, "MANDATORY OUTPUT LANGUAGE") {
		t.Fatal("optimizeSystemPrompt missing mandatory output-language section")
	}
	if !strings.Contains(optimizeSystemPrompt, "same language as the user's input") {
		t.Fatal("optimizeSystemPrompt missing language-following instruction")
	}
}
