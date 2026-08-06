//go:build integration

package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// reAnsi is the strip-ANSI regex shared by REPL assertions. biu's
// TUI renders via lipgloss/bubbletea which inject CSI escapes for
// colour, cursor moves, and full-screen redraws. assertions take
// the post-strip text to keep them readable.
var reAnsi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// REPL is a running biu REPL under a PTY. Use StartREPL to allocate,
// then Send keystrokes and Expect to wait for substrings. Always
// defer Close() — the underlying subprocess holds a TTY and won't
// shut down on its own.
type REPL struct {
	t      *testing.T
	cmd    *exec.Cmd
	pty    *bytesPTY
	cancel context.CancelFunc
}

// bytesPTY wraps pty.File with a thread-safe accumulator so multiple
// Expect calls share one canonical view of everything biu has
// written. Reads happen in a background goroutine; writers (the
// caller) push into Master.
type bytesPTY struct {
	master io.ReadWriteCloser
	mu     sync.Mutex
	buf    bytes.Buffer
	done   chan struct{}
}

func (p *bytesPTY) Write(b []byte) (int, error) { return p.master.Write(b) }
func (p *bytesPTY) Close() error                { return p.master.Close() }

func (p *bytesPTY) snapshot() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]byte, p.buf.Len())
	copy(out, p.buf.Bytes())
	return out
}

func (p *bytesPTY) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Reset()
}

// StartREPL launches biu under a PTY using sandbox's seeded $HOME +
// cwd. Waits up to 5 s for the TUI to render its first frame so
// follow-up Send calls don't race against bubbletea boot.
//
// Sets a fixed 80x24 PTY winsize and TERM=xterm-256color so
// bubbletea's capability-detection queries (DSR / OSC 11) get a
// definite answer instead of hanging on terminal queries that
// nobody is going to reply to.
func StartREPL(t *testing.T, sb *Sandbox) *REPL {
	t.Helper()
	bin := BiuBinary(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "--mode=direct")
	cmd.Dir = sb.Cwd
	// Inject TUI-friendly env on top of the sandbox env so bubbletea
	// has a known TERM and skips fallback queries.
	cmd.Env = append(sb.Env.Slice(),
		"TERM=xterm-256color",
		"COLUMNS=80",
		"LINES=24",
		"NO_COLOR=1",
		// bubbletea / charmbracelet/x looks at these for capability
		// gating in some paths; CI=true skips a few interactive
		// probes that hang under our PTY without a real terminal
		// emulator on the other side.
		"CI=true",
	)

	// pty.StartWithSize sets winsize at fork time so bubbletea sees a
	// known terminal size during its capability probe. With pty.Start
	// (no winsize) bubbletea falls back to DSR `\x1b[6n` queries and
	// hangs waiting for a reply that nobody sends.
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		cancel()
		t.Fatalf("StartREPL: pty.StartWithSize: %v", err)
	}

	bp := &bytesPTY{master: master, done: make(chan struct{})}
	// Reader + ONE-SHOT capability responder. bubbletea + charmbracelet/x/term
	// probe at startup via:
	//   OSC 11 ?  background color query  (esc ] 11 ; ? esc \ or BEL)
	//   DSR    ?  cursor position report  (esc [ 6 n)
	// They block on the response. We answer each ONCE — replying to
	// every occurrence after the prompt is interactive injects the
	// reply as user-typed text (bubbletea's input parser eats ESC
	// sequences only during init).
	go func() {
		defer close(bp.done)
		buf := make([]byte, 4096)
		osc11Replied := false
		dsrReplied := false
		for {
			n, err := master.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				bp.mu.Lock()
				bp.buf.Write(chunk)
				bp.mu.Unlock()
				if !osc11Replied && bytes.Contains(chunk, []byte("\x1b]11;?")) {
					_, _ = master.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
					osc11Replied = true
				}
				if !dsrReplied && bytes.Contains(chunk, []byte("\x1b[6n")) {
					_, _ = master.Write([]byte("\x1b[24;80R"))
					dsrReplied = true
				}
			}
			if err != nil {
				return
			}
		}
	}()

	r := &REPL{t: t, cmd: cmd, pty: bp, cancel: cancel}

	// Nudge bubbletea to dispatch its initial WindowSizeMsg even when
	// the TIOCGWINSZ ioctl returns nothing useful. Sending SIGWINCH
	// 100 ms after start (let the process attach its handler first)
	// forces bubbletea to re-query and emit a WindowSizeMsg with the
	// 24x80 we set on the PTY. Without this nudge biu's View()
	// returns "initializing…" forever because m.width stays 0.
	go func() {
		time.Sleep(100 * time.Millisecond)
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGWINCH)
		}
	}()

	// Wait for biu's prompt box to fully render — the "❯" prefix
	// only appears AFTER bubbletea has finished its capability probe
	// AND placed the input area on screen.
	if err := r.expectInternal("❯", 8*time.Second); err != nil {
		r.dumpAndFail("REPL didn't render prompt within 8 s")
	}
	// One more redraw frame to settle.
	time.Sleep(200 * time.Millisecond)
	return r
}

