// Native Glob + Grep tools.
//
// Glob walks the filesystem matching a doublestar pattern and emits
// each hit through env.OnProgress (so the TUI can show "5 / 17
// files matched" live). Grep falls back to ripgrep when it's on PATH
// (faster, respects .gitignore) and uses a pure-Go scanner
// otherwise.
//
// Both tools cap output at SearchMaxResults to keep tool_result
// blocks under the model's context budget.

package files

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// SearchMaxResults caps the number of hits any single Glob/Grep
// call returns. Generous enough for typical refactors but not so
// large that a `*` query OOMs the conversation.
const SearchMaxResults = 1000

// ─── Glob ─────────────────────────────────────────────

type GlobTool struct{}

func (GlobTool) Name() string { return "Glob" }

func (GlobTool) Description(_ map[string]any) string {
	return "Match files by pattern. Supports `**` for recursive globbing " +
		"(e.g. `**/*.go`). Returns absolute paths sorted by mtime " +
		"descending."
}

func (GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string", "description": "root to search (defaults to cwd)"},
		},
		"required": []string{"pattern"},
	}
}

func (GlobTool) IsReadOnly(_ map[string]any) bool        { return true }
func (GlobTool) IsDestructive(_ map[string]any) bool     { return false }
func (GlobTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (GlobTool) InterruptBehavior() string               { return "cancel" }

func (GlobTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		// 模型友好错误,带具体例子。glm-5.1 等推理模型 emit 空 args
		// 时,简短错误模型解析不出修复方向。
		return softErr("Glob",
			`missing required parameter 'pattern'. Please retry with arguments like: {"pattern": "**/*.go"} or {"pattern": "*.md", "path": "docs"}. Required: pattern (string, glob pattern). Optional: path (string, default cwd).`), nil
	}
	root := stringArg(input, "path")
	if root == "" {
		root = env.Cwd
		if root == "" {
			root, _ = os.Getwd()
		}
	} else {
		root = absPath(env, root)
	}

	type hit struct {
		path string
		mod  int64
	}
	var hits []hit
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, ".biumind": true,
		"vendor": true,
	}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // tolerate permission denials etc.
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if matchDoublestar(pattern, rel) || matchDoublestar(pattern, p) {
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			hits = append(hits, hit{path: p, mod: fi.ModTime().UnixNano()})
			if env != nil && env.OnProgress != nil && len(hits)%50 == 0 {
				env.OnProgress(engine.ProgressData{
					"kind": "glob_progress", "matched": len(hits),
				})
			}
			if len(hits) >= SearchMaxResults {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return softErr("Glob", err.Error()), nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].mod > hits[j].mod })

	if len(hits) == 0 {
		return text(fmt.Sprintf("No files matched %q under %s.", pattern, root)), nil
	}
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(h.path)
		b.WriteByte('\n')
	}
	if len(hits) >= SearchMaxResults {
		b.WriteString("\n…(truncated; refine the pattern)\n")
	}
	return text(strings.TrimRight(b.String(), "\n")), nil
}

// matchDoublestar implements a tiny `**` aware glob — `**` matches any
// number of path segments; `*` and `?` are forwarded to filepath.Match.
// Sufficient for the patterns callers actually pass; a full glob
// library would be overkill for P0.
func matchDoublestar(pattern, target string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, target)
		if ok {
			return true
		}
		return strings.HasSuffix(target, pattern)
	}
	regexPat := regexp.QuoteMeta(pattern)
	regexPat = strings.ReplaceAll(regexPat, `\*\*`, `.*`)
	regexPat = strings.ReplaceAll(regexPat, `\*`, `[^/]*`)
	regexPat = strings.ReplaceAll(regexPat, `\?`, `.`)
	re, err := regexp.Compile("^" + regexPat + "$")
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

// ─── Grep ─────────────────────────────────────────────

type GrepTool struct{}

func (GrepTool) Name() string { return "Grep" }

func (GrepTool) Description(_ map[string]any) string {
	return "Search file contents by regex. Uses ripgrep when available " +
		"(faster + respects .gitignore); falls back to a pure-Go " +
		"scanner otherwise. Returns `path:line:match` rows."
}

func (GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string"},
			"include": map[string]any{
				"type":        "string",
				"description": "Optional file glob (e.g. *.go) to limit which files are searched.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (GrepTool) IsReadOnly(_ map[string]any) bool        { return true }
func (GrepTool) IsDestructive(_ map[string]any) bool     { return false }
func (GrepTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (GrepTool) InterruptBehavior() string               { return "cancel" }

func (GrepTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return softErr("Grep",
			`missing required parameter 'pattern'. Please retry with arguments like: {"pattern": "TODO", "path": "src"} or {"pattern": "func.*Handler", "include": "*.go"}. Required: pattern (regex). Optional: path (default cwd), include (glob filter).`), nil
	}
	root := stringArg(input, "path")
	if root == "" {
		root = env.Cwd
		if root == "" {
			root, _ = os.Getwd()
		}
	} else {
		root = absPath(env, root)
	}
	include := stringArg(input, "include")

	if rg, err := exec.LookPath("rg"); err == nil {
		return runRipgrep(ctx, rg, pattern, root, include, env)
	}
	return goGrep(ctx, pattern, root, include, env)
}

// runRipgrep delegates to system ripgrep. We pass --json to avoid
// path-vs-regex parsing quirks but render plain text for the model.
func runRipgrep(ctx context.Context, rg, pattern, root, include string, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	args := []string{"--no-heading", "-n", "--with-filename", "--max-count", "10"}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, pattern, root)
	cmd := exec.CommandContext(ctx, rg, args...)
	out, err := cmd.Output()
	if err != nil {
		// rg returns 1 when there are no matches — treat as success.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return text("(no matches)"), nil
		}
		return softErr("Grep", err.Error()), nil
	}
	body := truncateLines(string(out), SearchMaxResults)
	if body == "" {
		body = "(no matches)"
	}
	return text(body), nil
}

// goGrep is the fallback when ripgrep isn't installed. Pure-Go,
// slower, no .gitignore awareness — but still good enough for small
// repos.
func goGrep(ctx context.Context, pattern, root, include string, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return softErr("Grep", "bad regex: "+err.Error()), nil
	}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, ".biumind": true,
		"vendor": true,
	}
	var hits []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" {
			if ok, _ := filepath.Match(include, d.Name()); !ok {
				return nil
			}
		}
		fi, _ := d.Info()
		if fi != nil && fi.Size() > 4*1024*1024 {
			return nil // skip giant files
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		lineNo := 0
		for scan.Scan() {
			lineNo++
			line := scan.Text()
			if re.MatchString(line) {
				hits = append(hits,
					fmt.Sprintf("%s:%d:%s", p, lineNo, line))
				if env != nil && env.OnProgress != nil && len(hits)%50 == 0 {
					env.OnProgress(engine.ProgressData{
						"kind": "grep_progress", "matched": len(hits),
					})
				}
				if len(hits) >= SearchMaxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return softErr("Grep", err.Error()), nil
	}
	if len(hits) == 0 {
		return text("(no matches)"), nil
	}
	body := strings.Join(hits, "\n")
	if len(hits) >= SearchMaxResults {
		body += "\n…(truncated; refine the pattern)"
	}
	return text(body), nil
}

// truncateLines keeps only the first n lines of s and appends a
// truncation marker when overflow.
func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return strings.TrimRight(s, "\n")
	}
	return strings.Join(lines[:n], "\n") + "\n…(truncated; refine the pattern)"
}

func stringArg(input map[string]any, key string) string {
	v, _ := input[key].(string)
	return v
}
