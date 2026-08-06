// Tool registry for Runtime Agent.
//
// Mirrors apps/cli/biu/internal/tools/tools.go so the Agent loop on
// the server side has the same tool fleet as the CLI: read / write /
// edit / grep / glob / bash, plus memory.recall / memory.store for
// long-term memory access.
//
// Each tool declares a Risk level which Agent's PermissionMode gate
// consults before invoking. See agent.go invokeTool.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Risk classifies a tool's blast radius. The PermissionMode gate uses
// this — not the tool's name — to decide allow / ask / deny so a new
// tool gets sane defaults without touching every Mode.
type Risk int

const (
	RiskLow    Risk = iota // read-only file ops, search, memory recall
	RiskMedium             // mutating but bounded (edit one file, store one memory)
	RiskHigh               // arbitrary write / arbitrary command execution
)

func (r Risk) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	default:
		return "unknown"
	}
}

// Tool is the contract the Agent invokes.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
	Risk        Risk
	// IsReadOnly is kept for backwards compatibility; new code uses Risk.
	IsReadOnly bool
	Invoke     func(ctx context.Context, args map[string]any) (string, error)
}

// Registry holds tools available to the Agent.
type Registry struct {
	tools map[string]*Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]*Tool{}} }

func (r *Registry) Register(t *Tool) { r.tools[t.Name] = t }

func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// HubcSchemas removed in S11-4 — relayclient is gone with publisher.
// 新代码用 Registry.AsBiumindkitTools() 投影到 biumindkit.Tool[]，由
// BuildBiumindkitAgent 喂 ExtraTools。

// DefaultRegistry returns the standard tool fleet (no memory tools —
// those need a MemoryClient and are added by the caller via
// RegisterMemoryTools).
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(readTool())
	r.Register(writeTool())
	r.Register(editTool())
	r.Register(grepTool())
	r.Register(globTool())
	r.Register(bashTool())
	return r
}

// ─── read ───────────────────────────────────────────────

func readTool() *Tool {
	return &Tool{
		Name:        "read",
		Description: "Read a UTF-8 text file from disk. Returns up to 'limit' lines starting at 'offset'.",
		Risk:        RiskLow,
		IsReadOnly:  true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Absolute or relative file path"},
				"offset": map[string]any{"type": "integer", "default": 0},
				"limit":  map[string]any{"type": "integer", "default": 2000},
			},
			"required": []string{"path"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			lines := strings.Split(string(data), "\n")
			off := intArg(args, "offset", 0)
			limit := intArg(args, "limit", 2000)
			if off > len(lines) {
				off = len(lines)
			}
			end := off + limit
			if end > len(lines) {
				end = len(lines)
			}
			var b strings.Builder
			for i, l := range lines[off:end] {
				fmt.Fprintf(&b, "%6d\t%s\n", off+i+1, l)
			}
			return b.String(), nil
		},
	}
}

// ─── write ──────────────────────────────────────────────

func writeTool() *Tool {
	return &Tool{
		Name:        "write",
		Description: "Create or overwrite a file with given content.",
		Risk:        RiskHigh, // arbitrary disk write
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			if path == "" {
				return "", fmt.Errorf("path required")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
		},
	}
}

// ─── edit ───────────────────────────────────────────────

func editTool() *Tool {
	return &Tool{
		Name:        "edit",
		Description: "Replace an exact unique string in a file with a new string.",
		Risk:        RiskMedium, // bounded mutation; pre-existing file
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"old_string": map[string]any{"type": "string"},
				"new_string": map[string]any{"type": "string"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			oldStr, _ := args["old_string"].(string)
			newStr, _ := args["new_string"].(string)
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			s := string(data)
			count := strings.Count(s, oldStr)
			if count == 0 {
				return "", fmt.Errorf("old_string not found in %s", path)
			}
			if count > 1 {
				return "", fmt.Errorf("old_string matches %d times; needs to be unique", count)
			}
			updated := strings.Replace(s, oldStr, newStr, 1)
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Replaced 1 occurrence in %s", path), nil
		},
	}
}

// ─── grep ───────────────────────────────────────────────

func grepTool() *Tool {
	return &Tool{
		Name:        "grep",
		Description: "Search a regex in files (uses ripgrep if installed; falls back to grep).",
		Risk:        RiskLow,
		IsReadOnly:  true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"path":    map[string]any{"type": "string", "default": "."},
			},
			"required": []string{"pattern"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			path, _ := args["path"].(string)
			if path == "" {
				path = "."
			}
			bin := "rg"
			if _, err := exec.LookPath(bin); err != nil {
				bin = "grep"
			}
			var cmd *exec.Cmd
			if bin == "rg" {
				cmd = exec.CommandContext(ctx, "rg", "-n", "--max-count=200", pattern, path)
			} else {
				cmd = exec.CommandContext(ctx, "grep", "-rn", pattern, path)
			}
			out, err := cmd.CombinedOutput()
			if err != nil && len(out) == 0 {
				if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
					return "(no matches)", nil
				}
				return "", err
			}
			return string(out), nil
		},
	}
}

