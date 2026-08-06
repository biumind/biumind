// Native Edit + MultiEdit + Write tools. Semantics:
//
//   * Edit / MultiEdit / Write require a prior Read of the same file
//     (otherwise the agent could blindly stomp content it never saw).
//     The freshness check uses the SHA-256 cached by ReadTool.
//   * Edit replaces a single, unique occurrence of `old_string`.
//     `replace_all=true` extends to all occurrences.
//   * MultiEdit applies a list of {old_string,new_string} edits in
//     order. All-or-nothing — if any edit doesn't match, no write.
//   * Write fully overwrites the file. New files (path didn't exist
//     before) skip the read-before check.
//
// Errors are surfaced as soft tool results so the LLM can recover
// (re-read, fix the old_string, etc.) rather than the engine bailing.

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ─── Edit ─────────────────────────────────────────────

type EditTool struct{}

func (EditTool) Name() string { return "Edit" }

func (EditTool) Description(_ map[string]any) string {
	return "Replace exactly one occurrence of `old_string` with " +
		"`new_string` in a file. Set `replace_all=true` to change every " +
		"match. Requires a prior Read of the same file."
}

func (EditTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":   map[string]any{"type": "string"},
			"old_string":  map[string]any{"type": "string"},
			"new_string":  map[string]any{"type": "string"},
			"replace_all": map[string]any{"type": "boolean", "default": false},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (EditTool) IsReadOnly(_ map[string]any) bool        { return false }
func (EditTool) IsDestructive(_ map[string]any) bool     { return true }
func (EditTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (EditTool) InterruptBehavior() string               { return "block" }

func (EditTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	path, _ := input["file_path"].(string)
	if path == "" {
		return softErr("Edit", "file_path required"), nil
	}
	old, _ := input["old_string"].(string)
	new, _ := input["new_string"].(string)
	replaceAll, _ := input["replace_all"].(bool)
	if old == "" {
		return softErr("Edit", "old_string required (use Write to create files)"), nil
	}

	full := absPath(env, path)
	current, fresh, err := readForEdit(env, full)
	if err != nil {
		return softErr("Edit", err.Error()), nil
	}
	if !fresh {
		return softErr("Edit",
			"file has not been read this session — Read it first so the engine can verify content"), nil
	}

	count := strings.Count(current, old)
	if count == 0 {
		return softErr("Edit",
			"old_string not found verbatim. Re-read the file and copy the exact text."), nil
	}
	if !replaceAll && count > 1 {
		return softErr("Edit", fmt.Sprintf(
			"old_string occurs %d times; pass replace_all=true or extend old_string with surrounding context to make it unique",
			count)), nil
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(current, old, new)
	} else {
		updated = strings.Replace(current, old, new, 1)
	}

	if err := writeFileAndCache(env, full, updated); err != nil {
		return softErr("Edit", err.Error()), nil
	}
	body := fmt.Sprintf("Edited %s (%d replacement%s).",
		path, count, plural(count))
	if d := UnifiedDiff(path, current, updated, 3); d != "" {
		body += "\n\n" + d
	}
	return text(body), nil
}

// ─── MultiEdit ────────────────────────────────────────

type MultiEditTool struct{}

func (MultiEditTool) Name() string { return "MultiEdit" }

func (MultiEditTool) Description(_ map[string]any) string {
	return "Apply a sequence of edits to a single file. All-or-nothing: " +
		"if any edit doesn't match, the file is left untouched and the " +
		"tool returns an error. Each edit is {old_string, new_string, " +
		"replace_all}."
}

func (MultiEditTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"old_string":  map[string]any{"type": "string"},
						"new_string":  map[string]any{"type": "string"},
						"replace_all": map[string]any{"type": "boolean"},
					},
					"required": []string{"old_string", "new_string"},
				},
			},
		},
		"required": []string{"file_path", "edits"},
	}
}

