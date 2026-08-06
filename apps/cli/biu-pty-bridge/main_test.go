package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startBridge boots the binary on a per-test socket. Returns the
// socket path and a kill func.
func startBridge(t *testing.T) (string, func()) {
	t.Helper()
	bin := os.Getenv("BIU_PTY_BRIDGE_BIN")
	if bin == "" {
		bin = "/tmp/biu-pty-bridge-test"
		// Build into the temp file. `go test` already compiled the package
		// into a binary internally, but spawning it under exec needs an
		// actual on-disk artifact, so we compile a fresh one.
		out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
		if err != nil {
			t.Fatalf("build bridge: %v\n%s", err, out)
		}
	}
	sock := filepath.Join(t.TempDir(), "pty.sock")
	cmd := exec.Command(bin, "-socket", sock)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	// Wait for socket to appear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sock, func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

func TestBridgeOpenWriteRead(t *testing.T) {
	sock, stop := startBridge(t)
	defer stop()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	send := func(f frame) {
		b, _ := json.Marshal(f)
		_, _ = c.Write(append(b, '\n'))
	}
	rd := bufio.NewReader(c)
	recv := func() frame {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var f frame
		_ = json.Unmarshal(line, &f)
		return f
	}

	send(frame{Op: "open", ID: "s1", Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if got := recv(); got.Op != "opened" || got.ID != "s1" || got.PID == 0 {
		t.Fatalf("open response: %+v", got)
	}
	// Drain the prompt the shell emits, give it ~200ms.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := rd.Peek(1); err != nil {
			break
		}
		f := recv()
		if f.Op != "output" {
			break
		}
	}
	_ = c.SetReadDeadline(time.Time{})

	// Run echo.
	send(frame{Op: "write", ID: "s1", Data: "echo biumind-pty-bridge-ok\n"})
	saw := ""
	for i := 0; i < 50; i++ {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		f := recv()
		if f.Op == "output" {
			b, _ := base64.StdEncoding.DecodeString(f.Data)
			saw += string(b)
			if strings.Contains(saw, "biumind-pty-bridge-ok") {
				break
			}
		}
	}
	if !strings.Contains(saw, "biumind-pty-bridge-ok") {
		t.Errorf("echo output missing; got %q", saw)
	}

	// Close cleanly.
	send(frame{Op: "close", ID: "s1"})
}

func TestBridgeRejectsUnknownOp(t *testing.T) {
	sock, stop := startBridge(t)
	defer stop()
	c, _ := net.Dial("unix", sock)
	defer c.Close()
	_, _ = c.Write([]byte(`{"op":"flarble"}` + "\n"))
	rd := bufio.NewReader(c)
	line, _ := rd.ReadBytes('\n')
	var f frame
	_ = json.Unmarshal(line, &f)
	if f.Op != "error" || !strings.Contains(f.Message, "unknown op") {
		t.Errorf("want error frame, got %+v", f)
	}
}
