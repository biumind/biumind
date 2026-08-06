// biu smoke test driver — exercises the full agent loop against a
// real Anthropic-compatible endpoint.
//
// Configure via env (matches Anthropic's official client conventions):
//
//   ANTHROPIC_API_KEY    — required
//   ANTHROPIC_BASE_URL   — defaults to https://api.anthropic.com
//   ANTHROPIC_MODEL      — defaults to claude-sonnet-4-6
//   ANTHROPIC_MODEL_ALT  — optional; A19 model-override case skips when unset
//   SMOKETEST_FILTER     — optional substring; only matching cases run
//
// Each scenario reports:
//   - elapsed time
//   - input / output / cache tokens
//   - any tool calls fired
//   - assistant final text (truncated)
//   - PASS / FAIL / SKIP
//
// Exit code: number of failed scenarios (so CI can gate on it).
//
// Per P20.47 v2: BypassPermissions is OFF by default — each scenario
// goes through the real PermissionPolicy code path. The default policy
// is PermissionAllow; deny-flow scenarios install PermissionDeny
// explicitly so the engine sees a real "deny" decision.
//
// Layout: scenarios live in scenarios_<layer>.go files (one per Layer
// in P20.47 v2 plan). Each file's init() appends to the global
// scenarios slice via register(). main.go is just the runner.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fail("ANTHROPIC_API_KEY not set")
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	filter := os.Getenv("SMOKETEST_FILTER")

	fmt.Printf("smoketest: endpoint=%s model=%s scenarios=%d\n",
		or(baseURL, "(default)"), model, len(scenarios))
	if filter != "" {
		fmt.Printf("smoketest: filter=%q\n", filter)
	}
	fmt.Println(strings.Repeat("─", 78))

	results := make([]result, 0, len(scenarios))
	for _, s := range scenarios {
		if filter != "" && !strings.Contains(s.name, filter) {
			continue
		}
		results = append(results, runScenario(s, apiKey, baseURL, model))
	}

	fmt.Println(strings.Repeat("─", 78))
	pass, fail, skip := 0, 0, 0
	for _, r := range results {
		marker := "✓"
		switch {
		case r.skip:
			marker = "○"
			skip++
		case !r.pass:
			marker = "✗"
			fail++
		default:
			pass++
		}
		fmt.Printf("%s  %-32s %5dms  in=%-5d out=%-5d  cR=%-5d cW=%-5d  %s\n",
			marker, r.scenario, r.elapsed.Milliseconds(),
			r.inTokens, r.outTokens,
			r.cacheReadTks, r.cacheWriteTk,
			strings.Join(r.toolsFired, ","))
		switch {
		case r.skip:
			fmt.Printf("    skip: %s\n", r.skipReason)
		case !r.pass:
			fmt.Printf("    err: %v\n", r.err)
			if r.preview != "" {
				fmt.Printf("    last assistant text: %s\n", r.preview)
			}
			for _, ti := range r.toolInputs {
				fmt.Printf("    tool: %s\n", ti)
			}
		}
	}
	fmt.Printf("\n%d passed, %d failed, %d skipped\n", pass, fail, skip)
	os.Exit(fail)
}

