package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/repoapp"
)

func TestRepoAppDoctorOutput(t *testing.T) {
	f := &rootFlags{}
	cmd := newRepoAppDoctorCmd(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "platform supported:") {
		t.Errorf("doctor must report platform support: %q", out)
	}
	// The fixed probe set, present whether found or missing.
	for _, name := range []string{"git", "python3", "uv", "node", "mise", "docker"} {
		if !strings.Contains(out, name) {
			t.Errorf("doctor output missing %q: %q", name, out)
		}
	}
}

func TestRepoAppListEmpty(t *testing.T) {
	t.Setenv("BIU_REPOAPP_ROOT", t.TempDir())
	f := &rootFlags{}
	cmd := newRepoAppListCmd(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no repo apps installed") {
		t.Errorf("empty store should print the hint, got %q", buf.String())
	}
}

func TestRepoAppListShowsInstance(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIU_REPOAPP_ROOT", root)
	store, err := repoapp.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := store.Create("owner-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := repoapp.SaveRuntime(inst.Dir, &repoapp.RuntimeInfo{
		RepoURL: "https://github.com/owner/repo.git",
		Stack:   "python",
		Ref:     "v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	f := &rootFlags{}
	cmd := newRepoAppListCmd(f)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "owner-repo") || !strings.Contains(out, "python") || !strings.Contains(out, "stopped") {
		t.Errorf("list output wrong: %q", out)
	}
}

// ─── --env merge (ensure / run) ─────────────────────────────

func TestMergeRepoAppEnv(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := repoapp.WriteEnvFile(envPath, map[string]string{
		"FLAVOUR": "vanilla",
		"OLD":     "keep",
	}); err != nil {
		t.Fatal(err)
	}

	err := mergeRepoAppEnv(envPath, []string{"FLAVOUR=chocolate", "API_KEY=sk-live"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	env, err := repoapp.LoadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"FLAVOUR": "chocolate", // flag wins over existing
		"OLD":     "keep",      // untouched keys survive
		"API_KEY": "sk-live",   // new key added
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%q] = %q want %q", k, env[k], v)
		}
	}
	if len(env) != len(want) {
		t.Errorf("merged %d keys want %d: %v", len(env), len(want), env)
	}

	// 0600 invariant survives the rewrite (file holds secrets, D9).
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %o want 600", info.Mode().Perm())
	}
}

func TestMergeRepoAppEnvRejectsMalformed(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	for _, bad := range []string{"NOEQUALS", "=novaluekey", "  =x"} {
		if err := mergeRepoAppEnv(envPath, []string{bad}); err == nil {
			t.Errorf("--env %q should be rejected", bad)
		}
	}
}

func TestMergeRepoAppEnvEmptyValueAllowed(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := mergeRepoAppEnv(envPath, []string{"EMPTY="}); err != nil {
		t.Fatalf("KEY= (empty value) must be accepted: %v", err)
	}
	env, err := repoapp.LoadEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("env[EMPTY] = %q ok=%v, want empty string present", v, ok)
	}
}

func TestMergeRepoAppEnvNoPairsIsNoop(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := mergeRepoAppEnv(envPath, nil); err != nil {
		t.Fatalf("nil pairs: %v", err)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Error("nil pairs must not create the .env file")
	}
}

func TestRepoAppEnsureHasEnvFlag(t *testing.T) {
	f := &rootFlags{}
	if newRepoAppEnsureCmd(f).Flags().Lookup("env") == nil {
		t.Error("ensure missing --env flag")
	}
	if newRepoAppRunCmd(f).Flags().Lookup("env") == nil {
		t.Error("run missing --env flag")
	}
}
