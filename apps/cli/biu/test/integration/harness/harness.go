//go:build integration

// Package harness provides shared utilities for biu's integration
// test suite — building the CLI binary once, isolating $HOME per
// test, gating real-API tests behind env vars, and capturing
// command output cleanly.
//
// All integration tests are tagged `integration` so they don't run
// in the default `go test ./...` sweep. Run with:
//
//   go test -tags=integration ./test/integration/...
//
// Real-API tests additionally require:
//
//   ANTHROPIC_API_KEY    — required to enable LLM scenarios
//   ANTHROPIC_BASE_URL   — optional proxy endpoint
//   ANTHROPIC_MODEL      — optional model id (default claude-sonnet-4-6)
//
// Tests without an API key still run for everything that doesn't
// need a real LLM (version, doctor, config, schema, etc.) — they
// just skip the LLM-driven layers.

package harness

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// modulePath is the import path of the biu module — used by
	// `go build` so the harness works from any cwd.
	modulePath = "github.com/biumind/biumind/apps/cli/biu"
	cmdPkg     = modulePath + "/cmd/biu"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// BiuBinary builds the biu binary into a process-shared temp file
// and returns its absolute path. Subsequent calls reuse the same
// build — `go build` is the slowest single operation in the suite,
// so we pay it once.
func BiuBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "biu-integration-bin-*")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "biu")
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, cmdPkg)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			binErr = err
		}
	})
	if binErr != nil {
		t.Fatalf("harness: build biu: %v", binErr)
	}
	return binPath
}

// Env captures the env-var slice passed to a child process. Build
// with NewEnv + With… so each test stays explicit about which vars
// are inherited from the parent process.
type Env struct {
	pairs []string
}

// NewEnv returns an Env seeded with PATH (so /bin/sh, git, sandbox-
// exec, etc. resolve) plus the bare minimum needed for `go` and
// network access.
func NewEnv() *Env {
	e := &Env{}
	for _, k := range []string{
		"PATH",         // every shell needs it
		"SHELL",        // bash sometimes touches it
		"LANG", "LC_ALL", "LC_CTYPE",
		"TZ",
		"GOPATH", "GOCACHE", "GOMODCACHE", // go runtime caches if biu shells out to go
		"SSL_CERT_FILE", "SSL_CERT_DIR",   // TLS roots for http providers
	} {
		if v := os.Getenv(k); v != "" {
			e.pairs = append(e.pairs, k+"="+v)
		}
	}
	return e
}

// Set adds or replaces an env entry.
func (e *Env) Set(k, v string) *Env {
	prefix := k + "="
	for i, p := range e.pairs {
		if strings.HasPrefix(p, prefix) {
			e.pairs[i] = prefix + v
			return e
		}
	}
	e.pairs = append(e.pairs, prefix+v)
	return e
}

// Slice returns a copy suitable for cmd.Env.
func (e *Env) Slice() []string {
	out := make([]string, len(e.pairs))
	copy(out, e.pairs)
	return out
}

// Sandbox is one fully-isolated test environment: an empty $HOME,
// an empty cwd, and a pre-seeded Env. Pass it to RunBiu via
// RunOpts.Sandbox.
type Sandbox struct {
	Home string
	Cwd  string
	Env  *Env
}

// NewSandbox returns a Sandbox under t.TempDir(). Home and Cwd are
// distinct so a test can verify path semantics — biu's settings
// loader looks at $HOME for ~/.biumind and at cwd for project
// .biumind/.
func NewSandbox(t *testing.T) *Sandbox {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "cwd")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("harness: mkdir home: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("harness: mkdir cwd: %v", err)
	}
	env := NewEnv().Set("HOME", home)
	return &Sandbox{Home: home, Cwd: cwd, Env: env}
}

// WriteFile writes a file under the sandbox cwd at the given relative
// path, creating parent dirs.
func (s *Sandbox) WriteFile(t *testing.T, rel string, body []byte) string {
	t.Helper()
	abs := filepath.Join(s.Cwd, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("harness: mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("harness: write %s: %v", rel, err)
	}
	return abs
}

// RunOpts controls a single biu invocation.
type RunOpts struct {
	// Sandbox is the isolated $HOME + cwd. Required.
	Sandbox *Sandbox

	// Args are the biu sub-command + flags (excluding argv[0]).
	Args []string

	// Stdin, when non-nil, is piped to the child as standard input.
	Stdin []byte

	// Timeout caps the run. Zero defaults to 30 seconds — most
	// non-LLM commands resolve in under 1s. LLM-driven runs need
	// per-call overrides.
	Timeout time.Duration
}

// Result captures everything an integration test typically asserts
// against.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
	Elapsed  time.Duration
}

