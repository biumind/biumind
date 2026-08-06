package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
)

// ServeStdio runs the MCP dispatcher over a stdio (NDJSON) transport.
// One JSON-RPC envelope per line on `in`; one response per request
// on `out`. Caller owns connection lifecycle: ServeStdio returns when
// `in` reaches EOF, or `ctx` is cancelled, or a write error occurs.
//
// Stdio is intended for local desktop clients (Claude Desktop, Cursor,
// Continue). Those clients spawn the binary as a subprocess and trust
// it implicitly — there's no JWT to validate. Caller pins the user
// identity via `asUser`; the server stamps that into request context
// so the dispatcher's ownership checks work the same as in HTTP.
//
// We do NOT respond to notifications (id absent) — the spec says
// notifications are fire-and-forget. Bad JSON gets a parse-error
// response so misbehaving clients can self-diagnose.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer, asUser uuid.UUID) error {
	if asUser == uuid.Nil {
		return errors.New("mcp: stdio transport requires a non-nil user id")
	}

	// Pin the user once; ctx is forked per-request so cancellation
	// from the parent still works.
	parent := bauth.WithClaims(ctx, &bauth.Claims{UserID: asUser.String()})

	// Serialise writes — handlers run synchronously today but a
	// future async dispatch would multiplex onto the same stdout.
	var writeMu sync.Mutex
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	emit := func(resp rpcResponse) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return enc.Encode(resp) // Encode appends '\n' itself
	}

	sc := bufio.NewScanner(in)
	// 4 MB max line — MCP request bodies are usually small but
	// `tools/call` with a big embedding query is plausible.
	buf := make([]byte, 0, 4*1024*1024)
	sc.Buffer(buf, cap(buf))

	for sc.Scan() {
		select {
		case <-parent.Done():
			return parent.Err()
		default:
		}

		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			if werr := emit(rpcResponse{
				JSONRPC: "2.0",
				Error: &rpcError{
					Code: codeParseError, Message: err.Error(),
				},
			}); werr != nil {
				return werr
			}
			continue
		}
		if req.JSONRPC != "2.0" {
			if werr := emit(rpcResponse{
				JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: codeInvalidRequest,
					Message: `jsonrpc must be "2.0"`},
			}); werr != nil {
				return werr
			}
			continue
		}

		// Notifications are fire-and-forget — id absent (or null).
		isNotification := len(req.ID) == 0 || string(req.ID) == "null"

		result, rerr := s.dispatch(parent, req)
		if isNotification {
			continue
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		if err := emit(resp); err != nil {
			return fmt.Errorf("mcp stdio write: %w", err)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mcp stdio read: %w", err)
	}
	return nil
}
