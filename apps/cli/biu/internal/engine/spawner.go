// Sub-agent spawner.
//
// AgentTool calls this to fan out work into a nested QueryEngine that
// shares the parent's Provider / Tools / Permissions but owns its own
// AppState and budget.
//
// Design choices:
//
//   * Same goroutine model — Spawn is synchronous, blocks until the
//     sub-agent's Submit channel closes. Streaming events go to a
//     callback so callers can render `⏺ sub: ...` rows.
//   * Sub-agent gets a fresh State so its history doesn't pollute the
//     parent.
//   * AgentID is propagated, so per-agent tools (TodoWrite,
//     memory) can scope themselves.
//   * MaxToolTurns is forwarded; sub-agents can be capped tighter
//     via SubAgentToolBudget.

package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// SpawnerOptions configures the parent engine's ability to fan out
// sub-agents. Defaults are fine for most cases.
type SpawnerOptions struct {
	// SubAgentToolBudget caps how many tool loops a sub-agent gets.
	// 0 = inherit parent's MaxToolTurns.
	SubAgentToolBudget int
}

// agentCounter assigns short string IDs to sub-agents ("agent-1",
// "agent-2", ...). Process-wide unique.
var agentCounter int64

// engineSpawner is the AgentSpawner backed by the parent QueryEngine.
type engineSpawner struct {
	parent  *QueryEngine
	options SpawnerOptions
}

// NewSpawner returns the spawner the parent engine hands to its
// tools' ToolEnv. Wires AgentSpawnRequest → fresh QueryEngine →
// drained event channel → AgentSpawnResult.
func NewSpawner(parent *QueryEngine, opt SpawnerOptions) AgentSpawner {
	return &engineSpawner{parent: parent, options: opt}
}

func (s *engineSpawner) Spawn(ctx context.Context, req AgentSpawnRequest) (*AgentSpawnResult, error) {
	id := nextAgentID()
	started := time.Now()

	sub, err := s.newSubEngine(req, id)
	if err != nil {
		return nil, err
	}

	// Fire SubagentStart on the parent's hook registry (P20.55).
	// Useful for "log every dispatch" / "ping when /ultraplan starts".
	if s.parent != nil && s.parent.hooks != nil &&
		s.parent.hooks.Has(hooks.EventSubagentStart) {
		hooks.Run(ctx,
			s.parent.hooks.For(hooks.EventSubagentStart, req.AgentType),
			hooks.EventSubagentStart,
			map[string]any{
				"session_id":  s.parent.agentID,
				"agent_id":    id,
				"agent_type":  req.AgentType,
				"description": req.Description,
				"prompt":      req.Prompt,
			})
	}

	subOut := s.runSubmit(ctx, sub, req.Prompt, "user")
	out := &AgentSpawnResult{
		Output:       subOut.Output,
		StopReason:   subOut.StopReason,
		InputTokens:  subOut.InputTokens,
		OutputTokens: subOut.OutputTokens,
	}
	if subOut.Err != nil {
		// Spawn's contract returns errors out-of-band; the
		// async-spawner path uses the (Output, Err) shape via runSubmit.
		out.Output = ""
	}
	out.Elapsed = time.Since(started)

	// Fire SubagentStop on the parent's hook registry. Useful for
	// users wiring "log every sub-agent run" or "ping me when
	// /ultraplan finishes" hooks. We fire on the parent because the
	// child has its own (forked) hooks state but the user-configured
	// hooks live at session level.
	if s.parent != nil && s.parent.hooks != nil &&
		s.parent.hooks.Has(hooks.EventSubagentStop) {
		hooks.Run(ctx,
			s.parent.hooks.For(hooks.EventSubagentStop, req.AgentType),
			hooks.EventSubagentStop,
			map[string]any{
				"session_id":  s.parent.agentID,
				"agent_id":    id,
				"agent_type":  req.AgentType,
				"description": req.Description,
				"prompt":      req.Prompt,
				"output":      out.Output,
				"stop_reason": out.StopReason,
				"elapsed_ms":  out.Elapsed.Milliseconds(),
			})
	}

	return out, nil
}

// newSubEngine builds a fresh QueryEngine for one teammate. Pulled
// out of Spawn() so SpawnAsync can reuse the same sub-engine across
// follow-up Submits queued by SendMessage (P20.53-2): each Submit
// appends to the sub-engine's AppState, preserving conversation
// history. Without this split the teammate would forget context
// between the original prompt and any follow-up.
func (s *engineSpawner) newSubEngine(req AgentSpawnRequest, agentID string) (*QueryEngine, error) {
	subState := state.New()

	budget := s.options.SubAgentToolBudget
	if budget == 0 {
		budget = s.parent.maxToolTurns
	}
	if req.MaxTurns > 0 {
		budget = req.MaxTurns
	}
	system := req.System
	if system == "" {
		system = s.parent.system
	}
	model := s.parent.model
	if req.Model != "" && req.Model != "inherit" {
		model = req.Model
	}
	subPerms := s.parent.perms
	if req.PermissionMode != "" {
		subPerms = forkPerms(s.parent.perms, permissions.ModeFromString(req.PermissionMode))
	}
	subTools := s.parent.tools
	if len(req.AllowedTools) > 0 || len(req.DisallowedTools) > 0 {
		subTools = filterToolRegistry(s.parent.tools, req.AllowedTools, req.DisallowedTools)
	}
	return New(Options{
		State:           subState,
		Tools:           subTools,
		Provider:        s.parent.provider,
		Model:           model,
		System:          system,
		Cwd:             s.parent.cwd,
		MaxToolTurns:    budget,
		MaxTokens:       s.parent.maxTokens,
		Permissions:     subPerms,
		Hooks:           s.parent.hooks,
		Cost:            s.parent.cost,
		AgentID:         agentID,
		ParentToolUseID: req.ParentToolUseID,
	})
}

