// Auto-memory: the persistent, file-based knowledge store the LLM
// builds up across sessions. Distinct from BIUMIND.md (which describes
// the *project*) — auto-memory describes the *user*, the *engagement
// history*, and *feedback the user has given* in past conversations.
//
// Mirrors Claude Code's auto-memory layer design:
//
//   ~/.biumind/memory/
//     MEMORY.md           — index, ≤200 lines, ≤25 KB, always loaded
//     <name>.md           — individual memory files with frontmatter
//
// The model is told (via Primer) where the directory lives, what the
// types of memory are, when to save / read, and what NOT to save.
// Individual memory files are NOT all loaded eagerly — the model
// reads them on demand via the Read tool.
//
// Why a separate file from memory.go?  BIUMIND.md is project-scoped
// and walks the cwd ancestry; auto-memory is user-scoped, single
// directory, and ships its own primer. Keeping the two layers in
// distinct types prevents accidental coupling (e.g. don't truncate
// MEMORY.md to MaxFileChars=40K when its own cap is 25K).

package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Auto-memory caps bound the index entrypoint. The index must stay
// short — its job is to be a directory-of-pointers, not a knowledge
// dump.
const (
	AutoMemoryDirName       = "memory"
	AutoMemoryIndexBaseName = "MEMORY.md"
	AutoMemoryMaxLines      = 200
	AutoMemoryMaxBytes      = 25_000
)

// AutoMemory is one user's persistent memory state. Empty value is
// valid — represents a user who hasn't started a memory yet.
type AutoMemory struct {
	// Dir is the resolved memory directory (~/.biumind/memory). Always
	// set even when the directory does not yet exist on disk, so
	// callers can advertise the path in primer text and write to it
	// later via Append.
	Dir string

	// IndexPath is Dir + "/MEMORY.md". Same "always set" semantics.
	IndexPath string

	// IndexContent is the raw body of MEMORY.md after truncation.
	// Empty when the file is missing or empty.
	IndexContent string

	// LineTruncated / ByteTruncated record whether the cap fired so
	// SystemPrompt can append a matching warning.
	LineTruncated bool
	ByteTruncated bool
}

// LoadAuto returns the auto-memory state rooted under `home` (typically
// $HOME). Missing directory / missing index = zero-content AutoMemory,
// not an error: the primer should still advertise the path so the
// model knows where to write.
func LoadAuto(home string) AutoMemory {
	if home == "" {
		return AutoMemory{}
	}
	dir := filepath.Join(home, ".biumind", AutoMemoryDirName)
	idx := filepath.Join(dir, AutoMemoryIndexBaseName)
	a := AutoMemory{Dir: dir, IndexPath: idx}

	raw, err := os.ReadFile(idx)
	if err != nil {
		// Missing is fine; permission errors silently degrade to empty
		// content (matches the BIUMIND.md loader's posture — never
		// fail a session just because memory couldn't be read).
		return a
	}
	a.IndexContent = truncateMemoryIndex(string(raw), &a.LineTruncated, &a.ByteTruncated)
	return a
}

// truncateMemoryIndex enforces both line and byte caps. Line cap fires
// first because newlines are the natural boundary; byte cap is a
// long-line failsafe (a single 30 KB pasted block could otherwise
// slip through line-only truncation).
func truncateMemoryIndex(raw string, lineFlag, byteFlag *bool) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > AutoMemoryMaxLines {
		*lineFlag = true
		lines = lines[:AutoMemoryMaxLines]
		trimmed = strings.Join(lines, "\n")
	}
	if len(trimmed) > AutoMemoryMaxBytes {
		*byteFlag = true
		// Cut at the last newline before the byte cap so we don't
		// slice mid-line. Falls back to a hard byte cut if there's no
		// newline before the limit.
		cut := AutoMemoryMaxBytes
		if nl := strings.LastIndexByte(trimmed[:cut], '\n'); nl > 0 {
			cut = nl
		}
		trimmed = trimmed[:cut]
	}
	return trimmed
}