func (MultiEditTool) IsReadOnly(_ map[string]any) bool        { return false }
func (MultiEditTool) IsDestructive(_ map[string]any) bool     { return true }
func (MultiEditTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (MultiEditTool) InterruptBehavior() string               { return "block" }

func (MultiEditTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	path, _ := input["file_path"].(string)
	if path == "" {
		return softErr("MultiEdit", "file_path required"), nil
	}
	rawEdits, _ := input["edits"].([]any)
	if len(rawEdits) == 0 {
		return softErr("MultiEdit", "edits array required"), nil
	}
	full := absPath(env, path)
	current, fresh, err := readForEdit(env, full)
	if err != nil {
		return softErr("MultiEdit", err.Error()), nil
	}
	if !fresh {
		return softErr("MultiEdit",
			"file has not been read this session — Read it first"), nil
	}

	working := current
	applied := 0
	for i, raw := range rawEdits {
		m, ok := raw.(map[string]any)
		if !ok {
			return softErr("MultiEdit", fmt.Sprintf("edit %d: not an object", i)), nil
		}
		old, _ := m["old_string"].(string)
		new, _ := m["new_string"].(string)
		replaceAll, _ := m["replace_all"].(bool)
		if old == "" {
			return softErr("MultiEdit", fmt.Sprintf("edit %d: old_string is empty", i)), nil
		}
		count := strings.Count(working, old)
		if count == 0 {
			return softErr("MultiEdit", fmt.Sprintf(
				"edit %d: old_string not found", i)), nil
		}
		if !replaceAll && count > 1 {
			return softErr("MultiEdit", fmt.Sprintf(
				"edit %d: old_string is ambiguous (%d matches)", i, count)), nil
		}
		if replaceAll {
			working = strings.ReplaceAll(working, old, new)
			applied += count
		} else {
			working = strings.Replace(working, old, new, 1)
			applied++
		}
	}

	if err := writeFileAndCache(env, full, working); err != nil {
		return softErr("MultiEdit", err.Error()), nil
	}
	body := fmt.Sprintf("MultiEdit %s: %d replacements across %d edits.",
		path, applied, len(rawEdits))
	if d := UnifiedDiff(path, current, working, 3); d != "" {
		body += "\n\n" + d
	}
	return text(body), nil
}

// ─── Write ────────────────────────────────────────────

type WriteTool struct{}

func (WriteTool) Name() string { return "Write" }

func (WriteTool) Description(_ map[string]any) string {
	return "Write `content` to `file_path`, replacing any existing " +
		"content. New files are created. Existing files require a prior " +
		"Read so the engine can detect concurrent modifications."
}

func (WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
		},
		"required": []string{"file_path", "content"},
	}
}

func (WriteTool) IsReadOnly(_ map[string]any) bool        { return false }
func (WriteTool) IsDestructive(_ map[string]any) bool     { return true }
func (WriteTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (WriteTool) InterruptBehavior() string               { return "block" }

func (WriteTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	path, _ := input["file_path"].(string)
	content, _ := input["content"].(string)
	if path == "" {
		return softErr("Write", "file_path required"), nil
	}
	full := absPath(env, path)

	// New-file path: skip the Read-before requirement.
	if _, err := os.Stat(full); os.IsNotExist(err) {
		if err := writeFileAndCache(env, full, content); err != nil {
			return softErr("Write", err.Error()), nil
		}
		return text(fmt.Sprintf("Wrote new file %s (%d bytes).",
			path, len(content))), nil
	}

	previous, fresh, err := readForEdit(env, full)
	if err != nil {
		return softErr("Write", err.Error()), nil
	}
	if !fresh {
		return softErr("Write",
			"existing file requires a prior Read so the engine can verify content"), nil
	}
	if err := writeFileAndCache(env, full, content); err != nil {
		return softErr("Write", err.Error()), nil
	}
	body := fmt.Sprintf("Overwrote %s (%d bytes).", path, len(content))
	if d := UnifiedDiff(path, previous, content, 3); d != "" {
		body += "\n\n" + d
	}
	return text(body), nil
}

// ─── Shared helpers ───────────────────────────────────

// readForEdit returns the on-disk content + a freshness flag. Fresh
// means: AppState.Files has a snapshot for this path AND its sha256
// matches the current on-disk content. Caller decides what to do
// when fresh=false.
func readForEdit(env *engine.ToolEnv, full string) (string, bool, error) {
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", false, err
	}
	if env == nil || env.AppState == nil {
		// No state ⇒ no freshness ledger. Treat as not fresh.
		return string(raw), false, nil
	}
	cached, ok := env.AppState.FileSnapshot(full)
	if !ok {
		return string(raw), false, nil
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != cached.Sha256 {
		// File changed under us since the last Read.
		return string(raw), false, nil
	}
	return string(raw), true, nil
}

// writeFileAndCache writes the new content and updates the AppState
// snapshot so subsequent edits see fresh state. Creates parent
// directories on demand for new file paths.
//
// Side-effect: when env.FileChanged is wired, fires it after the
// successful write so downstream systems (LSP pool) can invalidate
// stale cached views of the file. Without this gopls / pyright keep
// returning hover / reference info from the original didOpen text.
func writeFileAndCache(env *engine.ToolEnv, full, content string) error {
	// P20.57: capture pre-edit content under the current user
	// message UUID so `biu --rewind-files <uuid>` can restore.
	// Best-effort: snapshot failure (e.g. snapshots dir missing) is
	// logged via env.SnapshotFile's error return but doesn't block
	// the actual write — rewind ergonomics shouldn't fail real edits.
	if env != nil && env.SnapshotFile != nil {
		_ = env.SnapshotFile(full)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return err
	}
	if env != nil && env.AppState != nil {
		sum := sha256.Sum256([]byte(content))
		env.AppState.PutFile(state.FileState{
			Path: full, Content: content, ReadAt: time.Now().UTC(),
			NumLines: strings.Count(content, "\n") + 1,
			Sha256:   hex.EncodeToString(sum[:]),
		})
	}
	if env != nil && env.FileChanged != nil {
		env.FileChanged(full)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