// Send writes raw bytes to the PTY. Append "\r" to commit a line.
// Most callers want SendLine instead.
func (r *REPL) Send(b string) {
	r.t.Helper()
	if _, err := r.pty.Write([]byte(b)); err != nil {
		r.t.Fatalf("repl: write %q: %v", b, err)
	}
}

// SendLine is Send + carriage return — biu's bubbletea reads "\r"
// as the Enter key in PTY mode (newline doesn't always work).
func (r *REPL) SendLine(line string) {
	r.t.Helper()
	r.Send(line)
	r.Send("\r")
}

// Expect blocks until substring `want` appears in the post-ANSI-strip
// view of the accumulated output, or `timeout` elapses. Fails the
// test on timeout with a dump of what was actually captured.
func (r *REPL) Expect(want string, timeout time.Duration) {
	r.t.Helper()
	if err := r.expectInternal(want, timeout); err != nil {
		raw := r.pty.snapshot()
		r.t.Fatalf("repl: never saw %q within %v\n--- raw ---\n%s\n--- stripped ---\n%s",
			want, timeout, raw, reAnsi.ReplaceAll(raw, []byte("")))
	}
}

// ExpectAny is Expect with multiple acceptable substrings — first
// match wins.
func (r *REPL) ExpectAny(timeout time.Duration, wants ...string) string {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		stripped := string(reAnsi.ReplaceAll(r.pty.snapshot(), []byte("")))
		for _, w := range wants {
			if strings.Contains(stripped, w) {
				return w
			}
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("repl: none of %v appeared within %v\nstripped:\n%s",
				wants, timeout, stripped)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (r *REPL) expectInternal(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		stripped := string(reAnsi.ReplaceAll(r.pty.snapshot(), []byte("")))
		if strings.Contains(stripped, want) {
			return nil
		}
		// Also match against the RAW buffer so callers can wait for
		// pure ANSI markers (the initial-frame heuristic does this).
		if strings.Contains(string(r.pty.snapshot()), want) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ResetBuffer clears the captured output so subsequent Expect calls
// only see what comes AFTER. Useful for multi-step scenarios where
// previous output would otherwise satisfy the new assertion.
func (r *REPL) ResetBuffer() { r.pty.reset() }

// Snapshot returns the current ANSI-stripped output for ad-hoc
// inspection in assertions (e.g. checking absence of a string).
func (r *REPL) Snapshot() string {
	return string(reAnsi.ReplaceAll(r.pty.snapshot(), []byte("")))
}

// Close drains the PTY and waits for biu to exit. Safe to call
// multiple times.
func (r *REPL) Close() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	_ = r.pty.Close()
	// Best-effort wait; subprocess may already be gone.
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// SendCtrl is a convenience for sending a Ctrl-letter combination
// (e.g. SendCtrl('c') sends ETX). Letters must be a-z lowercase.
func (r *REPL) SendCtrl(letter byte) {
	r.t.Helper()
	if letter < 'a' || letter > 'z' {
		r.t.Fatalf("SendCtrl: letter %q out of range a-z", letter)
	}
	r.Send(string([]byte{letter - 'a' + 1}))
}

func (r *REPL) dumpAndFail(msg string) {
	r.t.Helper()
	raw := r.pty.snapshot()
	r.t.Fatalf("%s\n--- raw ---\n%s\n--- stripped ---\n%s",
		msg, raw, reAnsi.ReplaceAll(raw, []byte("")))
}

// strings is imported here so REPL can use strings.Contains without
// the test file having to.
//
// Anchor is via the package-level "strings" import declared in the
// rare path where the file might be tree-shaken.
var _ = fmt.Sprintf