// ─── glob ───────────────────────────────────────────────

func globTool() *Tool {
	return &Tool{
		Name:        "glob",
		Description: "List files matching a glob pattern.",
		Risk:        RiskLow,
		IsReadOnly:  true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
			},
			"required": []string{"pattern"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			pattern, _ := args["pattern"].(string)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return "", err
			}
			if len(matches) == 0 {
				return "(no matches)", nil
			}
			return strings.Join(matches, "\n"), nil
		},
	}
}

// ─── bash ───────────────────────────────────────────────

func bashTool() *Tool {
	return &Tool{
		Name: "bash",
		Description: "Run a shell command. Defaults to a 60 s timeout. " +
			"Operators should run Runtime in a Sandbox-wrapped environment.",
		Risk: RiskHigh, // arbitrary command execution
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmd":         map[string]any{"type": "string"},
				"timeout_sec": map[string]any{"type": "integer", "default": 60},
			},
			"required": []string{"cmd"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			cmd, _ := args["cmd"].(string)
			if cmd == "" {
				return "", fmt.Errorf("cmd required")
			}
			timeout := intArg(args, "timeout_sec", 60)
			tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			out, err := exec.CommandContext(tctx, "/bin/sh", "-c", cmd).CombinedOutput()
			if err != nil {
				return string(out), fmt.Errorf("exit: %w; output:\n%s", err, string(out))
			}
			return string(out), nil
		},
	}
}

// ─── memory tools (registered separately by RegisterMemoryTools) ─

// RegisterMemoryTools adds memory.recall + memory.store tools backed by
// the supplied MemoryClient. Both are project-scoped — the Agent passes
// the active project_id from RunInput so the LLM doesn't have to thread
// it through every call (and can't read a different project's memories
// even by hallucination).
func RegisterMemoryTools(r *Registry, mem MemoryClient, projectID string) {
	if mem == nil || projectID == "" {
		return
	}
	r.Register(memoryRecallTool(mem, projectID))
	r.Register(memoryStoreTool(mem, projectID))
}

// MemoryClient is the small slice of Brain.Memory the Agent needs.
// Implemented by services/runtime/internal/memclient.Client.
type MemoryClient interface {
	Recall(ctx context.Context, projectID, query string, limit int) ([]MemoryHit, error)
	Store(ctx context.Context, projectID, kind, content string, salience float32) error
}

// MemoryHit is one row from a recall response.
type MemoryHit struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Content  string  `json:"content"`
	Salience float32 `json:"salience"`
	Score    float32 `json:"score"`
}

func memoryRecallTool(mem MemoryClient, projectID string) *Tool {
	return &Tool{
		Name: "memory.recall",
		Description: "Search the user's long-term memories for facts relevant " +
			"to the query. Returns up to 'limit' hits ranked by hybrid " +
			"semantic + lexical score.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "default": 5},
			},
			"required": []string{"query"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			q, _ := args["query"].(string)
			if q == "" {
				return "", fmt.Errorf("query required")
			}
			limit := intArg(args, "limit", 5)
			hits, err := mem.Recall(ctx, projectID, q, limit)
			if err != nil {
				return "", fmt.Errorf("memory recall: %w", err)
			}
			if len(hits) == 0 {
				return "(no memories)", nil
			}
			b, _ := json.MarshalIndent(hits, "", "  ")
			return string(b), nil
		},
	}
}

func memoryStoreTool(mem MemoryClient, projectID string) *Tool {
	return &Tool{
		Name: "memory.store",
		Description: "Persist a fact in the user's long-term memory. Use sparingly: " +
			"only for facts the user explicitly asked you to remember, or strong " +
			"preferences likely to recur. kind ∈ {recall, preference, habit}. " +
			"('habit' was previously called 'skill' — input still accepted as a " +
			"deprecated alias until 2026-08-25; do not use it in new code.)",
		Risk: RiskMedium, // mutates user state but bounded to one row
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content":  map[string]any{"type": "string"},
				"kind":     map[string]any{"type": "string", "enum": []string{"recall", "preference", "habit"}, "default": "recall"},
				"salience": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": 0.5},
			},
			"required": []string{"content"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			content, _ := args["content"].(string)
			if strings.TrimSpace(content) == "" {
				return "", fmt.Errorf("content required")
			}
			kind, _ := args["kind"].(string)
			if kind == "" {
				kind = "recall"
			}
			var sal float32 = 0.5
			if s, ok := args["salience"].(float64); ok {
				sal = float32(s)
			}
			if err := mem.Store(ctx, projectID, kind, content, sal); err != nil {
				return "", fmt.Errorf("memory store: %w", err)
			}
			return "stored", nil
		},
	}
}

// ─── helpers ────────────────────────────────────────────

func intArg(args map[string]any, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}
