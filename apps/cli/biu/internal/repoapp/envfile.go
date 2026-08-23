// Minimal .env file support for repo-app instances. Hand-rolled parser
// (no third-party dotenv dep): KEY=VALUE lines, `#` comments, optional
// `export ` prefix, optional single/double quotes around the value.

package repoapp

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// ParseEnv parses dotenv-style content into a map. Malformed lines (no
// `=`) are skipped silently — a user hand-editing .env shouldn't be able
// to wedge the runner with a stray blank-ish line.
func ParseEnv(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// LoadEnvFile reads an instance .env; a missing file yields an empty
// map (not an error).
func LoadEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return ParseEnv(raw), nil
}

// WriteEnvFile writes kv as a .env file with mode 0600 — it holds
// plaintext secrets by design (TechPlan §3.5 D9), so the permission is
// part of the contract, not a nicety.
func WriteEnvFile(path string, kv map[string]string) error {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, kv[k])
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