// CombinedOK reports stdout when the run exited 0 and the test
// helper hasn't already failed; otherwise it fails the test with
// a clear diagnostic.
func (r Result) CombinedOK(t *testing.T) string {
	t.Helper()
	if r.Err != nil && !isExitError(r.Err) {
		t.Fatalf("biu run failed (non-exit error): %v\nstderr: %s", r.Err, r.Stderr)
	}
	if r.ExitCode != 0 {
		t.Fatalf("biu exited %d (expected 0)\nstderr: %s\nstdout: %s",
			r.ExitCode, r.Stderr, r.Stdout)
	}
	return r.Stdout
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

// RunBiu invokes the binary with the given options and returns a
// Result. Never panics — every failure mode lands in Result.Err
// or Result.ExitCode so tests can tell intentional non-zero exits
// from actual breakage.
func RunBiu(t *testing.T, opt RunOpts) Result {
	t.Helper()
	if opt.Sandbox == nil {
		t.Fatal("harness: RunOpts.Sandbox is required")
	}
	bin := BiuBinary(t)
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, opt.Args...)
	cmd.Dir = opt.Sandbox.Cwd
	cmd.Env = opt.Sandbox.Env.Slice()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if opt.Stdin != nil {
		cmd.Stdin = bytes.NewReader(opt.Stdin)
	}

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	r := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Elapsed:  elapsed,
		Err:      err,
		ExitCode: 0,
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.ExitCode = ee.ExitCode()
			r.Err = nil // exit-code errors aren't transport failures
		} else {
			r.ExitCode = -1
		}
	}
	return r
}

// AnthropicEnv carries the resolved API endpoint configuration for
// LLM-driven scenarios. Returned by RequireRealAPI.
type AnthropicEnv struct {
	APIKey  string
	BaseURL string
	Model   string
}

// Apply layers Anthropic env vars onto the sandbox env so child
// biu processes pick them up.
func (a AnthropicEnv) Apply(s *Sandbox) {
	s.Env.Set("ANTHROPIC_API_KEY", a.APIKey)
	if a.BaseURL != "" {
		s.Env.Set("ANTHROPIC_BASE_URL", a.BaseURL)
	}
	if a.Model != "" {
		s.Env.Set("ANTHROPIC_MODEL", a.Model)
	}
}

// SeedDirectConfig writes ~/.biu/config.toml inside the sandbox so
// `biu --mode=direct` (or the cfg's [default].mode) finds a valid
// [providers.anthropic] block. Required for LLM-driven CLI tests
// because biu's cmd entrypoint reads the API key from config, NOT
// from the env directly (the env vars are honoured by the SDK,
// which is a separate code path).
//
// Returns the absolute path of the written config so callers can
// inspect it on assertion failures.
func (s *Sandbox) SeedDirectConfig(t *testing.T, a AnthropicEnv) string {
	t.Helper()
	dir := filepath.Join(s.Home, ".biu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("harness: mkdir .biu: %v", err)
	}
	path := filepath.Join(dir, "config.toml")
	body := "[default]\n" +
		"mode = \"direct\"\n" +
		"provider = \"anthropic\"\n"
	if a.Model != "" {
		body += "model = \"" + a.Model + "\"\n"
	}
	body += "\n[providers.anthropic]\n" +
		"api_key = \"" + a.APIKey + "\"\n"
	if a.BaseURL != "" {
		body += "endpoint = \"" + a.BaseURL + "\"\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("harness: write config.toml: %v", err)
	}
	return path
}

// RequireRealAPI skips the test unless ANTHROPIC_API_KEY is set in
// the parent process. Returns the resolved env so the caller can
// pass it to Sandbox.Apply or to its own LLM client.
func RequireRealAPI(t *testing.T) AnthropicEnv {
	t.Helper()
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set; integration test skipped (set it + " +
			"optionally ANTHROPIC_BASE_URL / ANTHROPIC_MODEL to run real-API tests)")
	}
	return AnthropicEnv{
		APIKey:  apiKey,
		BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		Model:   firstNonEmpty(os.Getenv("ANTHROPIC_MODEL"), "claude-sonnet-4-6"),
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