// submitOutcome is the per-Submit return shape used by both Spawn
// (synchronous) and SpawnAsync (loop with follow-ups). Carries the
// fields runSubmit's caller might need without forcing one shape on
// the other.
type submitOutcome struct {
	Output       string
	StopReason   string
	InputTokens  int
	OutputTokens int
	Err          error
}

// runSubmit drives one Submit cycle on the sub-engine, draining its
// event channel into the outcome shape Spawn / SpawnAsync need. The
// "from" parameter lets SpawnAsync prefix follow-up messages with a
// system note ("Teammate received message from <from>: …"); for the
// initial user prompt this is "user" and the prompt is passed
// through unchanged.
func (s *engineSpawner) runSubmit(ctx context.Context, sub *QueryEngine, prompt, from string) submitOutcome {
	if from != "" && from != "user" {
		// Inject a system breadcrumb so the teammate knows where this
		// message came from. The actual user-role prompt is the body
		// itself; the system note tags provenance.
		sub.state.AppendMessage(state.Message{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: "<message-from sender=\"" + from + "\"/>",
			}},
		})
	}
	out := submitOutcome{}
	ch := sub.Submit(ctx, prompt)
	for ev := range ch {
		switch e := ev.(type) {
		case *DoneEvent:
			out.StopReason = e.StopReason
			out.InputTokens = e.InputTokens
			out.OutputTokens = e.OutputTokens
		case *AssistantMessageEvent:
			out.Output = extractText(e.Message)
		case *ErrorEvent:
			if !e.Recoverable {
				out.Err = e.Err
			}
		}
	}
	return out
}

func nextAgentID() string {
	n := atomic.AddInt64(&agentCounter, 1)
	return formatAgentID(n)
}

func formatAgentID(n int64) string {
	// Simple, deterministic, easy to spot in logs.
	return "agent-" + itoa(n)
}

// itoa avoids pulling strconv into this file (already imported in
// other engine files; this duplicates intentionally to keep spawner
// imports minimal). Same shape as strconv.Itoa.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// forkPerms builds a sub-agent permission context that carries the
// parent's rules but uses a different mode. We don't deep-clone the
// rule maps — engine writers only call SetMode on the parent, and
// rules are append-only via settings layers. Sharing rule slices is
// safe; sharing the mutex is not.
func forkPerms(parent *permissions.Context, mode permissions.Mode) *permissions.Context {
	if parent == nil {
		c := permissions.NewContext()
		c.SetMode(mode)
		return c
	}
	c := permissions.NewContext()
	// Copy each rule bucket across so deny / ask / allow still apply
	// to the sub-agent. Iterate via AllRules to flatten across sources.
	for _, b := range []permissions.Behavior{
		permissions.BehaviorDeny,
		permissions.BehaviorAsk,
		permissions.BehaviorAllow,
	} {
		bySrc := map[permissions.Source][]string{}
		for _, r := range parent.AllRules(b) {
			bySrc[r.Source] = append(bySrc[r.Source], r.Value.String())
		}
		for src, rules := range bySrc {
			c.AddRules(src, b, rules)
		}
	}
	// Carry over additional working directories + originalCwd so the
	// sub-agent's file-tool gate + Bash sandbox sees the same set
	// the parent does. Without this, /add-dir at the parent would
	// not propagate when the sub-agent overrides PermissionMode (the
	// other branch already shares the parent's ctx pointer outright).
	c.SetOriginalCwd(parent.OriginalCwd())
	bySrc := map[permissions.Source][]string{}
	for _, d := range parent.AdditionalDirectories() {
		bySrc[d.Source] = append(bySrc[d.Source], d.Path)
	}
	for src, paths := range bySrc {
		c.AddDirectories(src, paths)
	}
	c.SetMode(mode)
	return c
}

// filterToolRegistry returns a registry view restricted by allow /
// deny lists. Allow empty = no whitelist; both apply additively (a
// tool listed in deny is excluded even if also in allow).
func filterToolRegistry(parent ToolRegistry, allow, deny []string) ToolRegistry {
	allowSet := map[string]bool{}
	for _, n := range allow {
		allowSet[n] = true
	}
	denySet := map[string]bool{}
	for _, n := range deny {
		denySet[n] = true
	}
	out := NewRegistry()
	for _, t := range parent.List() {
		name := t.Name()
		if denySet[name] {
			continue
		}
		if len(allowSet) > 0 && !allowSet[name] {
			continue
		}
		out.Register(t)
	}
	return out
}

// extractText flattens an assistant message into a single string,
// joining all text content blocks. Tool-use blocks are skipped — the
// parent only wants the model's prose answer.
func extractText(m state.Message) string {
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type != state.ContentText {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}
