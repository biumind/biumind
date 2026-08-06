// stdio transport for MCP.
//
// MCP over stdio framing is newline-delimited JSON (NDJSON): each
// JSON-RPC message is a single line, terminated by `\n`. Stderr is
// NOT part of the protocol — servers may write logs there, which we
// route to the configured logger (or discard) without parsing.
//
// Lifecycle:
//   1. Spawn the server subprocess with a shared cwd + env
//   2. Wire stdin / stdout / stderr
//   3. Goroutine reads stdout line-by-line, decodes JSON-RPC, fans
//      results into pending request channels (by id) or notification
//      handler (no id).
//   4. Shutdown: close stdin → server gets EOF and exits gracefully;
//      we kill the process if it doesn't exit within 5s.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// StdioConfig is the launch spec for an MCP stdio server.
type StdioConfig struct {
	Name    string            // friendly name for logging + tool prefixing
	Command string            // executable
	Args    []string          // CLI arguments
	Env     map[string]string // extra env vars (merged on top of os env)
	Cwd     string            // working dir; empty = inherit
	// StderrSink receives the server's stderr output line by line.
	// nil = discard. Useful for surfacing MCP server crashes in the
	// REPL log panel.
	StderrSink func(line string)
}

// MaxConsecutiveErrors triggers the circuit breaker — after this many
// consecutive call failures (excluding ctx-canceled), the client
// closes the underlying transport. Caller can then re-Start() to
// reconnect with a fresh subprocess. Idea borrowed from Claude Code's
// MAX_ERRORS_BEFORE_RECONNECT.
const MaxConsecutiveErrors = 3

// StdioClient is a JSON-RPC over stdio MCP client. Safe for concurrent
// Call() invocations from multiple goroutines.
type StdioClient struct {
	cfg     StdioConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Scanner
	encoder *json.Encoder

	// readLoopDone closes when the per-Start readLoop goroutine
	// exits (stdout EOF or scan error). Reconnect waits on this
	// before mutating cmd/stdin/stdout/encoder so the new
	// readLoop never races against the old one. Reset to a fresh
	// channel on each Start.
	readLoopDone chan struct{}

	// Pending request id → response channel. Each id is a unique int.
	mu       sync.Mutex
	pending  map[int64]chan json.RawMessage
	pendErr  map[int64]chan *JSONRPCError
	nextID   atomic.Int64
	closed   atomic.Bool
	encMu    sync.Mutex // serialise stdin writes

	// Circuit breaker — incremented on every transport-level failure,
	// reset on every success. >= MaxConsecutiveErrors tears down the
	// transport so the next call must reconnect.
	consecErrors atomic.Int32
}

// NewStdio creates an unstarted stdio client. Call Start() to spawn.
func NewStdio(cfg StdioConfig) *StdioClient {
	return &StdioClient{
		cfg:     cfg,
		pending: map[int64]chan json.RawMessage{},
		pendErr: map[int64]chan *JSONRPCError{},
	}
}

// Start spawns the subprocess and begins the read loop.
//
// Subprocess lifetime is decoupled from the caller's ctx — we use
// exec.Command (no CommandContext). The reasons:
//
//   - BootstrapMCP wraps connectClient calls in a 10s timeout ctx
//     and `defer cancel`s it on return. CommandContext's runtime
//     watcher would SIGKILL every server right after bootstrap
//     finishes, breaking every subsequent tool call until the
//     health monitor re-spawns. (P20.47x bug — manifested as
//     "broken pipe" the moment the model invoked an MCP tool from
//     headless / SDK paths; REPL was masked because no MCP tool
//     ran inside the 10s window before the health monitor reconnect
//     covered the gap.)
//
//   - The lifecycle we actually want — close on Close()/Reconnect()
//     — is already handled explicitly: stdin.Close + Process.Kill
//     after a 5s grace, plus SIGKILL in Reconnect.
//
// ctx is still used for the initial Initialize handshake by the
// caller; once Start returns nil, the subprocess outlives ctx and
// only Close()/Reconnect() can take it down.
func (c *StdioClient) Start(ctx context.Context) error {
	if c.cmd != nil {
		return errors.New("mcp: already started")
	}
	_ = ctx // kept in signature for future reuse (e.g. Process.Kill on
	//        ctx done) but deliberately not bound to the cmd's lifetime.
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	if c.cfg.Cwd != "" {
		cmd.Dir = c.cfg.Cwd
	}
	cmd.Env = os.Environ()
	for k, v := range c.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdin: %w", c.cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stdout: %w", c.cfg.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp[%s]: stderr: %w", c.cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp[%s]: start: %w", c.cfg.Name, err)
	}
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewScanner(stdout)
	// Match the largest reasonable JSON message in one line.
	c.stdout.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	c.encoder = json.NewEncoder(stdin)
	c.readLoopDone = make(chan struct{})

	// Spawn read loops.
	go c.readLoop()
	go c.stderrLoop(stderr)
	return nil
}

