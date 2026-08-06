// Memory engine.Tool — the structured front end for AutoMemory.Append
// (P20.54). The system prompt's auto-memory primer already tells the
// model to save memories using the four-type taxonomy + frontmatter
// convention; before this tool, the model had to drive that with
// Write + Edit (one call to create the file, another to update
// MEMORY.md), which mismatched in subtle ways: forgetting the index
// pointer, slug collisions, frontmatter typos.
//
// The Memory tool collapses the operation into one call, validated
// against the same Append helper used by tests. Failure modes
// (missing home dir / invalid type / empty body) surface as soft
// errors so the model gets a clear retry path.
//
// Lives in the memory package (next to AutoMemory) rather than
// internal/tools/orchestration so the test that locks the Append
// contract stays close to the consumer; wiring.go registers it
// after the orchestration tools.

package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// MemoryToolName is the model-facing name. Matches the verb the
// system prompt asks the model to use ("remember this", "save…").
const MemoryToolName = "Memory"

// MemoryTool implements engine.Tool by wrapping AutoMemory. Operates
// against the live ~/.biumind/memory directory (or whichever Dir the
// AutoMemory was loaded with, so tests can point at a TempDir).
type MemoryTool struct {
	// Auto is the auto-memory state; we only need its Dir field at
	// call time, but storing the full struct keeps the door open for
	// future ops (e.g. dedupe based on currently-loaded index).
	Auto AutoMemory
}

func (MemoryTool) Name() string { return MemoryToolName }

func (MemoryTool) Description(_ map[string]any) string {
	return "Save, list, or remove entries in your persistent auto-memory store. " +
		"Memories survive across sessions; use them for facts about the user, " +
		"feedback they gave, project context, or external system pointers — " +
		"see the auto-memory primer for the four memory types and what NOT " +
		"to save.\n\n" +
		"Actions:\n" +
		"  save  — write a new memory file + add a one-line pointer to MEMORY.md\n" +
		"  list  — return the current MEMORY.md contents (model uses this to\n" +
		"          decide whether an existing entry should be updated rather\n" +
		"          than duplicated)\n" +
		"  remove — delete a memory file by basename and prune its pointer\n" +
		"\n" +
		"Use save in preference to Write+Edit for new memories — this tool " +
		"keeps the file and index in sync, slugs the filename, stamps it " +
		"with a timestamp, and validates the type."
}

func (MemoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Operation: save (default), list, or remove.",
				"enum":        []any{"save", "list", "remove"},
			},
			"memory_type": map[string]any{
				"type":        "string",
				"description": "Memory taxonomy (required for save). One of: user, feedback, project, reference.",
				"enum":        []any{"user", "feedback", "project", "reference"},
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Short slug-friendly title for the memory frontmatter (save action). Optional — first body line is used as a fallback.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "One-line hook describing what this memory is. Used in the MEMORY.md index entry. Optional — first body line is used as a fallback.",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Memory content (required for save). For feedback / project types, structure as: rule/fact, then **Why:** and **How to apply:** lines.",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Memory file basename (required for remove), e.g. 'user-deep-go-expertise-20260301-101503.md'. Use action=list first to see basenames.",
			},
		},
		"required": []string{},
	}
}

