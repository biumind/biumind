// Native Read tool. Replaces the legacy string-returning adapter
// with a freshness-aware engine.Tool that:
//
//   * caches the file's content + sha256 + read timestamp into
//     AppState.Files so Edit / Write can verify the model actually
//     looked at the file before mutating it);
//   * supports `offset` + `limit` for partial reads of huge files
//     (marking the view as partial so subsequent edits are refused);
//   * formats output with `cat -n` style line numbers so diffs the
//     model produces line up with what the user sees.
//
// File path resolution: absolute paths used verbatim; relative paths
// resolve against ToolEnv.Cwd. Symlinks are followed.

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// Maximum number of lines a single Read returns when neither
// offset+limit nor an absolute byte cap is reached. The 2000-line
// default forces the model to use offset/limit on long files.
const DefaultReadLines = 2000

// Maximum bytes pulled from a single file before truncation. Even
// when the file is small in lines (e.g. a 50KB single-line minified
// JS), we cap memory so Read can't OOM the agent.
const MaxReadBytes = 8 * 1024 * 1024 // 8 MB

type ReadTool struct{}

func (ReadTool) Name() string { return "Read" }

func (ReadTool) Description(_ map[string]any) string {
	return "Read a file from the workspace. Returns numbered lines and " +
		"caches the content for later Edit/Write. Use `offset` + `limit` " +
		"for files larger than " + itoa(DefaultReadLines) + " lines."
}

func (ReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"offset":    map[string]any{"type": "integer", "description": "1-based starting line"},
			"limit":     map[string]any{"type": "integer", "description": "max lines to return"},
		},
		"required": []string{"file_path"},
	}
}

func (ReadTool) IsReadOnly(_ map[string]any) bool        { return true }
func (ReadTool) IsDestructive(_ map[string]any) bool     { return false }
func (ReadTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (ReadTool) InterruptBehavior() string               { return "cancel" }

func (ReadTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	rawPath, _ := input["file_path"].(string)
	if rawPath == "" {
		return softErr("Read", "file_path required"), nil
	}
	full := absPath(env, rawPath)

	f, err := os.Open(full)
	if err != nil {
		return softErr("Read", fmt.Sprintf("open %s: %v", rawPath, err)), nil
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return softErr("Read", err.Error()), nil
	}
	truncated := false
	if len(raw) > MaxReadBytes {
		raw = raw[:MaxReadBytes]
		truncated = true
	}

	offset := intArg(input, "offset")
	limit := intArg(input, "limit")
	if limit <= 0 {
		limit = DefaultReadLines
	}

	lines := strings.Split(string(raw), "\n")
	totalLines := len(lines)
	start := 0
	if offset > 0 {
		start = offset - 1 // 1-based → 0-based
	}
	if start > totalLines {
		start = totalLines
	}
	end := start + limit
	if end > totalLines {
		end = totalLines
	}
	view := lines[start:end]
	partial := offset > 0 || limit < totalLines

	// Format with cat -n style numbering.
	var b strings.Builder
	for i, line := range view {
		fmt.Fprintf(&b, "%6d\t%s\n", start+i+1, line)
	}
	if truncated {
		b.WriteString("\n…(file truncated; max " + itoa(MaxReadBytes/1024) + " KB read)\n")
	}

	// Cache the *full* content (raw bytes hashed) so freshness checks
	// in Edit/Write can compare reliably even after partial reads.
	if env != nil && env.AppState != nil {
		sum := sha256.Sum256(raw)
		env.AppState.PutFile(state.FileState{
			Path:    full,
			Content: string(raw),
			ReadAt:  time.Now().UTC(),
			NumLines: totalLines,
			Sha256:  hex.EncodeToString(sum[:]),
		})
	}

	if partial && env != nil && env.OnProgress != nil {
		env.OnProgress(engine.ProgressData{
			"kind": "read", "lines_returned": end - start,
			"total_lines": totalLines,
		})
	}
	return text(strings.TrimRight(b.String(), "\n")), nil
}

// absPath resolves p against env.Cwd. Returns p unchanged when
// already absolute or env is nil.
func absPath(env *engine.ToolEnv, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cwd := ""
	if env != nil {
		cwd = env.Cwd
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

func intArg(input map[string]any, key string) int {
	if v, ok := input[key].(float64); ok {
		return int(v)
	}
	if v, ok := input[key].(int); ok {
		return v
	}
	return 0
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func text(s string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: s}},
	}
}

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("%s error: %s", name, msg),
		}},
		IsError: true, SoftError: msg,
	}
}
