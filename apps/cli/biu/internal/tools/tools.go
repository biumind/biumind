// Package tools provides built-in tools the Agent can invoke.
//
// MVP: read / write / edit / grep / glob / bash (file-bound).
package tools

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

// Tool descriptor.
type Tool struct {
	Name              string
	Description       string
	IsReadOnly        bool
	IsDestructive     bool
	IsConcurrencySafe bool
	Schema            map[string]any
	Invoke            func(ctx context.Context, args map[string]any) (string, error)
}

// Registry holds all configured tools.
type Registry struct {
	tools map[string]*Tool
}

func NewRegistry() *Registry         { return &Registry{tools: map[string]*Tool{}} }
func (r *Registry) Register(t *Tool) { r.tools[t.Name] = t }
func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// Defaults returns the standard MVP tool registry.
func Defaults() *Registry {
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
		Name: "read", Description: "Read a UTF-8 text file from disk",
		IsReadOnly: true, IsConcurrencySafe: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
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
			off, _ := args["offset"].(float64)
			limit := 2000.0
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = l
			}
			start := int(off)
			if start > len(lines) {
				start = len(lines)
			}
			end := start + int(limit)
			if end > len(lines) {
				end = len(lines)
			}
			var b strings.Builder
			for i, l := range lines[start:end] {
				fmt.Fprintf(&b, "%6d\t%s\n", start+i+1, l)
			}
			return b.String(), nil
		},
	}
}

// ─── write ──────────────────────────────────────────────

func writeTool() *Tool {
	return &Tool{
		Name: "write", Description: "Create or overwrite a file with given content",
		IsDestructive: true,
		Schema: map[string]any{
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
		Name: "edit", Description: "Replace exact string in a file",
		Schema: map[string]any{
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
		Name: "grep", Description: "Search regex in files (uses ripgrep if installed)",
		IsReadOnly: true, IsConcurrencySafe: true,
		Schema: map[string]any{
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
			// rg returns exit 1 when no matches — not an error
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
		Name: "glob", Description: "List files matching a glob pattern",
		IsReadOnly: true, IsConcurrencySafe: true,
		Schema: map[string]any{
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
		Name: "bash", Description: "Run a shell command (sandbox-wrapped where supported)",
		IsDestructive: true,
		Schema: map[string]any{
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
			timeout := 60
			if t, ok := args["timeout_sec"].(float64); ok && t > 0 {
				timeout = int(t)
			}
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

// EncodeArgs is a helper used by the REPL when echoing tool calls.
func EncodeArgs(a map[string]any) string {
	b, _ := json.Marshal(a)
	return string(b)
}
