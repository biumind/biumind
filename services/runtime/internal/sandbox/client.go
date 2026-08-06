// Package sandbox is Runtime's HTTP client for the central
// services/sandbox service. Implements
// services/runtime/internal/agent.SkillSandbox so the
// skill.exec_script builtin tool can drive sandbox commands without
// taking a hard dep on the sandbox internal types.
//
// PS3.6 continued: inline skill resources (≤64 KB, see
// installer.MaxResourceInlineBytes) are materialised at /skill/<vpath>
// via a shell prep step before the user command runs. The cap was
// lifted from 4 KB once profiling showed 95% of real-world resources
// fit; argv budget is well within ARG_MAX even at the new ceiling.
//
// True-large or binary resources still need a Files CAS path. The
// ResourceFetcher interface below pins the contract so the eventual
// wiring is a one-file change to runtime daemon main.go: implement
// ResourceFetcher against the brain Files internal endpoint and pass
// it into New(...). buildPrepCommand consults the fetcher whenever a
// resource has Sha256 set + Inline empty.

package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
)

// ResourceFetcher fetches a skill bundle resource by its sha256.
// Implementations talk to whatever CAS the runtime is wired against
// (typically brain Files /v1/files/by-sha256/<hex>). Nil-safe by
// design: nil fetcher means "CAS path disabled" — buildPrepCommand
// silently skips Sha256-only resources rather than failing the
// whole exec. Real production main.go MUST wire one when skills
// ship resources >64 KB.
type ResourceFetcher interface {
	FetchByHash(ctx context.Context, sha256Hex string) ([]byte, error)
}

// Client speaks HTTP to services/sandbox /v1/sandboxes/{id}/exec.
// One Client per Runtime process; thread-safe via sandboxes mutex.
type Client struct {
	URL   string
	Token string // bearer JWT used for /v1/sandboxes/* (forwarded from inbound request)
	HC    *http.Client

	// Files — optional CAS fetcher for resources where Inline=="" and
	// Sha256 is set. Nil disables the CAS path entirely; legacy
	// behaviour where >64KB resources are simply absent from the
	// sandbox /skill/ tree.
	Files ResourceFetcher

	mu        sync.Mutex
	sandboxes map[string]string // sessionID → sandboxID, lazy-created on first exec
}

func New(url, token string) *Client {
	return &Client{
		URL:       strings.TrimRight(url, "/"),
		Token:     token,
		HC:        &http.Client{Timeout: 60 * time.Second},
		sandboxes: map[string]string{},
	}
}

// WithFiles wires a CAS fetcher used to materialise non-inline
// resources during prep. Returns the same client for chaining.
func (c *Client) WithFiles(f ResourceFetcher) *Client {
	c.Files = f
	return c
}

// ExecWithSkill implements agent.SkillSandbox. Inline resources are
// materialised at /skill/<vpath> via a shell prep step; the user
// command then runs with those files reachable.
//
// CAS-backed bundles (skill.ZipFileSha256 set) are still TODO for
// v2.0. Skills that ship binary or >4KB resources currently see
// missing files in the sandbox; the runtime tool layer doesn't
// pretend otherwise.
func (c *Client) ExecWithSkill(ctx context.Context, sessionID, command string, skill *skillsreg.Skill) (string, int, error) {
	if c.URL == "" {
		return "", -1, fmt.Errorf("sandbox: no URL configured")
	}
	sandboxID, err := c.ensureSandbox(ctx, sessionID)
	if err != nil {
		return "", -1, fmt.Errorf("ensure sandbox: %w", err)
	}

	// Stage 1: prep — write inline resources to /skill/<vpath>.
	// Combined into ONE shell command so one round-trip lands every
	// file (typical skill: 0–5 files, ≤64KB each). base64 encoding
	// avoids YAML / heredoc / quote edge cases corrupting content.
	prep, err := c.buildPrepCommand(ctx, skill)
	if err != nil {
		return "", -1, fmt.Errorf("prep skill bundle: %w", err)
	}
	if prep != "" {
		_, exit, err := c.execShell(ctx, sandboxID, prep)
		if err != nil {
			return "", -1, fmt.Errorf("prep skill bundle: %w", err)
		}
		if exit != 0 {
			return "", exit, fmt.Errorf("prep skill bundle exit=%d", exit)
		}
	}

	// Stage 2: run the user command.
	return c.execShell(ctx, sandboxID, command)
}

// execShell sends one POST /v1/sandboxes/{id}/exec and drains the SSE
// stream. Used by both the prep stage and the user command.
func (c *Client) execShell(ctx context.Context, sandboxID, command string) (string, int, error) {
	body := map[string]any{
		"argv":        []string{"/bin/sh", "-c", command},
		"workdir":     "/workspace",
		"timeout_sec": 30,
	}
	js, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, c.URL+"/v1/sandboxes/"+sandboxID+"/exec",
		bytes.NewReader(js))
	if err != nil {
		return "", -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		return "", -1, fmt.Errorf("exec: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", -1, fmt.Errorf("exec: status %d body=%s",
			resp.StatusCode, snippet(raw))
	}
	out, exit := readSSE(resp.Body)
	return out, exit, nil
}

