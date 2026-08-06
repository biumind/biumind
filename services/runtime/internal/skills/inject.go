package skills

import (
	"strings"
)

// Injection — assembled prompt fragments ready to drop into the
// turn's request:
//
//	SystemPrompt   — block to append to the system message. Includes
//	                 every Pinned + AutoAttach body, plus the
//	                 <available_skills> name+description list. Empty
//	                 when there's nothing to inject.
//	SelectedBlock  — block to append to the LAST USER MESSAGE for the
//	                 skills the *user* explicitly @-mentioned this
//	                 turn. Wrapped in <selected_skill_context> so the
//	                 model knows the body is loaded and skill.activate
//	                 must NOT be called for them. Empty when no
//	                 selected skills carry content.
type Injection struct {
	SystemPrompt  string
	SelectedBlock string
}

// DefaultPromptCharBudget caps the assembled BuildSystemPrompt at
// ~8K characters (≈2K tokens for English / mixed CJK). Order of
// magnitude: a 200K-context model has plenty of room, but giving the
// skill prompt a hard ceiling means a user who pinned 50 chunky
// skills can't accidentally exhaust the model's whole prompt cache.
//
// Char count rather than tokenisation is intentional — token counts
// are model-specific and adding a tokenizer dependency to a SQL-
// adjacent package is overkill. Charcount overestimates English
// token cost (good — fail safe) and slightly underestimates CJK
// (still fine because CJK skill bodies are rare in v1).
const DefaultPromptCharBudget = 8 * 1024

// BuildSystemPrompt assembles the system-prompt block from a
// LoadedSkills set. Pinned + AutoAttach inject body inline; the
// remaining Available are listed by (name, description) only so the
// LLM can call skill.activate when it judges a match. Output is
// idempotent across calls with the same input — same byte-for-byte
// string, important for prompt-cache stability.
//
// Token-budget rule (P1-#11): assembly stays under
// DefaultPromptCharBudget by demoting overflow tiers in this order:
//
//  1. Pinned bodies are sacred — emit all (they were explicit user
//     choice; truncating mid-skill would silently break behaviour).
//  2. AutoAttach bodies are emitted while the running budget allows
//     each full body. Once full, remaining AutoAttach demote to the
//     <available_skills> name+description list — still discoverable
//     via skill.activate, just not pre-loaded.
//  3. Available list always emits (1 line per skill) even when the
//     budget is blown by Pinned alone — discovery has to keep working
//     so the model knows it has a fallback.
//
// In the worst case (all pinned) the prompt may exceed the budget.
// That's by design — pinning IS the user opting into "pay this cost
// for this skill, every turn".
func BuildSystemPrompt(loaded *LoadedSkills) string {
	return BuildSystemPromptWithBudget(loaded, DefaultPromptCharBudget)
}

// BuildSystemPromptWithBudget exposes the budget knob for callers
// (and tests) that want a different ceiling. budget <= 0 disables
// the cap entirely (legacy behaviour).
func BuildSystemPromptWithBudget(loaded *LoadedSkills, budget int) string {
	if loaded == nil {
		return ""
	}
	var b strings.Builder
	// Track demoted AutoAttach entries so the Available section
	// surfaces them as discoverable hints.
	var demoted []*Skill

	if len(loaded.Pinned) > 0 {
		b.WriteString("# Pinned skills\n\n")
		b.WriteString("These skills are always loaded for this agent. " +
			"Their full instructions are below — do NOT call skill.activate " +
			"for them, follow the instructions directly.\n")
		for _, s := range loaded.Pinned {
			writeSkillBlock(&b, s)
		}
	}

	if len(loaded.AutoAttach) > 0 {
		header := "\n# Auto-attached skills\n\n" +
			"These skill instructions are pre-loaded because " +
			"the project's working directory matched their `paths:` " +
			"frontmatter. Same rule as pinned: do NOT call skill.activate.\n"
		headerWritten := false
		for _, s := range loaded.AutoAttach {
			if s == nil {
				continue
			}
			body := skillBlockBytes(s)
			// Demote when the would-be size exceeds budget AND we've
			// already emitted at least the pinned section. Budget=0
			// (or negative) disables demotion → legacy behaviour.
			if budget > 0 && b.Len()+len(header)+len(body) > budget {
				demoted = append(demoted, s)
				continue
			}
			if !headerWritten {
				b.WriteString(header)
				headerWritten = true
			}
			b.WriteString(body)
		}
	}

	// Available — pinned list (line per skill) PLUS any demoted
	// AutoAttach entries promoted into discoverability so the model
	// can still find them via skill.activate.
	avail := append([]*Skill(nil), loaded.Available...)
	avail = append(avail, demoted...)
	if len(avail) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("# Available skills\n\n")
		b.WriteString("These skills are enabled for this agent but " +
			"not pre-loaded. When the user's task clearly matches one, " +
			"call `skill.activate(name=...)` to load its instructions.\n\n")
		b.WriteString("<available_skills>\n")
		for _, s := range avail {
			if s == nil {
				continue
			}
			b.WriteString("  <skill name=\"")
			b.WriteString(escapeXML(s.Name))
			b.WriteString("\" identifier=\"")
			b.WriteString(escapeXML(s.Identifier))
			b.WriteString("\">")
			b.WriteString(escapeXML(s.Description))
			b.WriteString("</skill>\n")
		}
		b.WriteString("</available_skills>\n")
	}
	return b.String()
}

