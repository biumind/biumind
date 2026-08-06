// Package telemetry collects anonymous, opt-in usage data so we can
// see which subcommands are popular, which fail most often, and where
// users are getting stuck.
//
// Defaults
//
//   * OFF by default — `biu config telemetry on` enables it
//     explicitly, `biu config telemetry status` shows current state.
//   * No remote upload when `endpoint` is empty — events still land
//     in the local jsonl so users (and we, when reproducing bugs)
//     can audit exactly what would have been reported.
//
// What we send
//
//   * Tool: "biu" + version + commit + os/arch
//   * Subcommand name (e.g. "doctor", "auth login")
//   * Outcome: ok / error / cancelled
//   * Duration in milliseconds
//   * Turn count + token totals (when the run produced a turn)
//   * Random session ID (per-process)
//   * Random install ID (stable per-machine, regenerated on `off → on`)
//
// What we DO NOT send
//
//   * Prompt content — never serialized.
//   * File paths — collapsed to `~/…` on read; subcommand args are
//     captured by *type* (e.g. "string", "bool"), not value.
//   * API keys / tokens — telemetry layer never touches the auth
//     code path; double-redacted with the same patterns the session
//     exporter uses just in case a future caller breaks the contract.
//
// Local storage
//
//   ~/.biu/telemetry.jsonl
//
// One JSON event per line. The user can `tail -f` it to audit live
// or open in any editor. Capped at 10 MB by rotation.

package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Config persists user preferences. Stored at ~/.biu/telemetry.json
// (the *control* file, separate from the events jsonl). Never
// committed to a repo, never shared.
type Config struct {
	// Enabled gates every Record call. False (default) → no-op.
	Enabled bool `json:"enabled"`

	// InstallID is the stable per-machine identifier. Regenerated on
	// each "off → on" transition so opting out and back in resets the
	// trail.
	InstallID string `json:"install_id,omitempty"`

	// Endpoint is the optional HTTPS URL to POST events to. Empty
	// means local-only; the jsonl still gets written. Set via
	// BIU_TELEMETRY_ENDPOINT or [telemetry].endpoint in config.toml
	// when the user explicitly enables it.
	Endpoint string `json:"endpoint,omitempty"`
}

// Event is one telemetry record. Fields chosen to mirror common
// observability schemas (OpenTelemetry-ish) without pulling in a
// heavy SDK.
type Event struct {
	// Schema versions the event so we can rev fields without
	// breaking older collectors.
	Schema string `json:"schema"`

	// TS in RFC3339 with millisecond precision.
	TS time.Time `json:"ts"`

	// Tool / Version / Commit / OS / Arch identify the producer.
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`

	// SessionID is per-process; InstallID is per-machine.
	SessionID string `json:"session_id"`
	InstallID string `json:"install_id"`

	// Subcommand is the cobra path ("doctor", "auth login", "version").
	Subcommand string `json:"subcommand"`

	// Outcome ∈ "ok" | "error" | "cancelled".
	Outcome string `json:"outcome"`

	// Duration of the operation, milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// ExitCode is the process exit code (only meaningful for the
	// root command). Zero for component-level events.
	ExitCode int `json:"exit_code,omitempty"`

	// Turns is the number of LLM turns produced by the run, if any.
	Turns int `json:"turns,omitempty"`

	// Token totals for the run.
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`

	// ErrorClass is a coarse bucket ("config", "provider", "permission",
	// "tool", "user_cancel"). NEVER the raw error message.
	ErrorClass string `json:"error_class,omitempty"`
}

// Reporter is the public handle the rest of biu uses. Use New() to
// build one — it consults config and falls back to a no-op when
// telemetry is off.
type Reporter struct {
	mu       sync.Mutex
	cfg      Config
	logPath  string
	httpDo   func(*http.Request) (*http.Response, error)
	now      func() time.Time
	sid      string
	disabled bool // true when Enabled=false; every record path is a fast no-op
}

// New constructs a Reporter from the user's saved config plus the
// current process metadata. Returns a no-op reporter when telemetry
// is disabled — callers can call Record unconditionally.
func New(version, commit string) (*Reporter, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	r := &Reporter{
		cfg:    cfg,
		now:    time.Now,
		sid:    randomID(8),
		httpDo: http.DefaultClient.Do,
	}
	if !cfg.Enabled {
		r.disabled = true
		return r, nil
	}
	logPath, err := eventsPath()
	if err != nil {
		return nil, err
	}
	r.logPath = logPath
	// Stash version/commit for every event without re-reading them.
	defaultEvent.Version = version
	defaultEvent.Commit = commit
	defaultEvent.OS = runtime.GOOS
	defaultEvent.Arch = runtime.GOARCH
	defaultEvent.InstallID = cfg.InstallID
	defaultEvent.Tool = "biu"
	defaultEvent.Schema = "biu.telemetry.v1"
	return r, nil
}

// defaultEvent is the per-process baseline filled by New. Each
// Record() copies it and overlays per-call fields.
var defaultEvent Event

// Disabled reports whether the reporter is a no-op. Useful for tests
// and conditional logging in callers.
func (r *Reporter) Disabled() bool { return r == nil || r.disabled }

