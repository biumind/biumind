// ToolSearchTool — keyword + select search over the deferred-tool
// catalog (P20.51).
//
// Big picture (see internal/engine/deferred.go for the gating rules):
// when an MCP server ships dozens of tools, biu can mark them
// "deferred" so their full JSONSchema doesn't bloat every system
// prompt. The model still sees their *names* in an
// <available-deferred-tools> attachment and can ask this tool to
// surface the schema for the small subset it actually wants to call.
//
// Phase 1 (this file) ships the tool itself: search algorithm,
// select:/keyword/+required forms, scoring. The catalog-filtering
// integration in engine/turn.go is Phase 2 — until then this tool
// happily runs against an empty deferred set and reports zero
// matches, which is the right behaviour: nothing to surface, no harm.

package orchestration

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// ToolSearchToolName is the canonical tool name. Pulled out so other
// packages can reference it without circular imports.
const ToolSearchToolName = "ToolSearch"

// ToolSearchTool surfaces deferred-tool schemas on demand.
//
// Registry is the same registry the engine dispatches against — we
// scan it for tools implementing engine.Deferrable. The tool stays
// useful even when the registry has zero deferred tools (search
// returns "no matches" gracefully).
type ToolSearchTool struct {
	Registry engine.ToolRegistry
}

func (ToolSearchTool) Name() string { return ToolSearchToolName }

func (ToolSearchTool) Description(_ map[string]any) string {
	return "Fetches full schema definitions for deferred tools so they can be called. " +
		"Until fetched, only the name is known — there is no parameter schema, so the " +
		"tool cannot be invoked. This tool takes a query, matches it against the deferred " +
		"tool list, and returns the matched tools' descriptions and JSONSchema. Once a " +
		"tool's schema appears in the result, you can invoke it normally on the next turn.\n\n" +
		"Query forms:\n" +
		"  - \"select:Read,Edit,Grep\" — fetch these exact tools by name.\n" +
		"  - \"notebook jupyter\"     — keyword search, up to max_results best matches.\n" +
		"  - \"+slack send\"          — require \"slack\" in the name, rank by remaining terms."
}

func (ToolSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type": "string",
				"description": "Query to find deferred tools. Use \"select:<tool_name>\" " +
					"for direct selection (comma-separated for multiple), or keywords " +
					"to search. Prefix a term with + to require it.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of keyword-search results to return (default 5).",
			},
		},
		"required": []string{"query"},
	}
}

