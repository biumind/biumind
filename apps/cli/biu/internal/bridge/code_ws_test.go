package bridge

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/gorilla/websocket"
)

// dialCodeWS 拨通 /v1/code/ws，返回连接。
func dialCodeWS(t *testing.T, tsURL string) *websocket.Conn {
	t.Helper()
	wsURL := strings.Replace(tsURL, "http://", "ws://", 1) + "/v1/code/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial code ws: %v", err)
	}
	return conn
}

func sendReq(t *testing.T, conn *websocket.Conn, reqID, method string, params any) {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	frame := &sdkproto.CodeRequest{Type: sdkproto.TypeCodeRequest, RequestID: reqID, Method: method, Params: raw}
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
}

// readFrame 读一条帧并 UnmarshalFrame，10s 兜底。
func readFrame(t *testing.T, conn *websocket.Conn) sdkproto.Frame {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	f, err := sdkproto.UnmarshalFrame(msg)
	if err != nil {
		t.Fatalf("unmarshal frame: %v (raw=%s)", err, msg)
	}
	return f
}

// 读到指定 request_id 的 code_response（跳过期间到达的 pty_chunk 等）。
func readResponse(t *testing.T, conn *websocket.Conn, reqID string) *sdkproto.CodeResponse {
	t.Helper()
	for i := 0; i < 100; i++ {
		f := readFrame(t, conn)
		if resp, ok := f.(*sdkproto.CodeResponse); ok && resp.RequestID == reqID {
			return resp
		}
	}
	t.Fatalf("no code_response for %q after 100 frames", reqID)
	return nil
}

// 端到端：经真实 WS 跑 git.status + fs.read + fs.list（不绕云端）。
func TestCodeWS_GitAndFS(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("file-a-body"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := newTestServer(t, "")
	defer ts.Close()
	conn := dialCodeWS(t, ts.URL)
	defer conn.Close()

	// git.status
	sendReq(t, conn, "g1", "git.status", map[string]string{"cwd": dir})
	resp := readResponse(t, conn, "g1")
	if !resp.OK {
		t.Fatalf("git.status: %s", resp.Error)
	}
	if !strings.Contains(string(resp.Result), "a.txt") {
		t.Errorf("git.status result missing a.txt: %s", resp.Result)
	}

	// fs.read
	sendReq(t, conn, "f1", "fs.read", map[string]string{"path": filepath.Join(dir, "a.txt")})
	resp = readResponse(t, conn, "f1")
	if !resp.OK {
		t.Fatalf("fs.read: %s", resp.Error)
	}
	if !strings.Contains(string(resp.Result), "file-a-body") {
		t.Errorf("fs.read result missing body: %s", resp.Result)
	}

	// fs.list
	sendReq(t, conn, "l1", "fs.list", map[string]string{"path": dir})
	resp = readResponse(t, conn, "l1")
	if !resp.OK {
		t.Fatalf("fs.list: %s", resp.Error)
	}
	if !strings.Contains(string(resp.Result), "a.txt") {
		t.Errorf("fs.list result missing a.txt: %s", resp.Result)
	}
}

// 端到端 PTY echo：pty.open(cat) → 发 code_pty_input → 收到回显的 code_pty_chunk。
// 这就是 M0 DoD 的「PTY echo 经 loopback bridge 跑通」。
func TestCodeWS_PtyEcho(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	ts := newTestServer(t, "")
	defer ts.Close()
	conn := dialCodeWS(t, ts.URL)
	defer conn.Close()

	sendReq(t, conn, "p1", "pty.open", map[string]any{"cmd": "cat"})
	resp := readResponse(t, conn, "p1")
	if !resp.OK {
		t.Fatalf("pty.open: %s", resp.Error)
	}
	var open struct {
		PtyID string `json:"pty_id"`
	}
	if err := json.Unmarshal(resp.Result, &open); err != nil || open.PtyID == "" {
		t.Fatalf("bad pty.open result: %v (%s)", err, resp.Result)
	}

	// 发输入帧；PTY 行规程会把它回显到输出。
	input := &sdkproto.CodePtyInput{Type: sdkproto.TypeCodePtyInput, PtyID: open.PtyID, Data: []byte("echo-roundtrip\n")}
	if err := conn.WriteJSON(input); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// 收 chunk 直到看到回显内容。
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f := readFrame(t, conn)
		if chunk, ok := f.(*sdkproto.CodePtyChunk); ok && chunk.PtyID == open.PtyID {
			got = append(got, chunk.Data...)
			if strings.Contains(string(got), "echo-roundtrip") {
				return // ✓ M0 DoD PTY echo 通过
			}
		}
	}
	t.Fatalf("did not see echoed input over WS; got %q", got)
}

// pty.open 后 kill，应收到 code_pty_exit。
func TestCodeWS_PtyExit(t *testing.T) {
	ts := newTestServer(t, "")
	defer ts.Close()
	conn := dialCodeWS(t, ts.URL)
	defer conn.Close()

	sendReq(t, conn, "p1", "pty.open", map[string]any{"cmd": "sh", "args": []string{"-c", "echo done-marker"}})
	resp := readResponse(t, conn, "p1")
	if !resp.OK {
		t.Fatalf("pty.open: %s", resp.Error)
	}

	sawExit := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f := readFrame(t, conn)
		if exit, ok := f.(*sdkproto.CodePtyExit); ok {
			if exit.ExitCode != 0 {
				t.Errorf("exit code = %d, want 0", exit.ExitCode)
			}
			sawExit = true
			break
		}
	}
	if !sawExit {
		t.Fatal("never received code_pty_exit")
	}
}
