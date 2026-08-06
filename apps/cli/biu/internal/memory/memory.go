// Package memory loads BIUMIND.md instructions from the project + user
// home + ascending ancestors and assembles them into a system prompt
// fragment.
//
// Layers (lowest priority first — later layers override earlier when
// the same instruction conflicts, but in practice we just concatenate
// and let the model weight by recency):
//
//   1. User memory     ~/.biumind/BIUMIND.md
//   2. Project memory  <git root>/BIUMIND.md, <git root>/.biumind/BIUMIND.md
//                      and the same files in every ancestor between
//                      cwd and the git root.
//   3. Local memory    <cwd>/BIUMIND.local.md (gitignored, per-machine)
//
// @include directive:
//
//   Lines like `@./other.md` or `@~/foo/bar.md` inline the referenced
//   file's content. Relative paths resolve from the *including* file's
//   directory. Circular includes are blocked.
//
// Hard cap: each loaded file is truncated to MaxFileChars to bound
// the prompt cost. Total assembled memory is also bounded by the
// implicit per-file limit × file count.

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxFileChars caps a single BIUMIND.md (or @include) file.
const MaxFileChars = 40_000

// Source labels tell the prompt assembler where each block came from
// so the model can weight them appropriately.
type Source string

const (
	SrcUser    Source = "user"
	SrcProject Source = "project"
	SrcLocal   Source = "local"
)

// File is one loaded memory file plus metadata.
type File struct {
	Path    string
	Source  Source
	Content string // already truncated + @include-expanded
}

// Loaded is everything memory.Load produced for one session. The
// preamble + per-file content together form the chunk we inject as a
// system message.
type Loaded struct {
	Files    []File
	Preamble string
}

// Preamble is the standard memory-instruction preamble, kept verbatim
// so the model treats project + user instructions with the intended
// precedence.
const Preamble = "Codebase and user instructions are shown below. " +
	"Be sure to adhere to these instructions. " +
	"IMPORTANT: These instructions OVERRIDE any default behavior and you " +
	"MUST follow them exactly as written."

// Load is the simple-default entry point — equivalent to LoadWithOptions
// with an empty Options. Kept for callers that don't care about excludes.
func Load(cwd string) Loaded {
	return LoadWithOptions(cwd, Options{})
}

// Options controls what Load skips. Empty value = old behaviour.
type Options struct {
	// Excludes are glob patterns (filepath.Match-style) checked
	// against absolute file paths. Any BIUMIND.md whose path matches
	// is skipped. `**` is supported via the standard double-star
	// translation (see matchExclude).
	//
	// Typical use: excluding `~/work/giant-monorepo/**` from a
	// personal user memory file.
	Excludes []string

	// ExtraDirs is the set of additional working directories
	// (from /add-dir, --add-dir, or
	// settings.permissions.additionalDirectories) whose BIUMIND.md /
	// .biumind/BIUMIND.md files should also be loaded.
	//
	// Loaded AFTER the cwd ancestor walk so closer-to-current files
	// retain the higher slot. Duplicates with cwd are skipped to
	// avoid double-injecting the same file.
	ExtraDirs []string
}

