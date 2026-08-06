package exechost

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestFor_Mapping(t *testing.T) {
	cases := map[string]Mode{
		"none":  ModeNone,
		"local": ModeLocal,
		"cloud": ModeCloud,
		"":      ModeLocal, // 空 → 兜底 local
		"bogus": ModeLocal, // 未知 → 兜底 local
	}
	for in, want := range cases {
		if got := For(in).Mode(); got != want {
			t.Errorf("For(%q).Mode() = %q, want %q", in, got, want)
		}
	}
}

func TestNoneHost_RejectsExec(t *testing.T) {
	_, err := NoneHost{}.Exec(context.Background(), ExecRequest{Argv: []string{"echo", "hi"}})
	if !errors.Is(err, ErrNoneHost) {
		t.Fatalf("NoneHost.Exec err = %v, want ErrNoneHost", err)
	}
}

func TestCloudHost_StubNotReady(t *testing.T) {
	_, err := CloudHost{}.Exec(context.Background(), ExecRequest{Argv: []string{"echo", "hi"}})
	if !errors.Is(err, ErrCloudNotReady) {
		t.Fatalf("CloudHost.Exec err = %v, want ErrCloudNotReady", err)
	}
}

func TestLocalHost_RunsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell test")
	}
	res, err := LocalHost{}.Exec(context.Background(), ExecRequest{
		Argv: []string{"/bin/sh", "-c", "printf hello"},
	})
	if err != nil {
		t.Fatalf("LocalHost.Exec: %v", err)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout = %q, want %q", res.Stdout, "hello")
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
}
