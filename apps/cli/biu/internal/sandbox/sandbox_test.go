package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestWrapReturnsRunnableCmd(t *testing.T) {
	cmd, mode := Wrap(context.Background(), "echo hello", Options{Cwd: "/tmp"})
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v out=%s mode=%s", err, out, mode)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("output: %s", out)
	}
}

func TestWrapMacProfileMentionsCwd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{Cwd: "/tmp/proj"})
	if !strings.Contains(prof, "/tmp/proj") {
		t.Errorf("profile missing cwd: %s", prof)
	}
	if !strings.Contains(prof, "deny file-write*") {
		t.Errorf("profile must deny writes by default")
	}
}

func TestWrapNoNetworkProfile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	prof := buildMacProfile(Options{Cwd: "/tmp", AllowNetwork: false})
	if !strings.Contains(prof, "deny network*") {
		t.Errorf("network must be denied: %s", prof)
	}
}