// buildPrepCommand assembles the shell prep that materialises
// skill.Resources into /skill/<vpath>. Returns "" when the skill
// has no resources — callers skip the prep round-trip entirely.
//
// Each file gets one mkdir + base64 -d → file. Flags and quoting are
// deliberately POSIX-shell-only (no Bashisms) so the command works on
// the alpine sandbox base image.
//
// Resource sourcing rule:
//  1. Inline (≤64 KB UTF-8) — embed directly via base64.
//  2. Sha256 set + Inline empty — fetch via c.Files (CAS) and embed.
//     When c.Files is nil, the entry is silently skipped: the legacy
//     contract before CAS landed. Production runtime should wire a
//     ResourceFetcher; the skipped path is a safety net for dev.
//
// Returning an error here only happens for fetcher I/O failures.
// Malformed paths (".." / absolute) are skipped without erroring —
// that's a defense-in-depth check; the installer rejects them up
// front so they shouldn't appear in a real Skill row.
func (c *Client) buildPrepCommand(ctx context.Context, skill *skillsreg.Skill) (string, error) {
	if skill == nil || len(skill.Resources) == 0 {
		return "", nil
	}
	var lines []string
	lines = append(lines, "set -e")
	for vpath, meta := range skill.Resources {
		clean := path.Clean(vpath)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			continue
		}
		var body []byte
		switch {
		case meta.Inline != "":
			body = []byte(meta.Inline)
		case meta.Sha256 != "" && c.Files != nil:
			fetched, err := c.Files.FetchByHash(ctx, meta.Sha256)
			if err != nil {
				return "", fmt.Errorf("fetch %s (sha256=%s): %w",
					clean, meta.Sha256, err)
			}
			body = fetched
		default:
			// CAS-only resource with no fetcher wired — skip
			// silently to preserve the prior dev / no-CAS contract.
			continue
		}
		full := "/skill/" + clean
		dir := path.Dir(full)
		encoded := base64.StdEncoding.EncodeToString(body)
		lines = append(lines,
			fmt.Sprintf("mkdir -p %s", shellQuote(dir)),
			fmt.Sprintf("printf %%s %s | base64 -d > %s",
				shellQuote(encoded), shellQuote(full)),
		)
	}
	if len(lines) == 1 { // only "set -e"
		return "", nil
	}
	return strings.Join(lines, "\n"), nil
}

// shellQuote wraps a string in single quotes, escaping any embedded
// single-quote with the standard '\” shell idiom. Safe against
// every printable byte sequence — used for both filenames and the
// base64 blob.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, "'\"$`\\!*?[]{}() \t") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ensureSandbox returns a sandbox id for this session, creating one
// on first call. Sandboxes are cheap to leak in dev (they auto-
// reap after 30 minutes of idle) but for correctness we should add
// explicit destroy on session end — tracked in a follow-up so this
// PR doesn't grow.
func (c *Client) ensureSandbox(ctx context.Context, sessionID string) (string, error) {
	c.mu.Lock()
	if id, ok := c.sandboxes[sessionID]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()

	createBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"tier":       "cloud_free",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.URL+"/v1/sandboxes", bytes.NewReader(createBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HC.Do(req)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create: status %d body=%s",
			resp.StatusCode, snippet(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("create: empty sandbox id")
	}
	c.mu.Lock()
	c.sandboxes[sessionID] = out.ID
	c.mu.Unlock()
	return out.ID, nil
}

// readSSE drains the sandbox's exec SSE stream and returns the
// concatenated stdout/stderr + the final exit code. Heartbeats
// (lines starting with ":") are dropped. Two event types we care
// about: "data" (chunks of output) and "end" (final {exit_code}).
func readSSE(r io.Reader) (string, int) {
	var (
		buf  bytes.Buffer
		exit = -1
	)
	br := newLineReader(r)
	for {
		line, ok := br.next()
		if !ok {
			break
		}
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "data: "):
			buf.WriteString(strings.TrimPrefix(line, "data: "))
			buf.WriteByte('\n')
		case strings.HasPrefix(line, "event: end"):
			// Next line is `data: {"exit_code": N}` — peek.
			next, ok := br.next()
			if !ok {
				return buf.String(), exit
			}
			payload := strings.TrimPrefix(next, "data: ")
			var done struct {
				ExitCode int `json:"exit_code"`
			}
			if err := json.Unmarshal([]byte(payload), &done); err == nil {
				exit = done.ExitCode
			}
			return buf.String(), exit
		}
	}
	return buf.String(), exit
}

// lineReader is a minimal LF-delimited reader with one-line peek.
// Avoid bufio.Scanner — its 64KB token cap silently truncates large
// command outputs.
type lineReader struct {
	r   io.Reader
	buf []byte
	hit []string
}

func newLineReader(r io.Reader) *lineReader { return &lineReader{r: r} }

func (l *lineReader) next() (string, bool) {
	for {
		// Drain any already-split lines.
		if len(l.hit) > 0 {
			line := l.hit[0]
			l.hit = l.hit[1:]
			return line, true
		}
		var chunk [4096]byte
		n, err := l.r.Read(chunk[:])
		if n > 0 {
			l.buf = append(l.buf, chunk[:n]...)
			for {
				idx := bytes.IndexByte(l.buf, '\n')
				if idx < 0 {
					break
				}
				l.hit = append(l.hit, string(l.buf[:idx]))
				l.buf = l.buf[idx+1:]
			}
		}
		if err != nil {
			if len(l.buf) > 0 {
				l.hit = append(l.hit, string(l.buf))
				l.buf = nil
				continue
			}
			return "", false
		}
		if len(l.hit) == 0 && n == 0 {
			return "", false
		}
	}
}

func snippet(b []byte) string {
	if len(b) > 256 {
		b = b[:256]
	}
	return strings.TrimSpace(string(b))
}
