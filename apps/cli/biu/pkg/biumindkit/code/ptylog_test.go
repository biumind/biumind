package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用 HOME 重定向到临时目录,隔离真实 ~/.biumind。
func withTempHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
}

func TestPTYLog_WriteReadRoundTrip(t *testing.T) {
	withTempHome(t)
	id := "task-abc"
	w := newPTYLogWriter(id, false)
	w.write([]byte("hello "))
	w.write([]byte("world"))
	w.close()

	got, err := readPTYLog(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestPTYLog_TruncateVsAppend(t *testing.T) {
	withTempHome(t)
	id := "task-xyz"
	w1 := newPTYLogWriter(id, false)
	w1.write([]byte("first"))
	w1.close()

	// append=true 保留旧内容
	w2 := newPTYLogWriter(id, true)
	w2.write([]byte("-second"))
	w2.close()
	got, _ := readPTYLog(id)
	if string(got) != "first-second" {
		t.Errorf("append: got %q", got)
	}

	// append=false truncate
	w3 := newPTYLogWriter(id, false)
	w3.write([]byte("fresh"))
	w3.close()
	got, _ = readPTYLog(id)
	if string(got) != "fresh" {
		t.Errorf("truncate: got %q", got)
	}
}

func TestPTYLog_SizeCapReadsTail(t *testing.T) {
	withTempHome(t)
	id := "task-big"
	w := newPTYLogWriter(id, false)
	big := strings.Repeat("A", ptyLogMaxReplay) + "TAIL"
	w.write([]byte(big))
	w.close()
	got, err := readPTYLog(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > ptyLogMaxReplay {
		t.Errorf("read %d bytes, want <= %d", len(got), ptyLogMaxReplay)
	}
	if !strings.HasSuffix(string(got), "TAIL") {
		t.Error("tail bytes should be retained")
	}
}

func TestPTYLog_MissingFileNoError(t *testing.T) {
	withTempHome(t)
	got, err := readPTYLog("never-ran")
	if err != nil {
		t.Fatalf("missing log should not error: %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %q", got)
	}
}

func TestPTYLog_Remove(t *testing.T) {
	withTempHome(t)
	id := "task-rm"
	newPTYLogWriter(id, false).close()
	p, _ := ptyLogPath(id)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("log file should exist: %v", err)
	}
	removePTYLog(id)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("log should be removed, stat err=%v", err)
	}
	_ = filepath.Base(p)
}
