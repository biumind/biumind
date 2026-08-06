// Package sessionmemory persists a per-session markdown note file
// across compact + restart boundaries.
//
// The intuition: macro compact replaces the message history with a
// summary, and at session end the summary is gone. SessionMemory
// captures the "still relevant at the next session" facts — what
// the user is building, which files are in flight, what dead ends
// were already explored — and stores them in a stable file under
// ~/.biumind/sessionMemory/<session-id>.md so:
//
//   - cross-compact: post-compact context can re-inject the
//     session memory body so summaries don't have to encode every
//     working detail.
//   - cross-restart: opening a session continues from the same
//     file rather than the user re-explaining context.
//
// This package owns the scaffolding (path resolution, template,
// load / save / truncate, token estimation). Extraction policy —
// "when do we update the file? what does the agent write?" — is
// the responsibility of higher layers; the engine's post-compact
// path uses UpdateFromSummary to seed the Current State section
// with the latest compact summary, and a future PR can add a
// post-sampling background extractor that fleshes out the rest.
//
// Wire format is plain markdown so users (and other tools) can
// read / edit the file directly. Section detection uses a simple
// "# <name>" header convention; unknown sections pass through
// unchanged on round-trips.
package sessionmemory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxSectionTokens caps how much we keep per section after a
// truncate pass. The truncation drops oldest content (top of
// section) so the freshest notes survive a token budget squeeze.
const MaxSectionTokens = 2_000 / 4 // ~4 chars/token

// MaxTotalTokens is the overall ceiling on the on-disk file. Beyond
// this, oldest sections truncate proportionally.
const MaxTotalTokens = 12_000

// DefaultTemplate is the section scaffold a fresh session memory
// file gets. The section names + prompts are part of the
// cross-tool contract; users porting transcripts or building
// extractors expect these exact headings.
const DefaultTemplate = `# Session Title
_A short and distinctive 5-10 word descriptive title for the session. Super info dense, no filler_

# Current State
_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._

# Task specification
_What did the user ask to build? Any design decisions or other explanatory context_

# Files and Functions
_What are the important files? In short, what do they contain and why are they relevant?_

# Workflow
_What bash commands are usually run and in what order? How to interpret their output if not obvious?_

# Errors & Corrections
_Errors encountered and how they were fixed. What did the user correct? What approaches failed and should not be tried again?_

# Codebase and System Documentation
_Anything learnt about the project structure, conventions, or system behaviour that's worth carrying across sessions._
`

// sectionHeaderRE matches a section header line. Tolerant of
// trailing whitespace + arbitrary level (any number of #s). We
// pin to "# " (one or more #s + space) so accidental "#! /usr/bin"
// inside a code block doesn't get parsed as a header.
var sectionHeaderRE = regexp.MustCompile(`(?m)^(#{1,6})[ \t]+(.+?)[ \t]*$`)

// Section is one parsed block from the markdown file.
type Section struct {
	Header string // "# Current State" — preserves original "# count
	Body   string // post-header lines, trim-trailing-space
}

// SessionMemory is the parsed file plus its absolute path. Modify
// via UpdateSection / SetSection then call Save.
type SessionMemory struct {
	Path     string
	Sections []Section

	// Created records when the underlying file was first written.
	// Mainly informational; surfaced in /sessions for the user.
	Created time.Time
}

// Path returns the canonical session-memory file path for a session
// ID. Uses ~/.biumind/sessionMemory/ to stay in biu's namespace.
func PathFor(sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("session id required")
	}
	if strings.ContainsAny(sessionID, "/\\.") {
		return "", fmt.Errorf("session id %q contains path separators", sessionID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "sessionMemory", sessionID+".md"), nil
}