func (c *StdioClient) readLoop() {
	// Snapshot the scanner pointer locally so a concurrent
	// Reconnect (which mutates c.stdout AFTER waiting on
	// readLoopDone) can't race with our reads. Reconnect closes
	// the underlying pipe via stdin.Close + Process.Kill before
	// touching c.stdout, so this scan returns false promptly.
	scanner := c.stdout
	done := c.readLoopDone
	defer func() {
		if done != nil {
			close(done)
		}
	}()
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Server sent something un-decodeable — log via stderrSink
			// for visibility, but keep going.
			if c.cfg.StderrSink != nil {
				c.cfg.StderrSink("malformed JSON-RPC: " + err.Error())
			}
			continue
		}
		// Skip server-initiated notifications for now (no id). Future
		// work: tools/list_changed handler.
		idF, ok := resp.ID.(float64)
		if !ok {
			continue
		}
		id := int64(idF)
		c.mu.Lock()
		ch, exists := c.pending[id]
		errCh := c.pendErr[id]
		delete(c.pending, id)
		delete(c.pendErr, id)
		c.mu.Unlock()
		if !exists {
			continue
		}
		if resp.Error != nil {
			errCh <- resp.Error
			close(errCh)
			close(ch)
		} else {
			ch <- resp.Result
			close(ch)
			close(errCh)
		}
	}
	// Stream ended — fail any in-flight requests.
	c.mu.Lock()
	for id, ch := range c.pending {
		c.pendErr[id] <- &JSONRPCError{
			Code: -32000, Message: "mcp: server stream closed",
		}
		close(c.pendErr[id])
		close(ch)
		delete(c.pending, id)
		delete(c.pendErr, id)
	}
	c.mu.Unlock()
}

func (c *StdioClient) stderrLoop(r io.Reader) {
	if c.cfg.StderrSink == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		c.cfg.StderrSink(scanner.Text())
	}
}

// call performs a single JSON-RPC request and waits for the response.
// timeoutCtx is honored; if it fires the request stays pending on the
// server side but our channel is forgotten.
func (c *StdioClient) call(
	ctx context.Context, method string, params any,
) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("mcp: client closed")
	}
	id := c.nextID.Add(1)
	respCh := make(chan json.RawMessage, 1)
	errCh := make(chan *JSONRPCError, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.pendErr[id] = errCh
	c.mu.Unlock()

	req := JSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s]: marshal params: %w", c.cfg.Name, err)
		}
		req.Params = raw
	}
	if err := c.write(req); err != nil {
		c.recordError()
		// Drop the pending entry — no response will ever come.
		c.mu.Lock()
		delete(c.pending, id)
		delete(c.pendErr, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		// User cancellation doesn't count toward the circuit breaker.
		return nil, ctx.Err()
	case e := <-errCh:
		if e != nil {
			c.recordError()
			return nil, fmt.Errorf("mcp[%s] %s: %w", c.cfg.Name, method, e)
		}
		// Channel closed without error means the success path raced;
		// fall through.
		select {
		case raw := <-respCh:
			c.consecErrors.Store(0)
			return raw, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case raw := <-respCh:
		c.consecErrors.Store(0)
		return raw, nil
	}
}

// recordError bumps the consecutive-error counter and, when the
// threshold is hit, force-closes the transport so the next call
// pushes the caller into reconnect logic. Safe to invoke from the
// read loop and from the user goroutine.
func (c *StdioClient) recordError() {
	if c.consecErrors.Add(1) >= int32(MaxConsecutiveErrors) {
		// Idempotent shutdown — Close() guards against double-call.
		_ = c.Close()
	}
}

// IsHealthy reports whether the transport is up and the circuit
// breaker hasn't tripped. Bootstrap callers poll this to decide
// whether to reconnect on next use.
func (c *StdioClient) IsHealthy() bool {
	return !c.closed.Load() &&
		c.consecErrors.Load() < int32(MaxConsecutiveErrors)
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *StdioClient) notify(method string, params any) error {
	req := JSONRPCRequest{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = raw
	}
	return c.write(req)
}

func (c *StdioClient) write(req JSONRPCRequest) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	c.encMu.Lock()
	defer c.encMu.Unlock()
	return c.encoder.Encode(req)
}

