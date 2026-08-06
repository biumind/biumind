// Native NotebookRead + NotebookEdit tools for .ipynb files.
//
// The .ipynb format is a JSON document with a top-level `cells`
// array; each cell has `cell_type` ("code" | "markdown" | "raw") +
// `source` (string OR array of strings) + optional metadata. We
// preserve every other field on round-trip so tools that read the
// notebook (Jupyter, nbconvert) keep working.
//
// Supported edit modes:
//
//   replace (default) — overwrite the cell at cell_number / cell_id
//   insert            — add a new cell after cell_number
//   delete            — remove the cell at cell_number
//
// File freshness reuses the same Read-before-Edit invariant the
// regular Edit tool enforces: the agent must NotebookRead first.

package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// notebook is a thin model preserving fields verbatim under map[string]any.
type notebook struct {
	Cells    []notebookCell  `json:"cells"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Nbformat int             `json:"nbformat,omitempty"`
	Major    int             `json:"nbformat_minor,omitempty"`
	// Anything else lives in raw — preserved on writeback.
	raw map[string]json.RawMessage
}

type notebookCell struct {
	CellType string          `json:"cell_type"`
	Source   any             `json:"source"`
	ID       string          `json:"id,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Outputs  json.RawMessage `json:"outputs,omitempty"`
	// Forward-compat for unknown fields.
	raw map[string]json.RawMessage
}

func parseNotebook(b []byte) (*notebook, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	nb := &notebook{raw: raw}
	if c, ok := raw["cells"]; ok {
		if err := json.Unmarshal(c, &nb.Cells); err != nil {
			return nil, fmt.Errorf("notebook: parse cells: %w", err)
		}
	}
	if m, ok := raw["metadata"]; ok {
		nb.Metadata = m
	}
	if v, ok := raw["nbformat"]; ok {
		_ = json.Unmarshal(v, &nb.Nbformat)
	}
	if v, ok := raw["nbformat_minor"]; ok {
		_ = json.Unmarshal(v, &nb.Major)
	}
	return nb, nil
}

func (nb *notebook) marshal() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range nb.raw {
		out[k] = v
	}
	cellsJSON, err := json.Marshal(nb.Cells)
	if err != nil {
		return nil, err
	}
	out["cells"] = cellsJSON
	return json.MarshalIndent(out, "", " ")
}

// cellSourceAsString joins any-string-or-array source forms.
func cellSourceAsString(src any) string {
	switch v := src.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			if s, ok := item.(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

// ─── NotebookRead ─────────────────────────────────────

type NotebookReadTool struct{}

func (NotebookReadTool) Name() string { return "NotebookRead" }
func (NotebookReadTool) Description(_ map[string]any) string {
	return "List every cell in a Jupyter notebook (.ipynb) with its " +
		"index, type, id, and source body. Required before NotebookEdit."
}
func (NotebookReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
		},
		"required": []string{"file_path"},
	}
}
func (NotebookReadTool) IsReadOnly(_ map[string]any) bool        { return true }
func (NotebookReadTool) IsDestructive(_ map[string]any) bool     { return false }
func (NotebookReadTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (NotebookReadTool) InterruptBehavior() string               { return "cancel" }

func (NotebookReadTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	path, _ := input["file_path"].(string)
	if path == "" {
		return softErr("NotebookRead", "file_path required"), nil
	}
	full := absPath(env, path)
	body, err := os.ReadFile(full)
	if err != nil {
		return softErr("NotebookRead", err.Error()), nil
	}
	nb, err := parseNotebook(body)
	if err != nil {
		return softErr("NotebookRead", err.Error()), nil
	}
	// Cache the freshness state by path so NotebookEdit's Read-before
	// check works the same as the plain Edit tool does.
	stash(env, full, body)

	var out strings.Builder
	fmt.Fprintf(&out, "Notebook %s — %d cell(s)\n", path, len(nb.Cells))
	for i, c := range nb.Cells {
		fmt.Fprintf(&out, "\n--- cell %d (%s)", i, c.CellType)
		if c.ID != "" {
			fmt.Fprintf(&out, " id=%s", c.ID)
		}
		out.WriteString(" ---\n")
		out.WriteString(cellSourceAsString(c.Source))
		out.WriteByte('\n')
	}
	return text(strings.TrimRight(out.String(), "\n")), nil
}

// ─── NotebookEdit ─────────────────────────────────────

type NotebookEditTool struct{}

func (NotebookEditTool) Name() string { return "NotebookEdit" }
func (NotebookEditTool) Description(_ map[string]any) string {
	return "Edit a cell in a Jupyter notebook (.ipynb). " +
		"edit_mode='replace' (default) overwrites the cell at " +
		"cell_number; 'insert' adds a new cell after cell_number; " +
		"'delete' removes it. Requires a prior NotebookRead."
}

func (NotebookEditTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"notebook_path": map[string]any{"type": "string"},
			"cell_number":   map[string]any{"type": "integer", "description": "0-based cell index"},
			"cell_id":       map[string]any{"type": "string"},
			"cell_type":     map[string]any{"type": "string", "enum": []string{"code", "markdown", "raw"}},
			"new_source":    map[string]any{"type": "string"},
			"edit_mode":     map[string]any{"type": "string", "enum": []string{"replace", "insert", "delete"}},
		},
		"required": []string{"notebook_path"},
	}
}