func (ToolSearchTool) IsReadOnly(_ map[string]any) bool        { return true }
func (ToolSearchTool) IsDestructive(_ map[string]any) bool     { return false }
func (ToolSearchTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (ToolSearchTool) InterruptBehavior() string               { return "cancel" }

func (s ToolSearchTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if s.Registry == nil {
		return softErr(ToolSearchToolName, "no tool registry available"), nil
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return softErr(ToolSearchToolName, "query is required"), nil
	}
	maxResults := 5
	if n, ok := input["max_results"].(float64); ok && n > 0 {
		maxResults = int(n)
	}
	if n, ok := input["max_results"].(int); ok && n > 0 {
		maxResults = n
	}

	var sel *engine.DeferredSelection
	if env != nil {
		sel = env.Selections
	}

	_, deferred := engine.PartitionDeferred(s.Registry)
	if len(deferred) == 0 {
		return plainResult(fmt.Sprintf(
			"No deferred tools available — this server has no tools that need on-demand loading. "+
				"Query: %q.", query)), nil
	}

	// select: prefix — exact-name lookup, comma-separated.
	if rest, ok := stripSelectPrefix(query); ok {
		return selectTools(s.Registry, deferred, rest, sel), nil
	}

	// Keyword scoring path.
	matches := keywordSearch(deferred, query, maxResults)
	if len(matches) == 0 {
		return plainResult(fmt.Sprintf(
			"No matching deferred tools for %q (searched %d deferred tools).",
			query, len(deferred))), nil
	}
	// Record matches as selected so their schemas land in the next
	// turn's wire-level tool catalog.
	for _, t := range matches {
		sel.Add(t.Name())
	}
	return plainResult(renderToolMatches(matches, query, len(deferred))), nil
}

// stripSelectPrefix recognises `select:foo,bar`. Case-insensitive.
func stripSelectPrefix(q string) (string, bool) {
	if len(q) < 7 {
		return "", false
	}
	if !strings.EqualFold(q[:7], "select:") {
		return "", false
	}
	return strings.TrimSpace(q[7:]), true
}

// selectTools pulls exact-name matches out of the registry. Tools the
// caller named that aren't deferred are still resolvable from the
// full registry (selecting an already-loaded tool is a no-op rather
// than an error — it lets the model proceed without retry churn).
//
// `sel` is the deferred-selection set; matched deferred tool names
// land in it so the engine's per-turn buildToolSpecs unlocks their
// schemas on the next provider request. nil sel = no persistence
// (test-only path; production engines always provide one).
func selectTools(reg engine.ToolRegistry, deferred []engine.Tool, names string, sel *engine.DeferredSelection) *engine.ToolResultPayload {
	requested := splitCSV(names)
	if len(requested) == 0 {
		return softErr(ToolSearchToolName, "select: requires at least one name")
	}
	deferredByName := indexByName(deferred)

	var found, missing []engine.Tool
	var missingNames []string
	for _, name := range requested {
		if t, ok := deferredByName[name]; ok {
			found = append(found, t)
			sel.Add(name)
			continue
		}
		// Fall through to the full registry for "already loaded" tools.
		if t, ok := reg.Get(name); ok {
			missing = append(missing, t) // technically present but non-deferred
			continue
		}
		missingNames = append(missingNames, name)
	}

	if len(found) == 0 && len(missing) == 0 {
		return plainResult(fmt.Sprintf(
			"No tools matched select: %s. Try keyword search instead.",
			strings.Join(missingNames, ", ")))
	}

	var b strings.Builder
	if len(found) > 0 {
		b.WriteString(renderToolMatches(found, "select:"+names, len(deferred)))
	}
	if len(missing) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		mNames := make([]string, 0, len(missing))
		for _, t := range missing {
			mNames = append(mNames, t.Name())
		}
		fmt.Fprintf(&b, "Already-loaded tools (no deferral, schema already in your "+
			"prompt): %s.", strings.Join(mNames, ", "))
	}
	if len(missingNames) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Not found: %s.", strings.Join(missingNames, ", "))
	}
	return plainResult(b.String())
}

// keywordSearch scores deferred tools by name + description match.
//
// Scoring weights are tuned to match the values models are commonly
// trained against — diverging would mean effectively retraining the
// prompt. MCP tools get a small boost on name-part matches because
// their structured names (mcp__server__action) carry strong signal.
func keywordSearch(deferred []engine.Tool, query string, maxResults int) []engine.Tool {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	if queryLower == "" {
		return nil
	}

	// Fast path: exact name match wins regardless of scoring noise.
	for _, t := range deferred {
		if strings.EqualFold(t.Name(), queryLower) {
			return []engine.Tool{t}
		}
	}

	// MCP prefix shortcut: `mcp__github` returns every github-namespaced tool.
	if strings.HasPrefix(queryLower, "mcp__") && len(queryLower) > 5 {
		var hits []engine.Tool
		for _, t := range deferred {
			if strings.HasPrefix(strings.ToLower(t.Name()), queryLower) {
				hits = append(hits, t)
			}
		}
		if len(hits) > 0 {
			if len(hits) > maxResults {
				hits = hits[:maxResults]
			}
			return hits
		}
	}

	terms := strings.Fields(queryLower)
	required := make([]string, 0, len(terms))
	scoring := make([]string, 0, len(terms))
	for _, t := range terms {
		if strings.HasPrefix(t, "+") && len(t) > 1 {
			required = append(required, t[1:])
		} else {
			scoring = append(scoring, t)
		}
	}
	allTerms := append(append([]string{}, required...), scoring...)
	if len(allTerms) == 0 {
		return nil
	}
	patterns := compileTermPatterns(allTerms)

	type scored struct {
		tool  engine.Tool
		score int
	}
	var ranked []scored

	for _, t := range deferred {
		parsed := parseToolName(t.Name())
		desc := strings.ToLower(t.Description(nil))

		// Filter on required terms.
		ok := true
		for _, term := range required {
			pat := patterns[term]
			matched := stringInParts(parsed.parts, term) ||
				partsContain(parsed.parts, term) ||
				pat.MatchString(desc)
			if !matched {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}

		// Score across all terms.
		score := 0
		for _, term := range allTerms {
			pat := patterns[term]
			switch {
			case stringInParts(parsed.parts, term):
				if parsed.isMcp {
					score += 12
				} else {
					score += 10
				}
			case partsContain(parsed.parts, term):
				if parsed.isMcp {
					score += 6
				} else {
					score += 5
				}
			case strings.Contains(parsed.full, term) && score == 0:
				score += 3
			}
			if pat.MatchString(desc) {
				score += 2
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{t, score})
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].tool.Name() < ranked[j].tool.Name()
	})

	if len(ranked) > maxResults {
		ranked = ranked[:maxResults]
	}
	out := make([]engine.Tool, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.tool)
	}
	return out
}