// Initialize performs the handshake. Must be called once after Start.
func (c *StdioClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	raw, err := c.call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      ClientInfo{Name: "biu", Version: "0.1.0"},
		Capabilities:    ClientCapabilities{},
	})
	if err != nil {
		return nil, err
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode initialize: %w", c.cfg.Name, err)
	}
	if err := c.notify(MethodInitialized, struct{}{}); err != nil {
		return nil, fmt.Errorf("mcp[%s]: send initialized: %w", c.cfg.Name, err)
	}
	return &res, nil
}

// ListTools fetches the catalog. Auto-paginates if the server returns
// nextCursor.
func (c *StdioClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	out := []ToolDef{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodToolsList, params)
		if err != nil {
			return nil, err
		}
		var page ListToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode tools/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// ListResources fetches every resource the server advertises.
// Pages through NextCursor the same way ListTools does. Servers
// without the Resources capability typically respond with a method-
// not-found JSON-RPC error; we surface that to the caller so the
// tool can soft-error rather than panic.
func (c *StdioClient) ListResources(ctx context.Context) ([]Resource, error) {
	out := []Resource{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodResourcesList, params)
		if err != nil {
			return nil, err
		}
		var page ListResourcesResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode resources/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Resources...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// ReadResource fetches the contents of a single resource by URI. The
// server's response is returned verbatim so callers can inspect both
// text and blob payloads.
func (c *StdioClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	raw, err := c.call(ctx, MethodResourcesRead, ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	var res ReadResourceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode resources/read: %w", c.cfg.Name, err)
	}
	return &res, nil
}

// ListPrompts fetches every prompt template the server exposes.
// Pages through NextCursor the same way ListTools / ListResources
// do. Servers without the Prompts capability return a method-not-
// found JSON-RPC error, which surfaces verbatim to the caller.
func (c *StdioClient) ListPrompts(ctx context.Context) ([]Prompt, error) {
	out := []Prompt{}
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, MethodPromptsList, params)
		if err != nil {
			return nil, err
		}
		var page ListPromptsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp[%s]: decode prompts/list: %w", c.cfg.Name, err)
		}
		out = append(out, page.Prompts...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// GetPrompt renders one prompt template. Arguments are sent as a
// flat string→string map (the MCP spec encodes all arg values as
// strings); callers stringify ints / bools at the boundary.
func (c *StdioClient) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	raw, err := c.call(ctx, MethodPromptsGet, GetPromptParams{
		Name: name, Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	var res GetPromptResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode prompts/get: %w", c.cfg.Name, err)
	}
	return &res, nil
}

// CallTool invokes a tool. Arguments are passed as a free-form map.
// The result's content blocks are flattened to a string by the caller
// when feeding back to the LLM.
func (c *StdioClient) CallTool(
	ctx context.Context, name string, args map[string]any,
) (*CallToolResult, error) {
	raw, err := c.call(ctx, MethodToolsCall, CallToolParams{
		Name: name, Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	var res CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp[%s]: decode tools/call: %w", c.cfg.Name, err)
	}
	return &res, nil
}

// Close shuts down the subprocess. Idempotent.
func (c *StdioClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
		return errors.New("mcp: server did not exit in 5s, killed")
	}
}

// Name returns the configured server name (used for tool prefixing).
func (c *StdioClient) Name() string { return c.cfg.Name }

// Spec returns the stdio transport metadata so the registry's
// Servers() output can label this row as a subprocess + show the
// command line that spawned it. Args is copied so the caller can't
// mutate StdioClient state through the slice.
func (c *StdioClient) Spec() ClientSpec {
	return ClientSpec{
		Transport: TransportStdio,
		Command:   c.cfg.Command,
		Args:      append([]string(nil), c.cfg.Args...),
	}
}

// Ping issues the MCP `ping` method and waits for the empty
// response. Cheap liveness probe — 60ms ish on a healthy local
// stdio child. Used by HealthMonitor to detect dead subprocesses
// proactively rather than waiting for the next tool call to fail.
//
// A failed Ping does NOT immediately mark the client unhealthy —
// the call() path's circuit breaker handles that on consecutive
// failures. Caller (HealthMonitor) decides what to do with the
// returned error based on its own retry policy.
func (c *StdioClient) Ping(ctx context.Context) error {
	_, err := c.call(ctx, MethodPing, map[string]any{})
	return err
}

// Reconnect tears the subprocess down and re-forks. Bypasses
// Close's 5s grace timer because we're reconnecting precisely
// when the old process is unresponsive — graceful shutdown isn't
// useful and would just stall the recovery path. SIGKILL the
// process directly, drain the read goroutines via stdin.Close,
// reset the client state, then Start + Initialize.
//
// Pending requests on the old transport see "stream closed" via
// readLoop's deferred error drain; their callers see a transport
// error in the same cycle the HealthMonitor noticed the failure.
//
// HealthMonitor calls this with backoff after consecutive Ping
// failures; the call() path itself never reconnects (caller
// retries are HealthMonitor's job, not the per-call request's).
func (c *StdioClient) Reconnect(ctx context.Context) error {
	// Hard-kill the old subprocess. SIGKILL is correct here:
	// graceful shutdown via stdin EOF is what Close() does for
	// normal teardown, but we're reconnecting because something
	// went wrong, and a wedged server won't honour EOF in time.
	oldCmd := c.cmd
	oldReadLoopDone := c.readLoopDone
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	// Wait for the old readLoop to actually exit before mutating
	// the scanner / pipe fields. Without this, the new readLoop
	// races against the old one for c.stdout reads — race
	// detector flags it, and at runtime the new readLoop's
	// response routing is non-deterministic. 2s cap is a
	// safety net; in practice closing the pipes ends the loop in
	// microseconds.
	if oldReadLoopDone != nil {
		select {
		case <-oldReadLoopDone:
		case <-time.After(2 * time.Second):
			// Old loop wedged. Continue anyway — leaking one
			// goroutine is better than failing the recovery.
		}
	}

	// Reap the zombie in a detached goroutine so we don't block
	// here. cmd.Wait can hang indefinitely on certain pipe states
	// even after Kill; the OS reaps the zombie at biu shutdown
	// anyway when we exit.
	if oldCmd != nil {
		go func() { _ = oldCmd.Wait() }()
	}

	// Reset state. The subprocess fields go back to zero; the old
	// readLoop has already exited (stdout pipe closed at Kill).
	// Re-init the pending maps so next request inserts work.
	c.cmd = nil
	c.stdin = nil
	c.stdout = nil
	c.encoder = nil
	c.mu.Lock()
	c.pending = map[int64]chan json.RawMessage{}
	c.pendErr = map[int64]chan *JSONRPCError{}
	c.mu.Unlock()
	c.closed.Store(false)
	c.consecErrors.Store(0)
	// Reset the JSON-RPC id counter. The fresh subprocess has no
	// memory of prior ids — starting again from 1 keeps the wire
	// trace clean and matches the contract every spec-compliant
	// MCP server expects on initialize.
	c.nextID.Store(0)

	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("mcp[%s]: respawn: %w", c.cfg.Name, err)
	}
	if _, err := c.Initialize(ctx); err != nil {
		// Initialize failed; tear down the freshly-spawned
		// subprocess so we don't leak it. closed gate prevents
		// the close from racing the next Reconnect.
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		c.closed.Store(true)
		return fmt.Errorf("mcp[%s]: re-initialize: %w", c.cfg.Name, err)
	}
	return nil
}
