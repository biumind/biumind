// Variable expansion in plugin manifest values.
//
// Existing plugins use ${CLAUDE_PLUGIN_ROOT} in hook commands and MCP
// server command paths to refer to the plugin's installation
// directory. Without expansion the path "python3
// ${CLAUDE_PLUGIN_ROOT}/hooks/foo.py" reaches the hook runner
// unresolved and the subprocess fails with a confusing error.
//
// We support two synonyms:
//
//	${CLAUDE_PLUGIN_ROOT}  — original ecosystem name; preserved for
//	                         drop-in compatibility with existing plugins
//	${BIU_PLUGIN_ROOT}     — biu-native name; what new bundled
//	                         plugins should use
//
// Both expand to the same absolute path, the LoadedPlugin.Path that
// the loader has already resolved. Any other ${VAR} is left untouched
// — biu does NOT expand the user's shell environment in plugin
// values (security: a plugin author can't read $AWS_SECRET_KEY by
// dropping it into a hook command).
package plugins

import (
	"regexp"
	"strings"
)

// pluginVarRE matches the two recognised variable references. We
// don't try to be a general-purpose shell-style expander — only
// the two plugin-root tokens are substituted, everything else
// (including unrecognised ${VAR}) survives intact.
var pluginVarRE = regexp.MustCompile(`\$\{(CLAUDE_PLUGIN_ROOT|BIU_PLUGIN_ROOT)\}`)

// expandVars replaces every recognised plugin-root token in s with
// pluginRoot. Unrecognised ${...} references pass through untouched.
//
// Caller responsibility: pluginRoot is expected to be absolute and
// pre-cleaned by the loader. expandVars itself doesn't validate.
func expandVars(s, pluginRoot string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return pluginVarRE.ReplaceAllString(s, pluginRoot)
}

// expandStringSlice applies expandVars to each element of args.
// Returns a new slice; the input is not mutated. Empty / nil input
// returns nil so JSON round-trips don't gain spurious empty arrays.
func expandStringSlice(args []string, pluginRoot string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = expandVars(a, pluginRoot)
	}
	return out
}

// expandStringMap applies expandVars to each map value. Keys are
// not expanded — env var names and HTTP header names should never
// contain ${PLUGIN_ROOT}, and expanding keys would be a footgun
// (pluginRoot containing characters like "/" would corrupt the map).
func expandStringMap(m map[string]string, pluginRoot string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = expandVars(v, pluginRoot)
	}
	return out
}

// expandJSON walks raw JSON and rewrites every string value that
// contains a recognised token. Used for the hooks JSON blob which
// PP3's hooks.Registry.MergeJSON consumes verbatim — the registry
// has no knowledge of plugin paths, so the substitution must happen
// before we hand the bytes off.
//
// Implementation: decode → walk → re-encode. The walk preserves the
// original structure including arrays of arrays of objects (which
// is the standard hooks shape). Numbers and booleans pass through
// untouched.
func expandJSON(raw []byte, pluginRoot string) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var v any
	if err := jsonUnmarshal(raw, &v); err != nil {
		return nil, err
	}
	walked := walkAndExpand(v, pluginRoot)
	return jsonMarshal(walked)
}

// walkAndExpand is the recursive step for expandJSON. Returns the
// transformed value; non-string types are returned as-is so the
// caller can re-marshal cleanly.
func walkAndExpand(v any, pluginRoot string) any {
	switch t := v.(type) {
	case string:
		return expandVars(t, pluginRoot)
	case []any:
		for i, child := range t {
			t[i] = walkAndExpand(child, pluginRoot)
		}
		return t
	case map[string]any:
		for k, child := range t {
			t[k] = walkAndExpand(child, pluginRoot)
		}
		return t
	default:
		return t
	}
}