// skillBlockBytes mirrors writeSkillBlock but returns the assembled
// bytes so the budget code can decide whether to commit before
// writing into the live builder. Allocation per call is acceptable —
// LoadedSkills is bounded by registry catalogue size.
func skillBlockBytes(s *Skill) string {
	var b strings.Builder
	writeSkillBlock(&b, s)
	return b.String()
}

// BuildSelectedBlock wraps user-@mentioned skills in the
// <selected_skill_context> block per Skills-Design §8.1. Pass the
// resolved skill rows (Registry.GetByIdentifier in a loop is fine)
// in the order the user mentioned them. Skills lacking content
// (e.g. resource-only refs) are emitted as empty <skill /> stubs
// for traceability without polluting the prompt.
//
// Dedup: when the caller passes duplicate skill rows (same ID — e.g.
// pinned + explicit selection of the same skill, or auto-attach +
// @-mention overlap), we emit each skill at most once. First
// occurrence wins so the user-visible order matches the call order.
// Without this guard the same body would appear twice in the prompt,
// burning tokens and confusing the model into thinking the skill
// matters more than it does.
func BuildSelectedBlock(selected []*Skill) string {
	if len(selected) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(selected))
	var b strings.Builder
	b.WriteString("<selected_skill_context>\n")
	b.WriteString("The user explicitly selected these skills for this " +
		"request. Their full instructions are already loaded below — " +
		"do NOT call skill.activate for them, use the provided content " +
		"directly.\n")
	b.WriteString("<selected_skills>\n")
	any := false
	for _, s := range selected {
		if s == nil {
			continue
		}
		if s.ID != "" {
			if _, dup := seen[s.ID]; dup {
				continue
			}
			seen[s.ID] = struct{}{}
		}
		if strings.TrimSpace(s.Content) == "" {
			b.WriteString("  <skill identifier=\"")
			b.WriteString(escapeXML(s.Identifier))
			b.WriteString("\" name=\"")
			b.WriteString(escapeXML(s.Name))
			b.WriteString("\" />\n")
			continue
		}
		any = true
		b.WriteString("  <skill identifier=\"")
		b.WriteString(escapeXML(s.Identifier))
		b.WriteString("\" name=\"")
		b.WriteString(escapeXML(s.Name))
		b.WriteString("\">\n")
		b.WriteString(s.Content)
		if !strings.HasSuffix(s.Content, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</selected_skills>\n")
	b.WriteString("</selected_skill_context>")
	if !any {
		// Every selected skill was content-less; emit an empty hint
		// rather than a noisy block so the prompt stays tidy.
		return ""
	}
	return b.String()
}

// writeSkillBlock formats one skill for the inline (Pinned /
// AutoAttach) section. Header line carries name + description so a
// reader scanning the system prompt understands which body is which
// without having to match identifiers across multiple sections.
func writeSkillBlock(b *strings.Builder, s *Skill) {
	if s == nil {
		return
	}
	b.WriteString("\n## ")
	b.WriteString(s.Name)
	if s.Description != "" {
		b.WriteString(" — ")
		b.WriteString(s.Description)
	}
	b.WriteByte('\n')
	body := strings.TrimSpace(s.Content)
	if body != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}
}

// escapeXML escapes the four characters that matter inside our XML-
// flavoured wrapping tags. We don't generate well-formed XML
// (whitespace + body content can be anything markdown-shaped), but
// attribute values still need the basics.
func escapeXML(s string) string {
	r := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}