func runScenario(s scenario, apiKey, baseURL, defaultModel string) result {
	r := result{scenario: s.name}

	if s.skipReason != nil {
		if reason := s.skipReason(); reason != "" {
			r.skip = true
			r.skipReason = reason
			return r
		}
	}

	dir, err := os.MkdirTemp("", "biu-smoke-*")
	if err != nil {
		r.err = err
		return r
	}
	defer os.RemoveAll(dir)
	if s.prep != nil {
		if err := s.prep(dir); err != nil {
			r.err = fmt.Errorf("prep: %w", err)
			return r
		}
	}

	model := defaultModel
	if s.modelOverride != "" {
		model = s.modelOverride
	}
	r.model = model

	policy := s.policy
	if policy == nil {
		policy = biumindkit.PermissionAllow()
	}

	memMode := biumindkit.NoMemory
	if s.loadMemory {
		memMode = biumindkit.AutoMemory
	}

	settingsMode := biumindkit.NoSettings
	if s.loadSettings {
		settingsMode = biumindkit.AutoSettings
	}

	maxTok := s.maxTokens
	if maxTok == 0 {
		maxTok = 4096
	}

	permMode := s.permissionMode

	agent, err := biumindkit.New(biumindkit.Options{
		APIKey:              apiKey,
		AnthropicEndpoint:   baseURL,
		Model:               model,
		Cwd:                 dir,
		System:              s.system,
		PermissionMode:      permMode,
		LoadProjectMemory:   memMode,
		LoadProjectSettings: settingsMode,
		PermissionPolicy:    policy,
		ExtraTools:          s.extraTools,
		MaxTokens:           maxTok,
	})
	if err != nil {
		r.err = err
		return r
	}
	// We DON'T defer Close here — the close path fires SessionEnd
	// hooks, which Layer K's K8 case needs to observe BEFORE
	// assertText runs. Explicit Close after the event loop ensures
	// any side-effects (markers, log writes) land first.
	closed := false
	defer func() {
		if !closed {
			_ = agent.Close()
		}
	}()

	timeout := s.timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	for ev := range agent.Submit(ctx, s.prompt) {
		switch e := ev.(type) {
		case biumindkit.ToolStart:
			r.toolsFired = append(r.toolsFired, e.Name)
			r.toolInputs = append(r.toolInputs, summarizeInput(e.Name, e.Input))
		case biumindkit.AssistantText:
			r.preview = truncate(e.Text, 400)
		case biumindkit.Error:
			r.err = e.Err
		case biumindkit.Done:
			r.inTokens = e.InputTokens
			r.outTokens = e.OutputTokens
			r.cacheReadTks = e.CacheReadTokens
			r.cacheWriteTk = e.CacheWriteTokens
		}
	}
	r.elapsed = time.Since(start)

	// Close NOW so SessionEnd hooks fire before assertText runs.
	// Errors here don't fail the scenario — they'd be hook config
	// problems orthogonal to whatever the case is asserting.
	_ = agent.Close()
	closed = true

	if r.err != nil {
		return r
	}
	if len(s.wantTools) > 0 {
		fired := map[string]bool{}
		for _, t := range r.toolsFired {
			fired[strings.ToLower(t)] = true
		}
		hit := false
		for _, want := range s.wantTools {
			if fired[strings.ToLower(want)] {
				hit = true
				break
			}
		}
		if !hit {
			r.err = fmt.Errorf("expected one of %v to fire; got %v", s.wantTools, r.toolsFired)
			return r
		}
	}
	for _, banned := range s.bannedTools {
		for _, fired := range r.toolsFired {
			if strings.EqualFold(fired, banned) {
				r.err = fmt.Errorf("forbidden tool %q fired", banned)
				return r
			}
		}
	}
	if r.preview == "" && !s.allowEmptyText {
		r.err = fmt.Errorf("assistant produced no text")
		return r
	}
	if s.assertText != nil {
		if err := s.assertText(r.preview); err != nil {
			r.err = err
			return r
		}
	}
	r.pass = true
	return r
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// summarizeInput builds a one-liner "<tool>(<key>=<value>, ...)" for
// debugging — keeps the most informative fields per tool, truncates
// long values so the log stays readable.
func summarizeInput(name string, input map[string]any) string {
	if len(input) == 0 {
		return name + "()"
	}
	keys := []string{"command", "file_path", "pattern", "path", "url",
		"prompt", "subagent_type", "old_string", "new_string", "content",
		"subject", "status", "taskId", "skill", "cell_index"}
	parts := []string{}
	for _, k := range keys {
		v, ok := input[k]
		if !ok {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "…"
		}
		s = strings.ReplaceAll(s, "\n", "\\n")
		parts = append(parts, fmt.Sprintf("%s=%q", k, s))
	}
	if len(parts) == 0 {
		// Fall back: show first key/value pair.
		for k, v := range input {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:57] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s=%q", k, s))
			break
		}
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(parts, ", "))
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "smoketest:", msg)
	os.Exit(2)
}