// LoadWithOptions is the configurable entry point. Excludes filter
// out matching BIUMIND.md files BEFORE we read them, so a giant
// excluded tree doesn't cost a stat / read.
func LoadWithOptions(cwd string, opt Options) Loaded {
	out := Loaded{Preamble: Preamble}
	visited := map[string]bool{}

	read := func(path string, source Source) {
		if matchesAnyExclude(path, opt.Excludes) {
			return
		}
		if f := readWithIncludes(path, source, visited); f != nil {
			out.Files = append(out.Files, *f)
		}
	}

	// User layer.
	if home, err := os.UserHomeDir(); err == nil {
		read(filepath.Join(home, ".biumind", "BIUMIND.md"), SrcUser)
	}

	if cwd != "" {
		// Walk up from cwd to filesystem root, collecting BIUMIND.md
		// and .biumind/BIUMIND.md at every level. Closer-to-cwd files
		// land later in the slice → higher attention from the model.
		var ancestors []string
		dir, _ := filepath.Abs(cwd)
		for {
			ancestors = append([]string{dir}, ancestors...) // top-down
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			if dir == "/" || dir == filepath.VolumeName(dir)+string(filepath.Separator) {
				ancestors = append([]string{dir}, ancestors...)
				break
			}
		}
		for _, d := range ancestors {
			read(filepath.Join(d, "BIUMIND.md"), SrcProject)
			read(filepath.Join(d, ".biumind", "BIUMIND.md"), SrcProject)
		}
		// Local memory — never checked in.
		read(filepath.Join(cwd, "BIUMIND.local.md"), SrcLocal)
	}

	// Additional working directories (from settings.permissions.
	// additionalDirectories / --add-dir / /add-dir). Each contributes
	// any BIUMIND.md / .biumind/BIUMIND.md it carries so the model
	// gains the same instruction context across sibling repos.
	for _, d := range opt.ExtraDirs {
		if d == "" || d == cwd {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		read(filepath.Join(abs, "BIUMIND.md"), SrcProject)
		read(filepath.Join(abs, ".biumind", "BIUMIND.md"), SrcProject)
	}

	return out
}

// matchesAnyExclude returns true when path matches any of patterns.
// `**` translates to "any number of directories" via regex; `*` and
// `?` go through filepath.Match. Plain (non-glob) patterns match as
// substrings against the absolute path so users can write
// `node_modules` and have it work.
func matchesAnyExclude(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	abs, _ := filepath.Abs(path)
	if abs == "" {
		abs = path
	}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if matchOneExclude(p, abs) {
			return true
		}
	}
	return false
}

func matchOneExclude(pattern, target string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.Contains(target, pattern)
	}
	if strings.Contains(pattern, "**") {
		regexPat := regexp.QuoteMeta(pattern)
		regexPat = strings.ReplaceAll(regexPat, `\*\*`, `.*`)
		regexPat = strings.ReplaceAll(regexPat, `\*`, `[^/]*`)
		regexPat = strings.ReplaceAll(regexPat, `\?`, `.`)
		ok, _ := regexp.MatchString("(?:^|/)"+regexPat+"(?:$|/)", target)
		return ok
	}
	if ok, _ := filepath.Match(pattern, target); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, filepath.Base(target)); ok {
		return true
	}
	return false
}

// SystemPrompt returns the assembled string ready to drop into the
// engine's `system` field. Empty when no files were found.
func (l Loaded) SystemPrompt() string {
	if len(l.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(l.Preamble)
	b.WriteString("\n\n")
	for _, f := range l.Files {
		// File header makes the source obvious to the model.
		fmt.Fprintf(&b, "Contents of %s (%s memory):\n\n", f.Path, f.Source)
		b.WriteString(f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// readWithIncludes reads a file, expands @include directives, and
// applies the per-file truncation cap. Returns nil when the file is
// missing, unreadable, or already visited (circular include).
func readWithIncludes(path string, source Source, visited map[string]bool) *File {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	if visited[abs] {
		return nil
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	visited[abs] = true

	content := expandIncludes(string(raw), filepath.Dir(abs), visited)
	if len(content) > MaxFileChars {
		content = content[:MaxFileChars] + "\n…(truncated)"
	}
	return &File{Path: abs, Source: source, Content: content}
}

// expandIncludes replaces `@path` lines with the referenced file's
// content. Path resolution rules:
//
//	@path             relative to the including file's dir
//	@./path           same as above
//	@~/path           ~ expands to $HOME
//	@/abs/path        absolute
//
// Only directives that own an entire line are expanded — embedded
// `@foo` inside a sentence is left alone; expansion only fires on
// standalone tokens. Approximation good enough for P0.
func expandIncludes(s, baseDir string, visited map[string]bool) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "@") || len(trimmed) < 2 {
			continue
		}
		// Skip lines that look like an `@user` mention rather than a
		// path: a mention won't resolve to an existing file, so the
		// readWithIncludes return-nil path naturally drops them.
		ref := trimmed[1:]
		var resolved string
		switch {
		case strings.HasPrefix(ref, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				continue
			}
			resolved = filepath.Join(home, ref[2:])
		case filepath.IsAbs(ref):
			resolved = ref
		default:
			resolved = filepath.Join(baseDir, ref)
		}
		f := readWithIncludes(resolved, SrcProject, visited)
		if f == nil {
			continue
		}
		lines[i] = f.Content
	}
	return strings.Join(lines, "\n")
}