// renderToolMatches builds the text block returned to the LLM. Each
// matched tool gets a short header + its description + a JSON-Schema
// dump so the next turn can call it without another search round-trip.
func renderToolMatches(tools []engine.Tool, query string, totalDeferred int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d deferred tool(s) matching %q (of %d total deferred):\n",
		len(tools), query, totalDeferred)
	for _, t := range tools {
		fmt.Fprintf(&b, "\n## %s\n%s\n", t.Name(), strings.TrimSpace(t.Description(nil)))
		fmt.Fprintf(&b, "\nInput schema:\n%s\n", formatSchema(t.InputSchema()))
	}
	return b.String()
}

// formatSchema dumps the JSONSchema map as a compact-ish indented
// JSON-like text. Avoid encoding/json so we don't pull in another
// dependency; the model reads either format fine.
func formatSchema(schema map[string]any) string {
	if schema == nil {
		return "(no schema)"
	}
	return prettyJSON(schema, 0)
}

func prettyJSON(v any, depth int) string {
	indent := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for i, k := range keys {
			fmt.Fprintf(&b, "%s  %q: %s", indent, k, prettyJSON(t[k], depth+1))
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s}", indent)
		return b.String()
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var parts []string
		for _, el := range t {
			parts = append(parts, prettyJSON(el, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		quoted := make([]string, len(t))
		for i, s := range t {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	case string:
		return fmt.Sprintf("%q", t)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ─── helpers ───────────────────────────────────────────────────────

type parsedName struct {
	parts []string
	full  string
	isMcp bool
}

func parseToolName(name string) parsedName {
	if strings.HasPrefix(name, "mcp__") {
		stripped := strings.ToLower(strings.TrimPrefix(name, "mcp__"))
		var parts []string
		for _, seg := range strings.Split(stripped, "__") {
			for _, p := range strings.Split(seg, "_") {
				if p != "" {
					parts = append(parts, p)
				}
			}
		}
		full := strings.ReplaceAll(strings.ReplaceAll(stripped, "__", " "), "_", " ")
		return parsedName{parts, full, true}
	}
	// CamelCase + underscore split.
	camelSplit := camelToSpaces(name)
	lower := strings.ToLower(strings.ReplaceAll(camelSplit, "_", " "))
	parts := strings.Fields(lower)
	return parsedName{parts, strings.Join(parts, " "), false}
}

func camelToSpaces(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(s[i-1])
			if prev >= 'a' && prev <= 'z' {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func compileTermPatterns(terms []string) map[string]*regexp.Regexp {
	out := make(map[string]*regexp.Regexp, len(terms))
	for _, t := range terms {
		if _, ok := out[t]; ok {
			continue
		}
		out[t] = regexp.MustCompile(`\b` + regexp.QuoteMeta(t) + `\b`)
	}
	return out
}

func stringInParts(parts []string, term string) bool {
	for _, p := range parts {
		if p == term {
			return true
		}
	}
	return false
}

func partsContain(parts []string, term string) bool {
	for _, p := range parts {
		if p != term && strings.Contains(p, term) {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func indexByName(tools []engine.Tool) map[string]engine.Tool {
	out := make(map[string]engine.Tool, len(tools))
	for _, t := range tools {
		out[t.Name()] = t
	}
	return out
}

func plainResult(text string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: text}},
	}
}
