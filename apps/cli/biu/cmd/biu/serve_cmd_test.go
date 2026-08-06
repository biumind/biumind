// S6-1 + S6-2 单测 —— PID 文件 helper + healthz handler。
//
// 不测整个 serve 命令的 RunE（依赖外部 NATS / brain）；那块由
// integration 手测覆盖。这里只验:
//   - PID 文件 acquire / release / stale 自动清理 / 现有进程拒启
//   - healthz handler 返回 200 + ok=true
//   - processAlive 在 self pid 上为 true，在 1 (不可用) 上为 false

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProcessAlive_Self(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Errorf("self process should be alive")
	}
}

func TestProcessAlive_NonExistent(t *testing.T) {
	// 99999999 一个明显不存在的 pid（OS pid 上限 ≤ pid_max，64-bit 也
	// 远小于这数）—— 平台无关探活
	if processAlive(99_999_999) {
		t.Errorf("non-existent pid should not be alive")
	}
	// pid <= 0 防御
	if processAlive(0) {
		t.Errorf("pid 0 should not be alive")
	}
	if processAlive(-1) {
		t.Errorf("negative pid should not be alive")
	}
}

func TestAcquirePIDFile_FreshDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "biu.pid")
	if err := acquirePIDFile(path); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer releasePIDFile(path)

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	got := strings.TrimSpace(string(body))
	pid, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid file=%d want %d", pid, os.Getpid())
	}
}

func TestAcquirePIDFile_StaleAutoCleared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "biu.pid")

	// 先写一个不存在的 stale pid
	if err := os.WriteFile(path, []byte("99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 预期：acquire 看到 pid 不存活 → overwrite 成功
	if err := acquirePIDFile(path); err != nil {
		t.Fatalf("expected stale auto-clear, got: %v", err)
	}
	defer releasePIDFile(path)

	body, _ := os.ReadFile(path)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(body)))
	if pid != os.Getpid() {
		t.Errorf("pid file=%d, expected overwrite to %d", pid, os.Getpid())
	}
}

func TestAcquirePIDFile_RunningProcessRefuses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "biu.pid")

	// 写当前自己的 pid —— 测试这个进程当然存活,且不是 biu serve。
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 接管语义下:存活但非-biu 的持有者必须被**拒绝**(绝不误杀无关进程),不接管。
	err := acquirePIDFile(path)
	if err == nil {
		t.Fatal("expected acquire to refuse when pid file points to a live non-biu process")
	}
	if !strings.Contains(err.Error(), "not a biu serve") {
		t.Errorf("err msg=%q, want mention 'not a biu serve'(拒绝误杀无关进程)", err.Error())
	}
	// 安全断言:拒绝路径不得终止该无关进程(本测试进程仍在)。
	if !processAlive(os.Getpid()) {
		t.Error("acquirePIDFile 误杀了无关的存活进程(本测试进程)")
	}
}

func TestAcquirePIDFile_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "deep", "biu.pid")

	if err := acquirePIDFile(path); err != nil {
		t.Fatalf("expected mkdir-p behaviour, got: %v", err)
	}
	defer releasePIDFile(path)

	if _, err := os.Stat(path); err != nil {
		t.Errorf("pid file not at expected path: %v", err)
	}
}

func TestReleasePIDFile_NoFileNoError(t *testing.T) {
	// release 不存在的文件 不应该 panic / log fatal
	releasePIDFile(filepath.Join(t.TempDir(), "no-such.pid"))
}

func TestHealthzHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	healthzHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type=%q want application/json", got)
	}
	var body struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if !body.OK {
		t.Errorf("ok=false")
	}
	if body.Service != "biu" {
		t.Errorf("service=%q", body.Service)
	}
	if body.Mode != "serve" {
		t.Errorf("mode=%q", body.Mode)
	}
}
