package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// acquirePIDFile 接管语义单测。重点验高风险分支:
//   - stale(死进程持有)→ 覆盖
//   - 存活但非 biu serve → 拒绝(绝不误杀无关进程)
//
// "存活 biu serve → 接管"这条难以在单测里安全造出(需一个 ps 命令行含
// biu+serve 的真进程),由实机回归覆盖;此处用 sleep 子进程验拒绝侧的安全闸。

func TestAcquirePIDFile_StaleOverwrite(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "biu.pid")

	// 造一个"死"pid:启一个秒退进程,Wait 回收后它的 pid 不再存活。
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatalf("spawn true: %v", err)
	}
	deadPID := dead.Process.Pid
	if err := os.WriteFile(pf, []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := acquirePIDFile(pf); err != nil {
		t.Fatalf("stale pid 应被覆盖,却报错: %v", err)
	}
	got, _ := os.ReadFile(pf)
	if strings.TrimSpace(string(got)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid 文件未写入自身 pid: got=%q want=%d", strings.TrimSpace(string(got)), os.Getpid())
	}
}

func TestAcquirePIDFile_RefusesLiveNonBiu(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "biu.pid")

	// 一个存活但显然不是 biu serve 的进程(sleep)→ acquirePIDFile 必须拒绝,
	// 不得终止它(防 pid 复用误杀)。
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill(); _ = sleep.Wait() })
	holderPID := sleep.Process.Pid
	if err := os.WriteFile(pf, []byte(strconv.Itoa(holderPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := acquirePIDFile(pf)
	if err == nil {
		t.Fatal("锁被存活的非-biu 进程持有,应拒绝启动")
	}
	if !strings.Contains(err.Error(), "not a biu serve") {
		t.Errorf("拒绝原因应说明非 biu serve,实际: %v", err)
	}
	// 关键安全断言:拒绝路径绝不能杀掉无关进程。
	if !processAlive(holderPID) {
		t.Error("acquirePIDFile 误杀了无关的存活进程")
	}
	// pid 文件应保持持有者 pid 不变(未被接管覆盖)。
	got, _ := os.ReadFile(pf)
	if strings.TrimSpace(string(got)) != strconv.Itoa(holderPID) {
		t.Errorf("拒绝时不应改写 pid 文件: got=%q", strings.TrimSpace(string(got)))
	}
}

func TestProcessIsBiuServe_FalseForSleep(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill(); _ = sleep.Wait() })
	if processIsBiuServe(sleep.Process.Pid) {
		t.Error("sleep 进程不应被判为 biu serve")
	}
}

func TestTerminatePID_KillsLiveProcess(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := sleep.Process.Pid
	terminatePID(pid)
	// 回收(否则僵尸),再断言已死。
	_ = sleep.Wait()
	// 给 reaper 一点时间。
	for i := 0; i < 10 && processAlive(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Error("terminatePID 未能终止存活进程")
	}
}
