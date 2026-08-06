// Package magicdocs auto-maintains markdown files tagged with a
// `# MAGIC DOC: <title>` header. When the model reads such a file
// during a session, biu remembers it; periodically, biu spawns an
// LLM call that re-reads the conversation context + the doc and
// applies updates so the doc stays fresh with what's been learnt.
//
//  Use
// case: a team-shared "ARCHITECTURE.md" with the magic header.
// Every dev's biu session contributes incremental updates without
// anyone explicitly authoring them — the file becomes a living
// transcript of the team's evolving understanding.
//
// Trust model: only files the user explicitly Read get tracked.
// biu won't scan the filesystem for magic headers — that would
// turn doc-authoring into a footgun where any dropped markdown
// gets auto-mutated.
//
// API surface:
//
//	IsMagicDoc(content) → (title, ok)  — detect header
//	Tracker — registry of paths the current session has touched
//	Updater — pluggable LLM-driven update runner

package magicdocs

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// MagicDocHeader matches the literal "# MAGIC DOC: <title>" line at
// the top of a file (first non-blank line). Case-insensitive on
// "MAGIC DOC" but the colon + space-after-colon is required so a
// random `# magic doc cake recipe` markdown header isn't picked
// up.
var MagicDocHeader = regexp.MustCompile(`(?im)^#\s*MAGIC\s+DOC:\s*(.+)$`)

// MagicDocInstructions matches an optional italic line right after
// the header — author guidance for the updater ("Keep concise.
// Cite line numbers when referencing code.").
var MagicDocInstructions = regexp.MustCompile(`(?m)^[_*](.+?)[_*]\s*$`)

// DocInfo is what IsMagicDoc returns when a file qualifies.
type DocInfo struct {
	Title        string
	Instructions string // optional italics line; empty when absent
}

// IsMagicDoc reports whether content has the magic header. Pulls
// the title and (when present) the italic instructions line that
// follows. The first line of the file is the only place the header
// may appear — putting it deeper risks false positives in code
// blocks that quote the syntax.
func IsMagicDoc(content string) (DocInfo, bool) {
	// Look at the first 2 KB only — magic header is always at the
	// top, and a 10 MB file shouldn't pay a regex scan.
	head := content
	if len(head) > 2048 {
		head = content[:2048]
	}
	// Restrict to the first non-blank line for the header so a
	// random middle-of-file `# MAGIC DOC:` in a code fence isn't
	// matched.
	firstLine := strings.SplitN(head, "\n", 2)[0]
	m := MagicDocHeader.FindStringSubmatch(firstLine)
	if m == nil {
		return DocInfo{}, false
	}
	info := DocInfo{Title: strings.TrimSpace(m[1])}

	// Look at the line immediately after for italic instructions.
	if i := strings.Index(head, "\n"); i >= 0 && i+1 < len(head) {
		nextLine := head[i+1:]
		if nl := strings.Index(nextLine, "\n"); nl >= 0 {
			nextLine = nextLine[:nl]
		}
		if im := MagicDocInstructions.FindStringSubmatch(strings.TrimSpace(nextLine)); im != nil {
			info.Instructions = strings.TrimSpace(im[1])
		}
	}
	return info, true
}

// Tracker remembers which magic-doc files the model has read in
// the current session. Concurrent-safe: tools may register
// concurrently while a periodic updater iterates.
type Tracker struct {
	mu   sync.RWMutex
	docs map[string]DocInfo // abs-path → header info
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{docs: map[string]DocInfo{}} }

// Note records that path has been read this session and (if it's
// a magic doc) registers it for periodic update. Pass the file
// content the read tool returned. Empty path / non-magic content
// is a no-op.
func (t *Tracker) Note(path, content string) bool {
	if t == nil || path == "" || content == "" {
		return false
	}
	info, ok := IsMagicDoc(content)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.docs[path] = info
	return true
}

// Tracked returns a snapshot of the current tracked set.
func (t *Tracker) Tracked() map[string]DocInfo {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]DocInfo, len(t.docs))
	for k, v := range t.docs {
		out[k] = v
	}
	return out
}