func (MemoryTool) IsReadOnly(input map[string]any) bool {
	a, _ := input["action"].(string)
	return a == "list"
}
func (MemoryTool) IsDestructive(input map[string]any) bool {
	a, _ := input["action"].(string)
	return a == "remove"
}
func (MemoryTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (MemoryTool) InterruptBehavior() string               { return "block" }

func (m MemoryTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	action, _ := input["action"].(string)
	if action == "" {
		action = "save"
	}
	if m.Auto.Dir == "" {
		return softErr(MemoryToolName,
			"auto-memory directory unresolved (HOME unset?); cannot save / list memories"), nil
	}

	switch action {
	case "save":
		return m.callSave(input)
	case "list":
		return m.callList()
	case "remove":
		return m.callRemove(input)
	default:
		return softErr(MemoryToolName,
			fmt.Sprintf("unknown action %q (want save / list / remove)", action)), nil
	}
}

func (m MemoryTool) callSave(input map[string]any) (*engine.ToolResultPayload, error) {
	rawType, _ := input["memory_type"].(string)
	mt, ok := ParseMemoryType(rawType)
	if !ok {
		return softErr(MemoryToolName,
			"memory_type required for save (one of: user, feedback, project, reference)"), nil
	}
	body, _ := input["body"].(string)
	body = strings.TrimSpace(body)
	if body == "" {
		return softErr(MemoryToolName, "body is required for save"), nil
	}
	name, _ := input["name"].(string)
	desc, _ := input["description"].(string)

	res, err := m.Auto.Append(mt, name, desc, body)
	if err != nil {
		return softErr(MemoryToolName, err.Error()), nil
	}
	msg := fmt.Sprintf("Saved memory %q (%s) at %s; index updated at %s.",
		res.Name, mt, res.FilePath, res.IndexPath)
	return plainResult(msg), nil
}

func (m MemoryTool) callList() (*engine.ToolResultPayload, error) {
	if !m.Auto.Exists() {
		return plainResult("No memories yet — auto-memory directory is empty. Use action=save to create the first one."), nil
	}
	idx, err := os.ReadFile(m.Auto.IndexPath)
	if err != nil {
		return softErr(MemoryToolName,
			fmt.Sprintf("read index: %v", err)), nil
	}
	files, err := os.ReadDir(m.Auto.Dir)
	if err != nil {
		return softErr(MemoryToolName, fmt.Sprintf("read dir: %v", err)), nil
	}
	var basenames []string
	for _, f := range files {
		if f.IsDir() || f.Name() == AutoMemoryIndexBaseName {
			continue
		}
		if !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		basenames = append(basenames, f.Name())
	}
	sort.Strings(basenames)

	var b strings.Builder
	fmt.Fprintf(&b, "Auto-memory index (%s):\n\n", m.Auto.IndexPath)
	b.Write(idx)
	if !strings.HasSuffix(string(idx), "\n") {
		b.WriteByte('\n')
	}
	if len(basenames) > 0 {
		b.WriteString("\nMemory files on disk:\n")
		for _, name := range basenames {
			fmt.Fprintf(&b, "  - %s\n", name)
		}
	}
	return plainResult(b.String()), nil
}

func (m MemoryTool) callRemove(input map[string]any) (*engine.ToolResultPayload, error) {
	file, _ := input["file"].(string)
	file = strings.TrimSpace(file)
	if file == "" {
		return softErr(MemoryToolName, "file is required for remove (use action=list to see basenames)"), nil
	}
	if strings.ContainsAny(file, "/\\") || file == AutoMemoryIndexBaseName {
		return softErr(MemoryToolName,
			"file must be a basename inside the memory dir (no slashes), and not MEMORY.md itself"), nil
	}
	target := filepath.Join(m.Auto.Dir, file)
	if _, err := os.Stat(target); err != nil {
		return softErr(MemoryToolName,
			fmt.Sprintf("memory %q not found", file)), nil
	}
	if err := os.Remove(target); err != nil {
		return softErr(MemoryToolName, fmt.Sprintf("delete %s: %v", file, err)), nil
	}
	// Best-effort index prune: drop any line referencing the basename.
	// On read/write failure we still report success on the file delete
	// so the user isn't left with a half-broken state.
	pruned := pruneIndexLines(m.Auto.IndexPath, file)
	msg := fmt.Sprintf("Removed memory file %s.", file)
	if pruned > 0 {
		msg += fmt.Sprintf(" Pruned %d index line(s).", pruned)
	}
	return plainResult(msg), nil
}

// pruneIndexLines best-effort removes lines from MEMORY.md that
// reference the just-deleted basename. Returns the number of lines
// removed; 0 on read/write error so the caller can decide whether to
// surface a hint.
func pruneIndexLines(indexPath, file string) int {
	body, err := os.ReadFile(indexPath)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(body), "\n")
	out := make([]string, 0, len(lines))
	removed := 0
	for _, l := range lines {
		if strings.Contains(l, file) {
			removed++
			continue
		}
		out = append(out, l)
	}
	if removed == 0 {
		return 0
	}
	if err := os.WriteFile(indexPath, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return 0
	}
	return removed
}

// ─── small helpers ────────────────────────────────────────────────

func plainResult(text string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: text}},
	}
}

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("%s error: %s", name, msg),
		}},
		IsError:   true,
		SoftError: msg,
	}
}

// Compile-time interface check.
var _ engine.Tool = MemoryTool{}
