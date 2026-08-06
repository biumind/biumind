package mcp

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestBootstrapDedup(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeFakeServer(t)
	r := NewRegistry()
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := r.Bootstrap(ctx, []BootstrapInput{
		{Source: "manual", Name: "fake", Command: "/bin/sh", Args: []string{path}},
		{Source: "plugin", Name: "fake-plugin-dup", Command: "/bin/sh", Args: []string{path}},
		{Source: "manual", Name: "different", Command: "/bin/sh", Args: []string{path, "--variant"}},
	}, nil)

	// First (manual) connects. Second (plugin) has same signature → skipped.
	// Third has different args → connects.
	if results[0].Skipped || results[0].Err != nil {
		t.Errorf("entry 0 should connect: %+v", results[0])
	}
	if !results[1].Skipped {
		t.Errorf("entry 1 should be skipped (dup of 0): %+v", results[1])
	}
	// Note: results[2] uses /bin/sh with one extra arg; the fake server
	// script ignores extra args so it still launches. Either Skipped=false
	// or Err is fine — we just want it not flagged as a dup of #0.
	if results[2].Skipped {
		t.Errorf("entry 2 different args, should not dedup: %+v", results[2])
	}
}

func TestBootstrapEnvExpansion(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	path := writeFakeServer(t)
	t.Setenv("FAKE_BIU_TEST", path)

	r := NewRegistry()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := r.Bootstrap(ctx, []BootstrapInput{
		{
			Source: "manual",
			Name:   "fake",
			Command: "/bin/sh",
			Args:    []string{"${FAKE_BIU_TEST}"},
			Env:     map[string]string{"BIU_DEFAULT": "${UNSET_VAR:-fallback}"},
		},
	}, nil)
	if results[0].Err != nil {
		t.Fatalf("err: %v", results[0].Err)
	}
	if len(results[0].Missing) != 0 {
		t.Errorf("FAKE_BIU_TEST + default should not be missing: %v", results[0].Missing)
	}
	if len(r.All()) == 0 {
		t.Errorf("expected at least one tool registered")
	}
}

func TestBootstrapMissingVarReported(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	os.Unsetenv("BIU_NEVER_SET")
	r := NewRegistry()
	defer r.Close()
	results := r.Bootstrap(context.Background(), []BootstrapInput{
		{
			Source:  "manual",
			Name:    "broken",
			Command: "/bin/sh",
			Args:    []string{"-c", "exit 0"},
			Env:     map[string]string{"X": "${BIU_NEVER_SET}"},
		},
	}, nil)
	if len(results[0].Missing) != 1 || results[0].Missing[0] != "BIU_NEVER_SET" {
		t.Errorf("expected missing var report, got %v", results[0].Missing)
	}
}