// Exists reports whether the memory directory and index file are
// both present on disk. Used by `/memory` to decide whether to
// surface "no auto-memory" guidance.
func (a AutoMemory) Exists() bool {
	if a.Dir == "" {
		return false
	}
	_, err := os.Stat(a.IndexPath)
	return err == nil
}

// EnsureDir creates the memory directory if it doesn't exist.
// Idempotent; safe to call before every write. Returns the resolved
// directory path (same as a.Dir on success).
func (a AutoMemory) EnsureDir() (string, error) {
	if a.Dir == "" {
		return "", errors.New("auto-memory: no home directory resolved")
	}
	if err := os.MkdirAll(a.Dir, 0o755); err != nil {
		return "", err
	}
	return a.Dir, nil
}

// MemoryType is one of the four canonical categories the primer
// teaches. We constrain the input here so a typo (`/remember -t feedbcak`)
// fails fast instead of producing an entry the model later can't
// classify.
type MemoryType string

const (
	TypeUser      MemoryType = "user"
	TypeFeedback  MemoryType = "feedback"
	TypeProject   MemoryType = "project"
	TypeReference MemoryType = "reference"
)

// ValidMemoryTypes returns the canonical type strings — exposed so
// callers (slash handlers, tests) can validate without re-listing.
func ValidMemoryTypes() []MemoryType {
	return []MemoryType{TypeUser, TypeFeedback, TypeProject, TypeReference}
}

// ParseMemoryType returns the canonical type for `s` if it matches
// one of the four categories. ok=false on miss so callers can show
// a "valid types are…" message.
func ParseMemoryType(s string) (MemoryType, bool) {
	t := MemoryType(strings.ToLower(strings.TrimSpace(s)))
	for _, valid := range ValidMemoryTypes() {
		if t == valid {
			return t, true
		}
	}
	return "", false
}

// AppendResult is the outcome of an auto-memory write — handed back
// so callers can echo a "wrote: <path>" status line and the test
// suite can assert on the filename without re-reading the directory.
type AppendResult struct {
	// FilePath is the absolute path to the new memory file.
	FilePath string
	// Name is the kebab-case slug used as the file's frontmatter
	// `name`, derived from the first line of the body when the
	// caller didn't supply one explicitly.
	Name string
	// IndexPath is the auto-memory index that was created or updated.
	IndexPath string
}

