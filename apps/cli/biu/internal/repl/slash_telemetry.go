// /telemetry slash + helpers. Surfaces the user's persisted
// telemetry config + lets them tail / export the on-disk events
// JSONL. Lives next to the rest of the slash family so it can
// move independently of the model lifecycle.

package repl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/telemetry"
)

// handleTelemetry dispatches /telemetry subcommands. Stateless —
// every call hits the on-disk config + events file directly so an
// out-of-band edit (or `biu telemetry enable` from another shell)
// is reflected immediately.
//
// Sub-commands:
//
//	bare              — config status + line count
//	tail [N]          — print the last N events (default 10)
//	export <path>     — copy ~/.biu/telemetry.jsonl to <path>
//	enable <endpoint> — telemetry.Enable(endpoint), persist
//	disable           — telemetry.Disable, persist
func (m model) handleTelemetry(parts []string) string {
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	switch sub {
	case "":
		return renderTelemetryStatus()
	case "tail":
		n := 10
		if len(parts) >= 3 {
			parsed, err := strconv.Atoi(parts[2])
			if err != nil || parsed <= 0 {
				return "/telemetry: tail count must be a positive integer; got " + parts[2]
			}
			n = parsed
		}
		return renderTelemetryTail(n)
	case "export":
		if len(parts) < 3 {
			return "/telemetry: usage: /telemetry export <path>"
		}
		return renderTelemetryExport(parts[2])
	case "enable":
		if len(parts) < 3 {
			return "/telemetry: usage: /telemetry enable <endpoint-url>"
		}
		cfg, err := telemetry.Enable(parts[2])
		if err != nil {
			return "/telemetry: " + err.Error()
		}
		return fmt.Sprintf("/telemetry: enabled · endpoint=%s · install_id=%s",
			cfg.Endpoint, redactID(cfg.InstallID))
	case "disable":
		if _, err := telemetry.Disable(); err != nil {
			return "/telemetry: " + err.Error()
		}
		return "/telemetry: disabled (events file kept; export with `/telemetry export <path>`)"
	default:
		return "/telemetry: usage: /telemetry [tail [N]|export <path>|enable <endpoint>|disable]"
	}
}

// renderTelemetryStatus shows the config + size of the on-disk
// events log. Status note format:
//
//	telemetry: <on|off>
//	  events file: ~/.biu/telemetry.jsonl (NN events, M.MM KB)
//	  config:      ~/.biu/telemetry.json
//	  endpoint:    <url|none>
//	  install_id:  <redacted>
func renderTelemetryStatus() string {
	cfg, err := telemetry.LoadConfig()
	if err != nil {
		return "/telemetry: " + err.Error()
	}
	state := "off"
	if cfg.Enabled {
		state = "on"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "telemetry: %s\n", state)

	eventsFile, eErr := telemetry.EventsPath()
	configFile, _ := telemetry.ConfigPath()
	if eErr == nil {
		count, size := telemetryFileStats(eventsFile)
		fmt.Fprintf(&b, "  events file: %s (%d events, %s)\n",
			eventsFile, count, formatBytes(size))
	}
	fmt.Fprintf(&b, "  config:      %s\n", configFile)
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "(none — events written to disk only)"
	}
	fmt.Fprintf(&b, "  endpoint:    %s\n", endpoint)
	fmt.Fprintf(&b, "  install_id:  %s", redactID(cfg.InstallID))
	return b.String()
}

// renderTelemetryTail prints the last N events from the events
// jsonl. Reads the whole file (telemetry rotates at MaxBytes so
// it's bounded). Each line is already JSON; we re-format the
// timestamp + outcome for readability.
func renderTelemetryTail(n int) string {
	path, err := telemetry.EventsPath()
	if err != nil {
		return "/telemetry: " + err.Error()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "/telemetry: no events yet (file " + path + " not created)"
		}
		return "/telemetry: " + err.Error()
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "/telemetry: events file is empty"
	}
	if n > len(lines) {
		n = len(lines)
	}
	tail := lines[len(lines)-n:]
	var b strings.Builder
	fmt.Fprintf(&b, "telemetry: last %d events from %s\n", len(tail), path)
	for _, line := range tail {
		var ev telemetry.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			fmt.Fprintf(&b, "  (unparseable line: %s)\n", truncateLine(line, 100))
			continue
		}
		ts := ev.TS.Format("15:04:05")
		fmt.Fprintf(&b, "  %s  %-12s  %-8s",
			ts, ev.Subcommand, ev.Outcome)
		if ev.DurationMs > 0 {
			fmt.Fprintf(&b, "  %dms", ev.DurationMs)
		}
		if ev.Turns > 0 {
			fmt.Fprintf(&b, "  turns=%d", ev.Turns)
		}
		if ev.ErrorClass != "" {
			fmt.Fprintf(&b, "  error=%s", ev.ErrorClass)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderTelemetryExport copies the events file to `dest`. We don't
// shell out — `os.ReadFile` + `os.WriteFile` keeps biu's binary
// hermetic and the operation works under sandboxed shells.
func renderTelemetryExport(dest string) string {
	path, err := telemetry.EventsPath()
	if err != nil {
		return "/telemetry: " + err.Error()
	}
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "/telemetry: no events to export (file " + path + " not created)"
		}
		return "/telemetry: " + err.Error()
	}
	abs := dest
	if !filepath.IsAbs(dest) {
		if cwd, err := os.Getwd(); err == nil {
			abs = filepath.Join(cwd, dest)
		}
	}
	if err := os.WriteFile(abs, src, 0o600); err != nil {
		return "/telemetry: " + err.Error()
	}
	count := strings.Count(strings.TrimRight(string(src), "\n"), "\n") + 1
	if len(src) == 0 {
		count = 0
	}
	return fmt.Sprintf("/telemetry: exported %d events → %s (%s)",
		count, abs, formatBytes(int64(len(src))))
}

// telemetryFileStats returns (line count, byte size) for the events
// file. Missing file yields (0, 0).
func telemetryFileStats(path string) (int, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, info.Size()
	}
	trimmed := strings.TrimRight(string(raw), "\n")
	if trimmed == "" {
		return 0, info.Size()
	}
	return strings.Count(trimmed, "\n") + 1, info.Size()
}