func (NotebookEditTool) IsReadOnly(_ map[string]any) bool        { return false }
func (NotebookEditTool) IsDestructive(_ map[string]any) bool     { return true }
func (NotebookEditTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (NotebookEditTool) InterruptBehavior() string               { return "block" }

func (NotebookEditTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	path, _ := input["notebook_path"].(string)
	if path == "" {
		return softErr("NotebookEdit", "notebook_path required"), nil
	}
	full := absPath(env, path)
	mode, _ := input["edit_mode"].(string)
	if mode == "" {
		mode = "replace"
	}
	cellNum := -1
	if v, ok := input["cell_number"].(float64); ok {
		cellNum = int(v)
	}
	cellID, _ := input["cell_id"].(string)
	newSource, _ := input["new_source"].(string)
	cellType, _ := input["cell_type"].(string)

	prev, fresh, err := readForEdit(env, full)
	if err != nil {
		return softErr("NotebookEdit", err.Error()), nil
	}
	if !fresh {
		return softErr("NotebookEdit",
			"notebook has not been read this session — call NotebookRead first"), nil
	}
	nb, err := parseNotebook([]byte(prev))
	if err != nil {
		return softErr("NotebookEdit", err.Error()), nil
	}

	idx := cellNum
	if cellID != "" {
		idx = -1
		for i, c := range nb.Cells {
			if c.ID == cellID {
				idx = i
				break
			}
		}
		if idx == -1 {
			return softErr("NotebookEdit", "no cell with id "+cellID), nil
		}
	}

	switch mode {
	case "replace":
		if idx < 0 || idx >= len(nb.Cells) {
			return softErr("NotebookEdit",
				fmt.Sprintf("cell_number %d out of range [0, %d)", idx, len(nb.Cells))), nil
		}
		c := nb.Cells[idx]
		if cellType != "" {
			c.CellType = cellType
		}
		c.Source = newSource
		nb.Cells[idx] = c
	case "insert":
		if cellType == "" {
			cellType = "code"
		}
		newCell := notebookCell{
			CellType: cellType,
			Source:   newSource,
		}
		insertAt := idx + 1
		if insertAt < 0 {
			insertAt = 0
		}
		if insertAt > len(nb.Cells) {
			insertAt = len(nb.Cells)
		}
		nb.Cells = append(nb.Cells[:insertAt],
			append([]notebookCell{newCell}, nb.Cells[insertAt:]...)...)
	case "delete":
		if idx < 0 || idx >= len(nb.Cells) {
			return softErr("NotebookEdit",
				fmt.Sprintf("cell_number %d out of range [0, %d)", idx, len(nb.Cells))), nil
		}
		nb.Cells = append(nb.Cells[:idx], nb.Cells[idx+1:]...)
	default:
		return softErr("NotebookEdit", "unknown edit_mode: "+mode), nil
	}

	out, err := nb.marshal()
	if err != nil {
		return softErr("NotebookEdit", err.Error()), nil
	}
	if err := writeFileAndCache(env, full, string(out)); err != nil {
		return softErr("NotebookEdit", err.Error()), nil
	}
	return text(fmt.Sprintf("NotebookEdit %s: %s on cell %d (%d cells total).",
		path, mode, idx, len(nb.Cells))), nil
}

// stash records the on-disk file body in AppState's freshness ledger
// without touching disk. NotebookEdit reuses the regular Edit
// freshness check, so this primes the SHA-256 cache with the bytes
// the model just saw.
func stash(env *engine.ToolEnv, full string, body []byte) {
	if env == nil || env.AppState == nil {
		return
	}
	sum := sha256Sum(body)
	env.AppState.PutFile(state.FileState{
		Path: full, Content: string(body),
		Sha256: sum, NumLines: strings.Count(string(body), "\n") + 1,
	})
}

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