// Load reads the session memory file for sessionID. Missing file
// returns a fresh SessionMemory seeded with DefaultTemplate — first
// run for a session is a normal case, not an error.
func Load(sessionID string) (*SessionMemory, error) {
	path, err := PathFor(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &SessionMemory{
			Path:     path,
			Sections: parseSections(DefaultTemplate),
			Created:  time.Now().UTC(),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	st, _ := os.Stat(path)
	created := time.Now().UTC()
	if st != nil {
		created = st.ModTime().UTC()
	}
	return &SessionMemory{
		Path:     path,
		Sections: parseSections(string(data)),
		Created:  created,
	}, nil
}

// parseSections splits a markdown body at # headers. Content before
// the first header lands in a synthetic "" header section so
// round-trips never lose leading content.
func parseSections(body string) []Section {
	var out []Section
	idxs := sectionHeaderRE.FindAllStringIndex(body, -1)
	if len(idxs) == 0 {
		return []Section{{Header: "", Body: strings.TrimRight(body, "\n") + "\n"}}
	}
	if idxs[0][0] > 0 {
		out = append(out, Section{
			Header: "",
			Body:   strings.TrimRight(body[:idxs[0][0]], "\n") + "\n",
		})
	}
	for i, m := range idxs {
		hdrEnd := m[1]
		var sectionEnd int
		if i+1 < len(idxs) {
			sectionEnd = idxs[i+1][0]
		} else {
			sectionEnd = len(body)
		}
		// Header line goes from m[0] up to first newline at or after hdrEnd.
		nlPos := strings.Index(body[hdrEnd:], "\n")
		if nlPos < 0 {
			nlPos = len(body) - hdrEnd
		}
		header := body[m[0] : hdrEnd+nlPos]
		bodyPart := body[hdrEnd+nlPos : sectionEnd]
		bodyPart = strings.TrimLeft(bodyPart, "\n")
		bodyPart = strings.TrimRight(bodyPart, "\n") + "\n"
		out = append(out, Section{Header: header, Body: bodyPart})
	}
	return out
}

// Render flattens the parsed sections back into a single markdown
// string suitable for Save / SystemPrompt injection.
func (s *SessionMemory) Render() string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	for i, sec := range s.Sections {
		if sec.Header != "" {
			b.WriteString(sec.Header)
			b.WriteByte('\n')
		}
		body := sec.Body
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		b.WriteString(body)
		// Blank line between sections, but not after the last.
		if i+1 < len(s.Sections) {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Save writes the rendered file back to disk. Atomic via temp +
// rename so a crash mid-write doesn't leave the file empty. Creates
// parent dirs on demand.
func (s *SessionMemory) Save() error {
	if s == nil || s.Path == "" {
		return errors.New("sessionmemory: no path")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(s.Render()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// FindSection returns the named section by header text (case-
// insensitive on the header label, ignoring leading #s and
// whitespace). Returns nil + false when not present.
func (s *SessionMemory) FindSection(name string) (*Section, bool) {
	if s == nil {
		return nil, false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range s.Sections {
		got := s.Sections[i].labelLower()
		if got == want {
			return &s.Sections[i], true
		}
	}
	return nil, false
}

// labelLower extracts the header label (without #s) lowercased.
func (s Section) labelLower() string {
	h := strings.TrimSpace(s.Header)
	for len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}
	return strings.ToLower(strings.TrimSpace(h))
}

// SetSection replaces (or adds) a section's body by header label.
// Pass body without the header — the existing header is preserved
// when the section already exists; missing sections get a default
// "# <name>\n" header generated.
func (s *SessionMemory) SetSection(name, body string) {
	if s == nil {
		return
	}
	// Trim trailing whitespace; ensure single trailing newline.
	body = strings.TrimRight(body, " \n\t") + "\n"

	if sec, ok := s.FindSection(name); ok {
		sec.Body = body
		return
	}
	// Append.
	s.Sections = append(s.Sections, Section{
		Header: "# " + strings.TrimSpace(name),
		Body:   body,
	})
}

// SetCurrentState is a convenience for the most common write path —
// the compact pipeline pushing the latest summary into the canonical
// section. Satisfies compact.SessionMemoryWriter so callers in
// internal/compact can write without taking a hard dep on this
// package's full API.
func (s *SessionMemory) SetCurrentState(body string) {
	s.SetSection("Current State", body)
}

// AppendToSection adds body content to an existing section, or
// creates the section when missing. Appended content is preceded
// by a blank line for readability.
func (s *SessionMemory) AppendToSection(name, body string) {
	if s == nil {
		return
	}
	body = strings.TrimRight(body, " \n\t")
	if sec, ok := s.FindSection(name); ok {
		existing := strings.TrimRight(sec.Body, "\n")
		sec.Body = existing + "\n\n" + body + "\n"
		return
	}
	s.SetSection(name, body)
}

// Truncate enforces section + total token caps. Sections that
// exceed MaxSectionTokens get oldest content dropped (head trim);
// total file beyond MaxTotalTokens drops sections from the bottom
// up after per-section trim.
//
// Token estimation is a flat 4 chars/token — fast and good enough
// for cap math. The model's real tokenizer will give different
// counts but we're within ±20% which is good enough for a budget.
func (s *SessionMemory) Truncate() {
	if s == nil {
		return
	}
	maxSection := MaxSectionTokens * 4
	for i := range s.Sections {
		if len(s.Sections[i].Body) > maxSection {
			body := s.Sections[i].Body
			body = body[len(body)-maxSection:]
			// Try to align cut to a line boundary so we don't
			// dangle a half-line at the top.
			if nl := strings.Index(body, "\n"); nl > 0 && nl < len(body)/2 {
				body = body[nl+1:]
			}
			body = "[…earlier content truncated…]\n\n" + body
			s.Sections[i].Body = body
		}
	}
	// Total cap: drop trailing sections until under budget. Drop
	// from the BOTTOM (least-recently-relevant by convention) and
	// preserve "# Current State" + "# Session Title" at the top.
	maxTotal := MaxTotalTokens * 4
	for s.byteSize() > maxTotal && len(s.Sections) > 2 {
		// Find the highest-index section that isn't a protected one.
		drop := -1
		for i := len(s.Sections) - 1; i >= 0; i-- {
			label := s.Sections[i].labelLower()
			if label == "session title" || label == "current state" {
				continue
			}
			drop = i
			break
		}
		if drop < 0 {
			break
		}
		s.Sections = append(s.Sections[:drop], s.Sections[drop+1:]...)
	}
}

// byteSize returns the rendered file size — used by Truncate's
// total budget check.
func (s *SessionMemory) byteSize() int {
	if s == nil {
		return 0
	}
	return len(s.Render())
}