// Append writes a new memory file under the auto-memory dir and
// extends MEMORY.md with a one-line pointer to it. Mirrors the
// frontmatter shape the primer documents:
//
//	---
//	name: <short slug>
//	description: <one-line hook>
//	type: user|feedback|project|reference
//	---
//	<body>
//
// The directory is created lazily; partial-write failures (file
// written but index update failed) leave the file in place and
// return the underlying error so the caller can surface it.
//
// Naming strategy: kebab-case slug from the first ~6 content words
// + a YYYYMMDD-HHMMSS suffix to guarantee uniqueness even when two
// captures share the same opening words ("user wants…" → twice).
//
// `name` and `description` may be left empty by the caller; in that
// case the body's first line is used for both (capped at sensible
// lengths).
func (a AutoMemory) Append(t MemoryType, name, description, body string) (AppendResult, error) {
	if a.Dir == "" {
		return AppendResult{}, errors.New("auto-memory: no home directory resolved")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return AppendResult{}, errors.New("auto-memory: body is required")
	}
	if _, ok := ParseMemoryType(string(t)); !ok {
		return AppendResult{}, fmt.Errorf("auto-memory: invalid type %q (want one of: user, feedback, project, reference)", t)
	}
	if _, err := a.EnsureDir(); err != nil {
		return AppendResult{}, fmt.Errorf("auto-memory: %w", err)
	}

	firstLine := body
	if i := strings.IndexByte(body, '\n'); i > 0 {
		firstLine = body[:i]
	}
	firstLine = strings.TrimSpace(firstLine)

	if name == "" {
		name = firstLine
	}
	if description == "" {
		description = firstLine
	}
	// Cap renderable fields so the index stays tight regardless of
	// what the user pasted.
	name = truncateRunes(name, 60)
	description = truncateRunes(description, 150)

	slug := slugify(name)
	if slug == "" {
		slug = "memory"
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s-%s.md", t, slug, stamp)
	target := filepath.Join(a.Dir, filename)

	frontmatter := strings.Builder{}
	frontmatter.WriteString("---\n")
	fmt.Fprintf(&frontmatter, "name: %s\n", name)
	fmt.Fprintf(&frontmatter, "description: %s\n", description)
	fmt.Fprintf(&frontmatter, "type: %s\n", t)
	frontmatter.WriteString("---\n\n")
	frontmatter.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		frontmatter.WriteByte('\n')
	}
	if err := os.WriteFile(target, []byte(frontmatter.String()), 0o644); err != nil {
		return AppendResult{}, fmt.Errorf("auto-memory: write %s: %w", target, err)
	}

	if err := a.appendIndexPointer(filename, name, description); err != nil {
		// File is on disk; surface the index error so the caller can
		// decide whether to retry. Don't roll back the file because
		// re-running Append would re-create it anyway.
		return AppendResult{
			FilePath: target, Name: name, IndexPath: a.IndexPath,
		}, fmt.Errorf("auto-memory: file written but index update failed: %w", err)
	}

	return AppendResult{
		FilePath: target, Name: name, IndexPath: a.IndexPath,
	}, nil
}

// appendIndexPointer appends one bullet to MEMORY.md, creating the
// file with a minimal header if it doesn't exist yet. Idempotent
// per (filename, hook) — re-running with the same args produces a
// duplicate bullet, which is fine: the index is human-readable, the
// model handles dedupe by reading the actual files.
func (a AutoMemory) appendIndexPointer(filename, name, description string) error {
	pointer := fmt.Sprintf("- [%s](%s) — %s\n", name, filename, description)

	existing, err := os.ReadFile(a.IndexPath)
	switch {
	case err == nil:
		// Append after a guaranteed trailing newline so we never
		// fuse to the end of an unterminated line.
		body := string(existing)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += pointer
		return os.WriteFile(a.IndexPath, []byte(body), 0o644)
	case errors.Is(err, os.ErrNotExist):
		// First entry — seed the index with a tiny header.
		header := "# Auto-memory index\n\n" +
			"One bullet per saved memory. Sorted by capture time " +
			"(oldest first). Edit freely — the model treats this " +
			"file as the source of truth for what memories exist.\n\n"
		return os.WriteFile(a.IndexPath, []byte(header+pointer), 0o644)
	default:
		return err
	}
}

// slugify maps a free-form string to a kebab-case filename-safe
// slug. ASCII-letter/digit chars stay; everything else collapses to
// dashes; trailing dashes get trimmed. Empty input → empty result.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Replace runs of non-alphanumeric with a single dash. Unicode
	// letters get folded out — slugs are filename-side, not user-
	// facing, so ASCII-only is the safer bet on every filesystem.
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return ""
	}
	// Cap to 6 segments so the filename stays short on long inputs
	// like "user wants chinese replies in technical contexts".
	parts := strings.Split(s, "-")
	if len(parts) > 6 {
		parts = parts[:6]
	}
	return strings.Join(parts, "-")
}

