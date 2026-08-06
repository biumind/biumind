// Claude plugins compatibility: read plugins from ~/.claude/plugins/
// in addition to the biu-native ~/.biumind/plugins/ root.
//
// Why: users with an existing `~/.claude/plugins/` tree (installed
// via a Claude marketplace flow, or hand-dropped) can activate those
// plugins in biu unchanged so the switch is zero-friction. The
// on-disk layout matches biu's exactly (.claude-plugin/plugin.json +
// commands/ + agents/ + hooks/), so no translation is needed at the
// path level.
//
// Foreign manifest keys (channels / lspServers / userConfig /
// outputStyles meta) round-trip through the loader's Unrecognised
// map without erroring — biu just doesn't act on them. See
// PluginManifest.UnmarshalJSON.
//
// Read-only by design: `biu plugin install / uninstall` always
// targets ~/.biumind/plugins/ (the user-owned biu root). The
// compat path is a discovery surface; modifications go to the
// biu-native location so external and biu installs don't fight
// over the same directory.
package plugins

import (
	"os"
	"path/filepath"
)

// CompatClaudeRoot returns ~/.claude/plugins/ when it exists, or
// "" when the directory is absent. Returning "" rather than the
// path lets callers append unconditionally — `LoadAll` already
// silently skips non-existent roots, but signalling "not present"
// also avoids a stat() in the hot path.
//
// $HOME resolution failures (rare, but possible inside container
// init paths) return "" too — there's no useful action to take if
// we can't find a home directory.
func CompatClaudeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "plugins")
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		return root
	}
	return ""
}
