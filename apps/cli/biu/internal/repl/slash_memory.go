// /memory + /remember slash handlers. Wraps the memory + auto-
// memory layers with REPL-friendly status / reload / append flows.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/memory"
)

// memoryStatusNote summarises both memory layers. Shown for `/memory`
// (no args) so the user can see at a glance which BIUMIND.md files
// were picked up AND whether their auto-memory directory has an
// index. The "primer is always active" line tells them the model
// can always write a new memory even without an existing index.
func (m model) memoryStatusNote() string {
	var b strings.Builder
	if len(m.memoryFiles) == 0 {
		b.WriteString("BIUMIND.md: none loaded\n")
	} else {
		b.WriteString("BIUMIND.md (loaded):\n")
		for _, f := range m.memoryFiles {
			fmt.Fprintf(&b, "  [%s] %s (%d chars)\n", f.Source, f.Path, len(f.Content))
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		auto := memory.LoadAuto(home)
		if auto.Exists() {
			fmt.Fprintf(&b, "\nauto-memory: %s (%d chars",
				auto.IndexPath, len(auto.IndexContent))
			if auto.LineTruncated {
				b.WriteString(", truncated to 200 lines")
			}
			if auto.ByteTruncated {
				b.WriteString(", truncated to 25 KB")
			}
			b.WriteString(")")
		} else {
			fmt.Fprintf(&b, "\nauto-memory: primer active, %s does not exist yet — model can create it",
				auto.IndexPath)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// reloadMemory re-reads BIUMIND.md + MEMORY.md and rewrites the
// engine's system prompt in place. Returns a status line for the UI.
//
// We rebuild the prompt from m.system (the user's --system flag, or
// empty) so reloading doesn't double-stack the memory blocks. Skills
// are NOT re-attached here; their auto-attach is path-based and
// stable across the session.
func (m *model) reloadMemory() string {
	if m.engine == nil {
		return "/memory reload: requires engine path"
	}
	cwd, _ := os.Getwd()
	mem := memory.Load(cwd)
	m.memoryFiles = mem.Files

	system := m.system
	if memSys := mem.SystemPrompt(); memSys != "" {
		if system == "" {
			system = memSys
		} else {
			system = system + "\n\n" + memSys
		}
	}
	autoSummary := "no auto-memory"
	if home, err := os.UserHomeDir(); err == nil {
		auto := memory.LoadAuto(home)
		if autoSys := auto.SystemPrompt(); autoSys != "" {
			if system == "" {
				system = autoSys
			} else {
				system = system + "\n\n" + autoSys
			}
			if auto.Exists() {
				autoSummary = fmt.Sprintf("auto-memory: %d chars", len(auto.IndexContent))
			} else {
				autoSummary = "auto-memory primer (no index yet)"
			}
		}
	}
	m.engine.SetSystem(system)
	return fmt.Sprintf("/memory reload: BIUMIND.md=%d files; %s",
		len(mem.Files), autoSummary)
}

// handleRemember parses `[-t <type>] <text>` and writes a memory file
// to ~/.biumind/memory/. Default type is `user`. After a successful
// write the engine's system prompt is rebuilt in place so the just-
// saved entry is visible to the model on the very next turn — same
// shape as /memory reload but scoped to one append.
//
// Returns a status line for the system note. Any parse / write error
// is also returned as a status line (no exception bubble) so the
// REPL stays predictable.
func (m *model) handleRemember(rest string) string {
	if rest == "" {
		return "usage: /remember [-t user|feedback|project|reference] <text>"
	}
	memType, body, err := parseRememberArgs(rest)
	if err != nil {
		return "/remember: " + err.Error()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "/remember: cannot resolve $HOME: " + err.Error()
	}
	auto := memory.LoadAuto(home)
	res, err := auto.Append(memType, "", "", body)
	if err != nil {
		return "/remember: " + err.Error()
	}

	// Rebuild the engine system prompt so the new entry is visible
	// to the model immediately. We only do this when an engine is
	// wired — the headless / no-engine path simply persists the file.
	if m.engine != nil {
		_ = m.reloadMemory()
	}
	return fmt.Sprintf("/remember: saved %s memory → %s",
		memType, filepath.Base(res.FilePath))
}

// parseRememberArgs splits the argument tail of `/remember` into
// (type, body). Recognises a leading `-t <type>` or `--type <type>`
// flag; everything after is treated as a single body string. We
// don't pull in spf13/pflag for one option — keeps the parser
// dependency-light and the syntax obvious.
func parseRememberArgs(rest string) (memory.MemoryType, string, error) {
	rest = strings.TrimSpace(rest)
	memType := memory.TypeUser // sane default — most quick captures
	for {
		// Recognise both `-t feedback` and `--type=feedback` shapes.
		switch {
		case strings.HasPrefix(rest, "--type="):
			val, body, _ := strings.Cut(strings.TrimPrefix(rest, "--type="), " ")
			t, ok := memory.ParseMemoryType(val)
			if !ok {
				return "", "", fmt.Errorf("invalid type %q (want one of: user, feedback, project, reference)", val)
			}
			memType = t
			rest = strings.TrimSpace(body)
		case strings.HasPrefix(rest, "--type "), strings.HasPrefix(rest, "-t "):
			// Strip the flag, then split off the value as the next token.
			rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(rest, "--type"), "-t"))
			val, body, _ := strings.Cut(rest, " ")
			t, ok := memory.ParseMemoryType(val)
			if !ok {
				return "", "", fmt.Errorf("invalid type %q (want one of: user, feedback, project, reference)", val)
			}
			memType = t
			rest = strings.TrimSpace(body)
		default:
			break
		}
		// One pass max — the loop is just for the switch's clean
		// fall-through structure, not for repeated flags.
		break
	}
	if rest == "" {
		return "", "", fmt.Errorf("body is required after the type flag")
	}
	return memType, rest, nil
}
