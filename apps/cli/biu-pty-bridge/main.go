// biu-pty-bridge — Unix-socket PTY broker for the desktop client.
//
// Why it exists:
//   The Flutter desktop app can't open a PTY directly (Flutter's
//   Process.start gives plain pipes). The bridge runs as a sidecar
//   process the client spawns once on startup, listens on a Unix
//   domain socket, and brokers full PTY sessions over a tiny JSON
//   protocol. The same bridge serves Web (which can't spawn at all)
//   when paired with a remote tunnel — but that's the Sandbox-routed
//   path, not this one.
//
// Wire protocol — line-delimited JSON, both directions:
//
//   client → bridge:
//     {"op":"open","id":"sess-1","argv":["/bin/bash","-l"],"cols":120,"rows":40}
//     {"op":"write","id":"sess-1","data":"ls\n"}
//     {"op":"resize","id":"sess-1","cols":140,"rows":50}
//     {"op":"close","id":"sess-1"}
//
//   bridge → client:
//     {"op":"opened","id":"sess-1","pid":12345}
//     {"op":"output","id":"sess-1","data":"<base64 chunk>"}
//     {"op":"exit","id":"sess-1","code":0}
//     {"op":"error","id":"sess-1","message":"..."}
//
// Output is base64-encoded so binary terminal escape sequences
// survive the JSON encoder. We use one connection per session today
// (simpler back-pressure semantics); when the desktop client wants
// many concurrent shells, the broker can multiplex over the same
// socket without a wire-format change.

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/creack/pty"
)

type frame struct {
	Op      string   `json:"op"`
	ID      string   `json:"id,omitempty"`
	Argv    []string `json:"argv,omitempty"`
	Cols    uint16   `json:"cols,omitempty"`
	Rows    uint16   `json:"rows,omitempty"`
	Data    string   `json:"data,omitempty"` // base64 for output, plain UTF-8 for input writes
	PID     int      `json:"pid,omitempty"`
	Code    int      `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

type session struct {
	id     string
	cmd    *exec.Cmd
	tty    *os.File
	mu     sync.Mutex
	closed bool
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.tty != nil {
		_ = s.tty.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func main() {
	var sockPath string
	flag.StringVar(&sockPath, "socket", defaultSocketPath(), "unix socket path")
	flag.Parse()

	// Tear down any stale socket.
	_ = os.Remove(sockPath)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		log.Fatalf("mkdir socket dir: %v", err)
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatalf("listen %s: %v", sockPath, err)
	}
	defer l.Close()
	if err := os.Chmod(sockPath, 0o600); err != nil {
		log.Fatalf("chmod socket: %v", err)
	}
	log.Printf("biu-pty-bridge listening on %s", sockPath)

	for {
		c, err := l.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			return
		}
		go handle(c)
	}
}

func handle(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	wr := newJSONWriter(c)

	var sess *session
	defer func() {
		if sess != nil {
			sess.close()
		}
	}()

	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("read: %v", err)
			}
			return
		}
		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			wr.send(frame{Op: "error", Message: "bad json: " + err.Error()})
			continue
		}
		switch f.Op {
		case "open":
			if sess != nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: "session already open"})
				continue
			}
			s, err := openSession(f, wr)
			if err != nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: err.Error()})
				continue
			}
			sess = s
			wr.send(frame{Op: "opened", ID: f.ID, PID: s.cmd.Process.Pid})
			go drainOutput(s, wr)
			go waitExit(s, wr)
		case "write":
			if sess == nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: "no open session"})
				continue
			}
			if _, err := sess.tty.Write([]byte(f.Data)); err != nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: err.Error()})
			}
		case "resize":
			if sess == nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: "no open session"})
				continue
			}
			if err := pty.Setsize(sess.tty, &pty.Winsize{Cols: f.Cols, Rows: f.Rows}); err != nil {
				wr.send(frame{Op: "error", ID: f.ID, Message: err.Error()})
			}
		case "close":
			if sess != nil {
				sess.close()
				sess = nil
			}
			return
		default:
			wr.send(frame{Op: "error", ID: f.ID, Message: "unknown op " + f.Op})
		}
	}
}

func openSession(f frame, wr *jsonWriter) (*session, error) {
	if len(f.Argv) == 0 {
		return nil, errors.New("argv required")
	}
	cmd := exec.Command(f.Argv[0], f.Argv[1:]...)
	cmd.Env = os.Environ()
	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}
	if f.Cols == 0 {
		f.Cols = 100
	}
	if f.Rows == 0 {
		f.Rows = 30
	}
	if err := pty.Setsize(tty, &pty.Winsize{Cols: f.Cols, Rows: f.Rows}); err != nil {
		_ = tty.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("setsize: %w", err)
	}
	return &session{id: f.ID, cmd: cmd, tty: tty}, nil
}

func drainOutput(s *session, wr *jsonWriter) {
	buf := make([]byte, 4096)
	for {
		n, err := s.tty.Read(buf)
		if n > 0 {
			wr.send(frame{
				Op:   "output",
				ID:   s.id,
				Data: base64.StdEncoding.EncodeToString(buf[:n]),
			})
		}
		if err != nil {
			return
		}
	}
}

func waitExit(s *session, wr *jsonWriter) {
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			wr.send(frame{Op: "error", ID: s.id, Message: err.Error()})
		}
	}
	wr.send(frame{Op: "exit", ID: s.id, Code: code})
}

// ─── helpers ──────────────────────────────────────────────

type jsonWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newJSONWriter(w io.Writer) *jsonWriter { return &jsonWriter{w: w} }

func (j *jsonWriter) send(f frame) {
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, _ = j.w.Write(append(b, '\n'))
}

func defaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/biumind-pty.sock"
	}
	return filepath.Join(home, ".biumind", "pty.sock")
}
