package code

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// collector 是测试用 PtyEmitter，聚合所有 chunk 字节并在退出时关 exited。
type collector struct {
	mu            sync.Mutex
	chunks        []byte
	exitCode      int
	exitMsg       string
	sessionEvents []map[string]any
	exited        chan struct{}
	once          sync.Once
}

func newCollector() *collector { return &collector{exited: make(chan struct{})} }

func (c *collector) Chunk(_ string, data []byte) {
	c.mu.Lock()
	c.chunks = append(c.chunks, data...)
	c.mu.Unlock()
}

func (c *collector) Exit(_ string, code int, msg string) {
	c.mu.Lock()
	c.exitCode = code
	c.exitMsg = msg
	c.mu.Unlock()
	c.once.Do(func() { close(c.exited) })
}

func (c *collector) SessionEvent(_ string, event map[string]any) {
	c.mu.Lock()
	c.sessionEvents = append(c.sessionEvents, event)
	c.mu.Unlock()
}

func (c *collector) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, len(c.chunks))
	copy(out, c.chunks)
	return out
}

func mustParams(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func req(method string, params json.RawMessage) *sdkproto.CodeRequest {
	return &sdkproto.CodeRequest{Type: sdkproto.TypeCodeRequest, RequestID: "t", Method: method, Params: params}
}

// PTY 输出 + 退出：`sh -c 'echo ...'` 应吐出 marker 并以 code 0 退出。
func TestService_PtyOutputAndExit(t *testing.T) {
	svc := NewService()
	defer svc.CloseAll()
	c := newCollector()

	resp := svc.Dispatch(context.Background(), req("pty.open", mustParams(t, map[string]any{
		"cmd":  "sh",
		"args": []string{"-c", "echo hello-pty-12345"},
	})), c)
	if !resp.OK {
		t.Fatalf("pty.open failed: %s", resp.Error)
	}

	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("pty did not exit within 5s")
	}
	if c.exitCode != 0 {
		t.Errorf("exit code = %d (msg=%q), want 0", c.exitCode, c.exitMsg)
	}
	if !bytes.Contains(c.snapshot(), []byte("hello-pty-12345")) {
		t.Errorf("output missing marker; got %q", c.snapshot())
	}
}

// PTY 输入回显：往 `cat` 写字节，PTY 行规程会把输入回显到 master 读端 ——
// 这就是 M0 DoD 的「PTY echo」。
func TestService_PtyInputEcho(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	svc := NewService()
	defer svc.CloseAll()
	c := newCollector()

	resp := svc.Dispatch(context.Background(), req("pty.open", mustParams(t, map[string]any{
		"cmd": "cat",
	})), c)
	if !resp.OK {
		t.Fatalf("pty.open failed: %s", resp.Error)
	}
	var open struct {
		PtyID string `json:"pty_id"`
	}
	if err := json.Unmarshal(resp.Result, &open); err != nil || open.PtyID == "" {
		t.Fatalf("bad pty.open result: %v (%s)", err, resp.Result)
	}

	if err := svc.Input(open.PtyID, []byte("echotest\n")); err != nil {
		t.Fatalf("input: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		if bytes.Contains(c.snapshot(), []byte("echotest")) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("did not see echoed input; got %q", c.snapshot())
		case <-time.After(20 * time.Millisecond):
		}
	}

	// 收尾：kill 后应收到退出通知。
	if err := svc.Dispatch(context.Background(), req("pty.kill", mustParams(t, map[string]string{"pty_id": open.PtyID})), c); err != nil {
		// Dispatch 返回 *CodeResponse，不返回 error；这里只是占位防误用。
	}
	select {
	case <-c.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("killed pty did not report exit")
	}
}

func TestService_GitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	resp := svc.Dispatch(context.Background(), req("git.status", mustParams(t, map[string]string{"cwd": dir})), nil)
	if !resp.OK {
		t.Fatalf("git.status failed: %s", resp.Error)
	}
	var st struct {
		Untracked []string `json:"untracked"`
		Clean     bool     `json:"clean"`
	}
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if st.Clean {
		t.Error("expected dirty repo")
	}
	found := false
	for _, f := range st.Untracked {
		if f == "new.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("new.txt not in untracked: %+v", st.Untracked)
	}
}