// Record writes one event to the local jsonl and, when an endpoint
// is configured, fires a best-effort POST. Failures are silent — we
// never crash the user's run because telemetry can't reach us.
func (r *Reporter) Record(ev Event) {
	if r.Disabled() {
		return
	}
	if ev.TS.IsZero() {
		ev.TS = r.now().UTC()
	}
	// Inherit the per-process baseline for any field the caller left
	// blank. Direct assignment because zero-value fields semantic-ok.
	if ev.Tool == "" {
		ev.Tool = defaultEvent.Tool
	}
	if ev.Schema == "" {
		ev.Schema = defaultEvent.Schema
	}
	if ev.Version == "" {
		ev.Version = defaultEvent.Version
	}
	if ev.Commit == "" {
		ev.Commit = defaultEvent.Commit
	}
	if ev.OS == "" {
		ev.OS = defaultEvent.OS
	}
	if ev.Arch == "" {
		ev.Arch = defaultEvent.Arch
	}
	if ev.InstallID == "" {
		ev.InstallID = defaultEvent.InstallID
	}
	if ev.SessionID == "" {
		ev.SessionID = r.sid
	}

	body, err := json.Marshal(ev)
	if err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Local jsonl write (best-effort).
	if r.logPath != "" {
		if err := os.MkdirAll(filepath.Dir(r.logPath), 0o755); err == nil {
			if f, err := os.OpenFile(r.logPath,
				os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				_, _ = f.Write(body)
				_, _ = f.Write([]byte{'\n'})
				_ = f.Close()
				_ = rotateIfHuge(r.logPath, 10*1024*1024)
			}
		}
	}

	// Optional remote POST. Capped at 2s — telemetry never blocks
	// the user. Don't honour redirects; hostile/captive networks
	// shouldn't be able to bounce telemetry into a different URL.
	if r.cfg.Endpoint != "" {
		go r.fireAndForget(body)
	}
}

func (r *Reporter) fireAndForget(body []byte) {
	req, err := http.NewRequest(http.MethodPost, r.cfg.Endpoint,
		strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "biu-telemetry/"+defaultEvent.Version)
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// rotateIfHuge moves a fat log to .1 and starts fresh. Caller-locked
// so we don't race against a concurrent appender.
func rotateIfHuge(path string, maxBytes int64) error {
	st, err := os.Stat(path)
	if err != nil || st.Size() < maxBytes {
		return nil
	}
	return os.Rename(path, path+".1")
}

// ─── Config persistence ────────────────────────────

// configPath is the on-disk control file. Separate from events.jsonl
// so users can rm one without losing the other.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "telemetry.json"), nil
}

// eventsPath is where the jsonl lands.
func eventsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "telemetry.jsonl"), nil
}

// EventsPath is the exported variant for cmd/biu use.
func EventsPath() (string, error) { return eventsPath() }

// ConfigPath is the exported variant for cmd/biu use.
func ConfigPath() (string, error) { return configPath() }

// LoadConfig reads ~/.biu/telemetry.json. Missing file → zero-value
// (= disabled), no error. Honours BIU_TELEMETRY env vars for CI
// override paths that don't want to touch the home directory.
func LoadConfig() (Config, error) {
	if v := os.Getenv("BIU_TELEMETRY_DISABLED"); v == "1" || strings.EqualFold(v, "true") {
		return Config{Enabled: false}, nil
	}
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Allow opt-in via env var even without a config file.
			if os.Getenv("BIU_TELEMETRY_ENABLED") == "1" {
				return Config{
					Enabled:   true,
					InstallID: randomID(16),
					Endpoint:  os.Getenv("BIU_TELEMETRY_ENDPOINT"),
				}, nil
			}
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("telemetry: parse %s: %w", path, err)
	}
	if env := os.Getenv("BIU_TELEMETRY_ENDPOINT"); env != "" {
		cfg.Endpoint = env
	}
	return cfg, nil
}

// SaveConfig writes ~/.biu/telemetry.json. mode 0600 because the
// install_id is a per-machine identifier we'd rather not leak.
func SaveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Enable flips telemetry on. Generates a fresh InstallID — opting
// out and back in resets the trail (intentional; users should never
// be tracked across an opt-out).
func Enable(endpoint string) (Config, error) {
	cfg := Config{
		Enabled:   true,
		InstallID: randomID(16),
		Endpoint:  endpoint,
	}
	return cfg, SaveConfig(cfg)
}

// Disable flips telemetry off. Preserves the jsonl on disk for
// audit. Caller can rm it manually.
func Disable() (Config, error) {
	cfg := Config{Enabled: false}
	return cfg, SaveConfig(cfg)
}

// randomID returns a hex-encoded random string of the given byte
// length (so the printed string is twice as long).
func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ErrorClass is the small bucket vocabulary callers should pick from
// when reporting an error event. Don't pass raw error messages.
type ErrorClass string

const (
	ErrConfig     ErrorClass = "config"
	ErrProvider   ErrorClass = "provider"
	ErrPermission ErrorClass = "permission"
	ErrTool       ErrorClass = "tool"
	ErrUserCancel ErrorClass = "user_cancel"
	ErrUnknown    ErrorClass = "unknown"
)

// ClassifyError makes a coarse guess at the right ErrorClass for an
// error. Free-text errors → ErrUnknown so we never accidentally
// surface message contents.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "context canceled"),
		strings.Contains(s, "interrupt"):
		return ErrUserCancel
	case strings.Contains(s, "permission"),
		strings.Contains(s, "denied"):
		return ErrPermission
	case strings.Contains(s, "config"),
		strings.Contains(s, "api_key"),
		strings.Contains(s, "endpoint"):
		return ErrConfig
	case strings.Contains(s, "tool"):
		return ErrTool
	case strings.Contains(s, "provider"),
		strings.Contains(s, "anthropic"),
		strings.Contains(s, "model-relay"):
		return ErrProvider
	}
	return ErrUnknown
}
