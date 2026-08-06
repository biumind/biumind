// /env slash — print environment variables that influence biu.
//
// We don't dump full os.Environ() — most of it is shell noise. The
// allowlist below is the set of vars biu actually reads at startup
// or per-request, plus a few of the common LLM-provider keys so
// users can sanity-check why a particular model is unavailable.
//
// Every value is filtered through redactSecret before display.
// Anything matching a "looks like a secret" heuristic (KEY / TOKEN /
// SECRET / PASSWORD / ANTHROPIC / OPENAI in the var name) is shown
// as "<set, …last4>" instead of the full value. /env is the kind of
// thing people paste into bug reports; we want it to be safe-by-
// default.

package repl

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// envVars lists the names biu cares about, grouped + ordered for
// readability. Adding a new env var to biu? Add it here too — the
// list doubles as documentation.
var envVars = []struct {
	name  string
	group string
}{
	// Provider auth
	{"ANTHROPIC_API_KEY", "provider"},
	{"OPENAI_API_KEY", "provider"},
	{"BIU_API_KEY", "provider"},
	{"BIU_MODEL_RELAY_URL", "provider"},
	{"BIU_PROVIDER", "provider"},
	{"BIU_MODEL", "provider"},
	{"BIU_DIRECT", "provider"},

	// Local config
	{"BIUMIND_HOME", "config"},
	{"BIU_HOME", "config"},
	{"BIU_CONFIG", "config"},
	{"BIU_LOG_LEVEL", "config"},
	{"BIU_TELEMETRY", "config"},
	{"DISABLE_COMPACT", "config"},
	{"BIU_DISABLE_AUTO_DREAM", "config"},

	// Bridge / IDE
	{"BIU_BRIDGE_URL", "bridge"},
	{"BIU_BRIDGE_TOKEN", "bridge"},

	// Sandbox / shell
	{"BIU_SANDBOX_MODE", "sandbox"},
	{"SHELL", "shell"},
	{"PATH", "shell"},
	{"HOME", "shell"},
	{"PWD", "shell"},

	// Plugins
	{"BIU_PLUGIN_PATH", "plugins"},
	{"BIU_DISABLE_PLUGINS", "plugins"},
}

func (m model) handleEnv(parts []string) string {
	filter := ""
	if len(parts) >= 2 {
		filter = strings.ToLower(parts[1])
	}

	// Walk in stable group order so output diffs across runs.
	groups := []string{"provider", "config", "bridge", "sandbox", "shell", "plugins"}
	byGroup := map[string][]string{}
	for _, v := range envVars {
		if filter != "" && !strings.Contains(strings.ToLower(v.name), filter) &&
			!strings.Contains(v.group, filter) {
			continue
		}
		byGroup[v.group] = append(byGroup[v.group], v.name)
	}

	var b strings.Builder
	b.WriteString("/env: biu-relevant environment\n")
	any := false
	for _, g := range groups {
		names := byGroup[g]
		if len(names) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "\n[%s]\n", g)
		sort.Strings(names)
		for _, name := range names {
			val := os.Getenv(name)
			if val == "" {
				fmt.Fprintf(&b, "  %-22s (unset)\n", name)
				continue
			}
			fmt.Fprintf(&b, "  %-22s %s\n", name, redactSecret(name, val))
		}
	}
	if !any {
		return "/env: no vars match filter " + filter
	}
	return strings.TrimRight(b.String(), "\n")
}

// redactSecret returns the value safe for display. Anything whose
// name suggests credentials becomes "<set, …last4>"; PATH gets
// truncated to 80 chars; everything else passes through.
func redactSecret(name, val string) string {
	upper := strings.ToUpper(name)
	sensitive := false
	for _, needle := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASS", "ANTHROPIC_API", "OPENAI_API"} {
		if strings.Contains(upper, needle) {
			sensitive = true
			break
		}
	}
	if sensitive {
		if len(val) <= 4 {
			return "<set>"
		}
		return fmt.Sprintf("<set, …%s>", val[len(val)-4:])
	}
	if name == "PATH" && len(val) > 80 {
		return val[:77] + "…"
	}
	return val
}