// Forget removes a path from the tracked set. Called when the user
// explicitly stops watching a doc, or when the file no longer has
// a magic header (post-edit).
func (t *Tracker) Forget(path string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.docs, path)
}

// Updater drives the actual update LLM call. Same Summariser-shape
// interface compact / sessionmemory / agentsummary use, so wiring
// supplies one summariser shared across all four.
type Updater interface {
	Update(ctx context.Context, messages []state.Message, instruction string) (string, error)
}

// MaxUpdateBytes caps the size of a doc we'll feed to the updater.
// Large docs (beyond this) are rare; for them, the updater would
// have to re-emit a huge body, which is expensive. We surface a
// "doc too large" note instead and skip — the user can split.
const MaxUpdateBytes = 64 * 1024 // 64 KiB

// UpdateAll runs the updater against every tracked magic doc.
// Returns the count of docs successfully updated. Never errors —
// failures per-doc surface as stderr-style logs the caller can
// drop.
//
// The function is intended to be called from a periodic scheduler
// (every N tool calls or N seconds) or from a slash command.
func UpdateAll(ctx context.Context, t *Tracker, u Updater, recent []state.Message) int {
	if t == nil || u == nil {
		return 0
	}
	tracked := t.Tracked()
	if len(tracked) == 0 {
		return 0
	}
	updated := 0
	for path, info := range tracked {
		if ok := updateOne(ctx, path, info, u, recent); ok {
			updated++
		}
	}
	return updated
}

// updateOne handles one doc. Reads the current content, calls the
// updater with the structured prompt, writes back ON DISK if the
// updater returned a non-empty body. Errors are silent (the doc
// stays untouched).
func updateOne(ctx context.Context, path string, info DocInfo, u Updater, recent []state.Message) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(content) > MaxUpdateBytes {
		return false
	}
	// Re-check that the magic header is still present — the user
	// may have removed it (which means "stop auto-updating").
	if _, ok := IsMagicDoc(string(content)); !ok {
		return false
	}
	prompt := BuildUpdatePrompt(path, info, string(content))
	resp, err := u.Update(ctx, recent, prompt)
	if err != nil {
		return false
	}
	resp = strings.TrimSpace(resp)
	if resp == "" || resp == strings.TrimSpace(string(content)) {
		// No-change response — skip the write to keep the file's
		// mtime stable when nothing actually changed.
		return false
	}
	// Final guard: refuse to write back content that no longer has
	// the magic header — that would silently strip the marker and
	// detach the doc from future updates.
	if _, ok := IsMagicDoc(resp); !ok {
		return false
	}
	if err := os.WriteFile(path, []byte(resp), 0o644); err != nil {
		return false
	}
	return true
}

// BuildUpdatePrompt assembles the per-doc update instruction. The
// model is given: the file's current contents, the author-supplied
// instructions (italic line), and a strong "preserve structure +
// header" rule. We pass the conversation as `messages` so the
// updater sees what's been learnt, not just the doc itself.
func BuildUpdatePrompt(path string, info DocInfo, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		`You are updating a Magic Doc — a markdown file that maintains a living record of project knowledge across sessions.

Path: %s
Title: %q
`, path, info.Title)
	if info.Instructions != "" {
		fmt.Fprintf(&b, "Author instructions: %s\n", info.Instructions)
	}
	b.WriteString(`
Output rules:
  - Emit the full updated file body. NOT a diff. NOT a note. The full markdown.
  - Preserve the "# MAGIC DOC:" header line verbatim — without it, future
    sessions stop tracking this file.
  - Keep existing structure (sections, headings) unless something material changed.
  - Add NEW information you've learnt in this conversation that's worth carrying
    forward. Drop information that's contradicted by what you now know.
  - When you have nothing material to add, return the file UNCHANGED. Don't
    invent change for change's sake.
  - Do not include the path or any prefix line. The first character of your
    response must be `)
	b.WriteString("`#`")
	b.WriteString(` (the header).

Current file:
`)
	b.WriteString(content)
	return b.String()
}