// truncateRunes caps `s` at n runes (not bytes) so multi-byte chars
// don't get split. Adds an ellipsis to make truncation visible.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// SystemPrompt assembles the auto-memory section that gets folded
// into the engine system prompt. Always returns *something* when
// `home` is resolvable, even if MEMORY.md is missing — the primer
// itself is the value: it tells the model the directory exists and
// memories can be created.
//
// Empty string only when `home` is unresolved (truly nothing to say).
func (a AutoMemory) SystemPrompt() string {
	if a.Dir == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(autoMemoryPrimer(a.Dir))
	if a.IndexContent != "" {
		b.WriteString("\n\nContents of ")
		b.WriteString(a.IndexPath)
		b.WriteString(" (auto-memory index):\n\n")
		b.WriteString(a.IndexContent)
		if a.LineTruncated || a.ByteTruncated {
			b.WriteString("\n…(truncated — keep MEMORY.md under ")
			if a.LineTruncated {
				b.WriteString("200 lines")
			} else {
				b.WriteString("25 KB")
			}
			b.WriteString(")")
		}
	}
	return b.String()
}

// autoMemoryPrimer is the model-facing description of biu's auto-
// memory contract. Kept compact (~1.5 KB) so it doesn't dwarf the
// rest of the system prompt; covers WHEN to save, WHAT NOT to save,
// the four memory types, and where the files live.
//
// Mirrors the auto-memory section of Claude Code's system prompt,
// shortened where biu doesn't have analogous infrastructure (e.g.
// no team-memory split yet).
func autoMemoryPrimer(dir string) string {
	return "# Auto-memory\n\n" +
		"You have a persistent, file-based memory at `" + dir + "`. " +
		"Build it up over time so future sessions can pick up where this " +
		"one left off — who the user is, how they want to collaborate, " +
		"what to repeat, and what to avoid.\n\n" +
		"## Memory types\n\n" +
		"- **user** — facts about the user's role, expertise, goals.\n" +
		"- **feedback** — guidance the user gave (corrections AND validations). " +
		"Lead with the rule, then `**Why:**` and `**How to apply:**` lines.\n" +
		"- **project** — non-derivable facts about ongoing work (deadlines, " +
		"motivations, stakeholders). Convert relative dates to absolute.\n" +
		"- **reference** — pointers to external systems (Linear, Slack, " +
		"dashboards) and what they hold.\n\n" +
		"## How to save\n\n" +
		"Prefer the **Memory tool** — one call writes the file AND " +
		"updates `MEMORY.md` together, with the slug, timestamp, and " +
		"frontmatter formatted correctly:\n\n" +
		"```\nMemory{action:\"save\", memory_type:\"feedback\",\n" +
		"       name:\"prefer terse responses\",\n" +
		"       description:\"this user dislikes trailing summaries\",\n" +
		"       body:\"…rule, **Why:**, **How to apply:**…\"}\n```\n\n" +
		"Use `Memory{action:\"list\"}` to see existing entries before " +
		"creating a new one — update an existing memory rather than " +
		"duplicating it. `Memory{action:\"remove\", file:\"<basename>\"}` " +
		"removes a stale memory and prunes the index pointer.\n\n" +
		"Manual fallback (when the Memory tool is unavailable): write " +
		"the memory to its own file in the directory above with " +
		"frontmatter `name / description / type`, then add a one-line " +
		"pointer `- [Title](file.md) — one-line hook` to `MEMORY.md`.\n\n" +
		"## What NOT to save\n\n" +
		"- Code patterns, architecture, file paths — derivable from the " +
		"current project state.\n" +
		"- Git history / who-changed-what — `git log` is authoritative.\n" +
		"- Debugging recipes — the fix is in the code; the commit message " +
		"has the context.\n" +
		"- Anything already in BIUMIND.md.\n" +
		"- Ephemeral task / conversation state.\n\n" +
		"## When to read\n\n" +
		"Before answering a question that depends on history, check " +
		"MEMORY.md for relevant entries; read the linked file when the " +
		"index hook looks promising. If the user asks you to ignore memory, " +
		"do not cite or apply it. Memories can become stale — verify " +
		"current state before acting on a recalled fact."
}