func TestService_FsReadAndList(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(fp, []byte("content-42"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()

	resp := svc.Dispatch(context.Background(), req("fs.read", mustParams(t, map[string]any{"path": fp})), nil)
	if !resp.OK {
		t.Fatalf("fs.read failed: %s", resp.Error)
	}
	var rd struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(resp.Result, &rd); err != nil {
		t.Fatal(err)
	}
	if rd.Content != "content-42" || rd.Truncated {
		t.Errorf("read = %q trunc=%v, want content-42 false", rd.Content, rd.Truncated)
	}

	resp = svc.Dispatch(context.Background(), req("fs.list", mustParams(t, map[string]string{"path": dir})), nil)
	if !resp.OK {
		t.Fatalf("fs.list failed: %s", resp.Error)
	}
	var ls struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(resp.Result, &ls); err != nil {
		t.Fatal(err)
	}
	if len(ls.Entries) != 1 || ls.Entries[0].Name != "hello.txt" {
		t.Errorf("list entries = %+v, want [hello.txt]", ls.Entries)
	}
}

func TestService_UnknownMethod(t *testing.T) {
	svc := NewService()
	resp := svc.Dispatch(context.Background(), req("bogus.method", nil), nil)
	if resp.OK {
		t.Fatal("expected failure for unknown method")
	}
}

// code.runTask 经注入的 detector 把 agent 解析到 /bin/sh,验证 PTY 以 task_id
// 为 pty_id 拉起、可在 active 集查到。真实 claude/codex 启动由 BuildLaunch 单测
// (flag 映射)+ 手动 DoD 覆盖。
func TestService_RunTaskUsesTaskIdAsPtyId(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	svc := NewService()
	defer svc.CloseAll()
	// 注入桩:任何 agent 都解析到 /bin/sh(无 flag 的 claude → 跑交互式 sh)。
	svc.detect = func(string) (string, error) { return "/bin/sh", nil }
	c := newCollector()

	resp := svc.Dispatch(context.Background(), req("code.runTask", mustParams(t, map[string]any{
		"task_id":         "task-xyz",
		"agent_type":      "claude",
		"permission_mode": "", // 无 flag → /bin/sh 无参,进交互式
		"prompt":          "",
	})), c)
	if !resp.OK {
		t.Fatalf("runTask failed: %s", resp.Error)
	}
	var rd struct {
		PtyID string `json:"pty_id"`
	}
	_ = json.Unmarshal(resp.Result, &rd)
	if rd.PtyID != "task-xyz" {
		t.Fatalf("pty_id = %q, want task-xyz (task_id)", rd.PtyID)
	}

	// active 集应含 task_id
	a := svc.Dispatch(context.Background(), req("pty.active", nil), nil)
	if !strings.Contains(string(a.Result), "task-xyz") {
		t.Errorf("active set missing task-xyz: %s", a.Result)
	}

	svc.Dispatch(context.Background(), req("pty.kill", mustParams(t, map[string]string{"pty_id": "task-xyz"})), c)
	select {
	case <-c.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("runTask pty never exited after kill")
	}
}

func TestService_RunTaskBiuRejected(t *testing.T) {
	svc := NewService()
	svc.detect = func(string) (string, error) { return "/bin/sh", nil }
	resp := svc.Dispatch(context.Background(), req("code.runTask", mustParams(t, map[string]any{
		"task_id": "t1", "agent_type": "biu", "prompt": "x",
	})), newCollector())
	if resp.OK {
		t.Fatal("biu runTask should be rejected (in-process)")
	}
}

// pty.active 反映活跃 PTY 集：open 后含、退出后不含 —— 启动对账依赖这个真相源。
func TestService_PtyActive(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	svc := NewService()
	defer svc.CloseAll()
	c := newCollector()

	// 初始为空
	resp := svc.Dispatch(context.Background(), req("pty.active", nil), nil)
	if !resp.OK {
		t.Fatalf("pty.active: %s", resp.Error)
	}
	var active struct {
		PtyIDs []string `json:"pty_ids"`
	}
	_ = json.Unmarshal(resp.Result, &active)
	if len(active.PtyIDs) != 0 {
		t.Fatalf("expected empty active set, got %v", active.PtyIDs)
	}

	// open cat（长驻）→ active 应含
	open := svc.Dispatch(context.Background(), req("pty.open", mustParams(t, map[string]any{"cmd": "cat"})), c)
	if !open.OK {
		t.Fatalf("pty.open: %s", open.Error)
	}
	var od struct {
		PtyID string `json:"pty_id"`
	}
	_ = json.Unmarshal(open.Result, &od)

	resp = svc.Dispatch(context.Background(), req("pty.active", nil), nil)
	_ = json.Unmarshal(resp.Result, &active)
	found := false
	for _, id := range active.PtyIDs {
		if id == od.PtyID {
			found = true
		}
	}
	if !found {
		t.Fatalf("active set %v missing opened pty %s", active.PtyIDs, od.PtyID)
	}

	// kill → 等退出 → active 不含
	svc.Dispatch(context.Background(), req("pty.kill", mustParams(t, map[string]string{"pty_id": od.PtyID})), c)
	select {
	case <-c.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("killed pty never reported exit")
	}
	resp = svc.Dispatch(context.Background(), req("pty.active", nil), nil)
	_ = json.Unmarshal(resp.Result, &active)
	for _, id := range active.PtyIDs {
		if id == od.PtyID {
			t.Fatalf("killed pty %s still in active set %v", od.PtyID, active.PtyIDs)
		}
	}
}
