//go:build integration

package harness

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// listenLineRE captures the resolved address biu prints right after
// bind: "[biu] bridge listening on 127.0.0.1:53942 (auth=false)"
var listenLineRE = regexp.MustCompile(
	`\[biu\] bridge listening on (\S+) \(auth=(true|false)\)`)

// BridgeServer is a running `biu bridge` subprocess wired to a real
// (or fake-via-config) provider. Always stopped via Close — leaving
// it running orphans a port and an Anthropic session.
type BridgeServer struct {
	t      *testing.T
	cmd    *exec.Cmd
	cancel context.CancelFunc

	URL       string // "http://127.0.0.1:PORT"
	AuthToken string // empty when no auth is enforced

	stdoutBuf bytes.Buffer
	stderrBuf bytes.Buffer
	stderrMu  sync.Mutex
}

// StartBridge spawns biu bridge against the supplied sandbox and
// waits up to 8 s for it to print the listening line. The sandbox
// must already have SeedDirectConfig called against it (or the
// agent factory will fail).
//
// authToken="" disables auth (matches biu's --auth-token=""); set
// it to require Bearer.
//
// extraArgs is for forwarding additional flags (e.g. --model).
func StartBridge(t *testing.T, sb *Sandbox, authToken string, extraArgs ...string) *BridgeServer {
	t.Helper()
	bin := BiuBinary(t)
	args := []string{"--mode=direct", "bridge", "--listen", "127.0.0.1:0"}
	if authToken != "" {
		args = append(args, "--auth-token", authToken)
	}
	args = append(args, extraArgs...)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = sb.Cwd
	cmd.Env = sb.Env.Slice()

	bs := &BridgeServer{t: t, cmd: cmd, cancel: cancel, AuthToken: authToken}
	cmd.Stdout = &bs.stdoutBuf

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("bridge: stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("bridge: start: %v", err)
	}

	// Tee stderr to (a) the buffer for failure dumps and (b) the
	// addr-detection scanner. We don't block on the scanner — once
	// it sees the listening line it signals readiness; subsequent
	// stderr lines just keep flowing into the buffer.
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			bs.stderrMu.Lock()
			bs.stderrBuf.WriteString(line)
			bs.stderrBuf.WriteByte('\n')
			bs.stderrMu.Unlock()

			if m := listenLineRE.FindStringSubmatch(line); m != nil {
				select {
				case ready <- m[1]:
				default:
				}
			}
		}
	}()

	select {
	case addr := <-ready:
		bs.URL = "http://" + addr
	case <-time.After(8 * time.Second):
		bs.dumpAndFail(t, "bridge: timed out waiting for listening line")
	}
	return bs
}

// Close terminates the bridge subprocess. Idempotent.
func (b *BridgeServer) Close() {
	if b.cancel != nil {
		b.cancel()
	}
	_ = b.cmd.Wait()
}

// Stderr returns the accumulated bridge stderr, useful for failure
// dumps. Concurrency-safe.
func (b *BridgeServer) Stderr() string {
	b.stderrMu.Lock()
	defer b.stderrMu.Unlock()
	return b.stderrBuf.String()
}

func (b *BridgeServer) dumpAndFail(t *testing.T, msg string) {
	t.Helper()
	t.Fatalf("%s\nstderr:\n%s\nstdout:\n%s", msg, b.Stderr(), b.stdoutBuf.String())
}

// Do runs an authenticated HTTP request against the bridge. Path is
// joined to b.URL; pass body as nil for GETs / empty bodies. The
// returned response body is fully buffered so the caller doesn't
// have to remember to Close — this is for one-shot calls only,
// not SSE.
func (b *BridgeServer) Do(t *testing.T, method, path string, body io.Reader) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, b.URL+path, body)
	if err != nil {
		t.Fatalf("bridge: build request: %v", err)
	}
	if b.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.AuthToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bridge: %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, buf
}

// SSEFrame is one decoded text/event-stream message.
type SSEFrame struct {
	ID    string // empty when the frame had no `id:` line
	Event string // event name (e.g. "assistant_text"); empty for unnamed
	Data  string // raw data block, multiline if the source used multiple `data:` lines
}

// StreamEvents opens GET /v1/code/sessions/<id>/events and parses
// every SSE frame into the returned channel. The channel closes
// when the server closes the stream or ctx fires. Pass extra
// headers (e.g. Last-Event-ID) via header.
func (b *BridgeServer) StreamEvents(ctx context.Context, t *testing.T, sessionID string, header http.Header) <-chan SSEFrame {
	t.Helper()
	out := make(chan SSEFrame, 32)
	go func() {
		defer close(out)
		req, _ := http.NewRequestWithContext(ctx, "GET",
			b.URL+"/v1/code/sessions/"+sessionID+"/events", nil)
		if b.AuthToken != "" {
			req.Header.Set("Authorization", "Bearer "+b.AuthToken)
		}
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return
		}
		decodeSSE(resp.Body, out)
	}()
	return out
}

func decodeSSE(r io.Reader, out chan<- SSEFrame) {
	br := bufio.NewReader(r)
	var cur SSEFrame
	dataParts := []string{}
	flush := func() {
		if cur.Event == "" && len(dataParts) == 0 && cur.ID == "" {
			return
		}
		cur.Data = strings.Join(dataParts, "\n")
		out <- cur
		cur = SSEFrame{}
		dataParts = dataParts[:0]
	}
	for {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			flush()
			if err != nil {
				return
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "id:"):
			cur.ID = strings.TrimSpace(trimmed[3:])
		case strings.HasPrefix(trimmed, "event:"):
			cur.Event = strings.TrimSpace(trimmed[6:])
		case strings.HasPrefix(trimmed, "data:"):
			dataParts = append(dataParts, strings.TrimSpace(trimmed[5:]))
		}
		if err != nil {
			flush()
			return
		}
	}
}

// CreateSession POSTs /v1/code/sessions and returns the new id.
func (b *BridgeServer) CreateSession(t *testing.T) string {
	t.Helper()
	code, _, body := b.Do(t, "POST", "/v1/code/sessions", nil)
	if code != 200 && code != 201 {
		t.Fatalf("create session: status %d body=%s", code, body)
	}
	id := extractJSONString(body, "id")
	if id == "" {
		t.Fatalf("create session: no id in body %s", body)
	}
	return id
}

// extractJSONString digs out a top-level string field. Tiny scanner
// — we don't pull in the std json package because the body shapes
// are flat and predictable.
func extractJSONString(body []byte, field string) string {
	needle := []byte(`"` + field + `"`)
	i := bytes.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	col := bytes.IndexByte(rest, ':')
	if col < 0 {
		return ""
	}
	rest = bytes.TrimLeft(rest[col+1:], " \t")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return string(rest[:end])
}

// SubmitPrompt POSTs /v1/code/sessions/<id>/messages with a single
// prompt and returns nothing — the actual reply lands on the events
// SSE stream.
func (b *BridgeServer) SubmitPrompt(t *testing.T, sessionID, prompt string) {
	t.Helper()
	body := fmt.Sprintf(`{"prompt":%q}`, prompt)
	code, _, resp := b.Do(t, "POST", "/v1/code/sessions/"+sessionID+"/messages",
		strings.NewReader(body))
	if code != 200 && code != 202 {
		t.Fatalf("submit prompt: status %d body=%s", code, resp)
	}
}
