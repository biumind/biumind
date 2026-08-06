package driver

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStubLifecycle(t *testing.T) {
	ctx := context.Background()
	d := NewStub()

	sb, err := d.Create(ctx, CreateInput{OwnerID: "u1", Image: "alpine"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.ID == "" || !strings.HasPrefix(sb.ID, "stub-") {
		t.Errorf("bad id: %q", sb.ID)
	}
	if sb.Status != "running" {
		t.Errorf("want running, got %q", sb.Status)
	}

	got, err := d.Get(ctx, sb.ID)
	if err != nil || got.ID != sb.ID {
		t.Fatalf("get: %v / %+v", err, got)
	}

	list, err := d.List(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v / %d", err, len(list))
	}

	otherList, _ := d.List(ctx, "u2")
	if len(otherList) != 0 {
		t.Errorf("owner scoping leaked: %d", len(otherList))
	}

	if err := d.Pause(ctx, sb.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	got, _ = d.Get(ctx, sb.ID)
	if got.Status != "paused" {
		t.Errorf("status after pause: %q", got.Status)
	}

	if err := d.Resume(ctx, sb.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if err := d.Destroy(ctx, sb.ID); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := d.Get(ctx, sb.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-destroy get should be ErrNotFound, got %v", err)
	}
}

func TestStubExecCapturesOutputAndExit(t *testing.T) {
	ctx := context.Background()
	d := NewStub()
	sb, _ := d.Create(ctx, CreateInput{OwnerID: "u1"})

	var buf bytes.Buffer
	res, err := d.Exec(ctx, ExecInput{
		SandboxID: sb.ID,
		Argv:      []string{"sh", "-c", "echo hi; echo bye 1>&2; exit 7"},
	}, &buf)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", res.ExitCode)
	}
	out := buf.String()
	if !strings.Contains(out, "hi") || !strings.Contains(out, "bye") {
		t.Errorf("missing stream content: %q", out)
	}
}

func TestStubExecRequiresArgv(t *testing.T) {
	ctx := context.Background()
	d := NewStub()
	sb, _ := d.Create(ctx, CreateInput{OwnerID: "u1"})

	var buf bytes.Buffer
	_, err := d.Exec(ctx, ExecInput{SandboxID: sb.ID, Argv: nil}, &buf)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("want ErrInvalid, got %v", err)
	}
}

func TestStubRecordsCalls(t *testing.T) {
	ctx := context.Background()
	d := NewStub()
	sb, _ := d.Create(ctx, CreateInput{OwnerID: "u1"})
	_ = d.Pause(ctx, sb.ID)
	_ = d.Resume(ctx, sb.ID)
	_, _ = d.Snapshot(ctx, sb.ID)
	_ = d.Destroy(ctx, sb.ID)

	if got := strings.Join(d.Calls, "\n"); !strings.Contains(got, "create owner=u1") ||
		!strings.Contains(got, "pause") || !strings.Contains(got, "resume") ||
		!strings.Contains(got, "snapshot") || !strings.Contains(got, "destroy") {
		t.Errorf("missing recorded calls: %v", d.Calls)
	}
}

func TestStubExecRefusesPausedSandbox(t *testing.T) {
	ctx := context.Background()
	d := NewStub()
	sb, _ := d.Create(ctx, CreateInput{OwnerID: "u1"})
	_ = d.Pause(ctx, sb.ID)

	var buf bytes.Buffer
	_, err := d.Exec(ctx, ExecInput{SandboxID: sb.ID, Argv: []string{"true"}}, &buf)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("want not-running error, got %v", err)
	}
}
