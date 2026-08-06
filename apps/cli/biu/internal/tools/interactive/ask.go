// AskUserQuestion — synchronous structured prompt to the human.
//
// The model uses this to clarify ambiguous instructions ("which
// library?", "which tone?") without having to guess and risk a wasted
// turn.
//
// Schema:
//
//   * `questions: Question[]` — 1-4 questions in one batch. Each has
//     question + header (chip ≤12 chars) + 2-4 options + multiSelect.
//   * Each option has label + description + optional preview (mockup
//     / code snippet shown side-by-side).
//   * Free-text "Other" path is synthesised by the REPL on every
//     question; users can pick it to type a custom answer.
//   * `annotations` returned per-question carries notes + selected
//     option preview content for downstream auditing.
//
// Wiring: the engine's ToolEnv.AskUser callback is one-question at a
// time; we loop here so the engine plumbing stays simple. The REPL
// (which actually renders) treats every question independently.

package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// AskUserQuestionTool is the engine-facing tool registration.
type AskUserQuestionTool struct{}

func (AskUserQuestionTool) Name() string { return "AskUserQuestion" }

func (AskUserQuestionTool) Description(_ map[string]any) string {
	return "Ask the user one or more multiple-choice questions and " +
		"block until they answer. Pass a `questions` array of 1-4 " +
		"questions, each with 2-4 options. Use this BEFORE acting " +
		"when the user's intent is ambiguous; do NOT use when the " +
		"user has already specified a preference. Each option " +
		"supports an optional `preview` (markdown / code snippet) " +
		"shown side-by-side with the option list."
}

// askChipMax is the chip width cap advertised in the schema; keep it
// in sync with the schema description.
const askChipMax = 12

func (AskUserQuestionTool) InputSchema() map[string]any {
	option := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label": map[string]any{
				"type":        "string",
				"description": "Display text (1-5 words) shown to the user.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Explanation of what choosing this option means or implies.",
			},
			"preview": map[string]any{
				"type":        "string",
				"description": "Optional preview content (mockup / code / config) rendered when this option is focused.",
			},
		},
		"required": []string{"label", "description"},
	}
	question := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The full question to ask the user. End with a question mark.",
			},
			"header": map[string]any{
				"type":        "string",
				"description": fmt.Sprintf("Short chip label (≤%d chars).", askChipMax),
			},
			"options": map[string]any{
				"type":        "array",
				"minItems":    2,
				"maxItems":    4,
				"items":       option,
				"description": "2-4 mutually exclusive options. The REPL automatically adds an 'Other' free-text option.",
			},
			"multiSelect": map[string]any{
				"type":        "boolean",
				"description": "When true, allow multiple selections (e.g. feature toggles). Default: false.",
			},
		},
		"required": []string{"question", "header", "options"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    4,
				"items":       question,
				"description": "1-4 questions to ask the user, in order.",
			},
			"metadata": map[string]any{
				"type":        "object",
				"description": "Optional analytics tracking metadata (not displayed).",
				"properties": map[string]any{
					"source": map[string]any{"type": "string"},
				},
			},
		},
		"required": []string{"questions"},
	}
}

func (AskUserQuestionTool) IsReadOnly(_ map[string]any) bool        { return true }
func (AskUserQuestionTool) IsDestructive(_ map[string]any) bool     { return false }
func (AskUserQuestionTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (AskUserQuestionTool) InterruptBehavior() string               { return "cancel" }

// answeredQuestion captures one question + the user's response.
// Used internally to format the tool result in the
// `"Q1"="A1", "Q2"="B,C user notes: ..."` shape.
type answeredQuestion struct {
	Question        string
	Selected        []string // option labels (in display order)
	Other           string   // text the user typed if they picked "Other"
	Notes           string   // free-text annotation
	SelectedPreview string   // preview content of the chosen option (single-select only)
}

func (AskUserQuestionTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if env == nil || env.AskUser == nil {
		return softErr("AskUserQuestion",
			"no interactive UI available — pick a default and proceed"), nil
	}

	rawQuestions, ok := input["questions"].([]any)
	if !ok {
		// Backwards-compat: the legacy single-question shape (the
		// pre-batch schema biu shipped initially). Convert on the fly
		// so old prompts keep working until callers migrate.
		if legacy, isLegacy := legacyQuestion(input); isLegacy {
			rawQuestions = []any{legacy}
		} else {
			return softErr("AskUserQuestion", "`questions` array required (1-4 entries)"), nil
		}
	}
	if len(rawQuestions) == 0 || len(rawQuestions) > 4 {
		return softErr("AskUserQuestion", "questions array must have 1-4 entries"), nil
	}

	answered := make([]answeredQuestion, 0, len(rawQuestions))
	seen := map[string]bool{} // questions must be unique
	for i, raw := range rawQuestions {
		qm, ok := raw.(map[string]any)
		if !ok {
			return softErr("AskUserQuestion",
				fmt.Sprintf("question[%d] must be an object", i)), nil
		}
		q, err := parseQuestion(qm)
		if err != nil {
			return softErr("AskUserQuestion",
				fmt.Sprintf("question[%d]: %v", i, err)), nil
		}
		if seen[q.Question] {
			return softErr("AskUserQuestion",
				fmt.Sprintf("question[%d]: duplicate question text", i)), nil
		}
		seen[q.Question] = true

		ans, err := env.AskUser(ctx, q)
		if err != nil {
			return softErr("AskUserQuestion", err.Error()), nil
		}
		if ans.Cancelled {
			return softErr("AskUserQuestion", "user cancelled"), nil
		}

		out := answeredQuestion{Question: q.Question, Notes: ans.Notes}
		for _, idx := range ans.Selected {
			if idx < 0 || idx >= len(q.Options) {
				continue
			}
			out.Selected = append(out.Selected, q.Options[idx].Label)
			if !q.MultiSelect && q.Options[idx].Preview != "" {
				out.SelectedPreview = q.Options[idx].Preview
			}
		}
		// "Other" handling: a non-empty Notes paired with no Selected
		// indicates the user typed a free-text answer. Surface it as
		// the answer, not as a side note.
		if len(out.Selected) == 0 && ans.Notes != "" {
			out.Other = ans.Notes
			out.Notes = ""
		}
		if len(out.Selected) == 0 && out.Other == "" && out.Notes == "" {
			return softErr("AskUserQuestion", "user dismissed without picking"), nil
		}
		answered = append(answered, out)
	}

	return text(formatAnswerResult(answered)), nil
}

// parseQuestion extracts a UserQuestion from one element of the
// `questions` array. Returns an error message suitable for the soft
// error the tool emits.
func parseQuestion(qm map[string]any) (engine.UserQuestion, error) {
	question, _ := qm["question"].(string)
	if strings.TrimSpace(question) == "" {
		return engine.UserQuestion{}, fmt.Errorf("question text is required")
	}
	header, _ := qm["header"].(string)
	if len(header) > askChipMax {
		header = header[:askChipMax]
	}
	multi, _ := qm["multiSelect"].(bool)

	rawOpts, _ := qm["options"].([]any)
	if len(rawOpts) < 2 {
		return engine.UserQuestion{}, fmt.Errorf("need at least 2 options")
	}
	if len(rawOpts) > 4 {
		return engine.UserQuestion{}, fmt.Errorf("at most 4 options allowed")
	}
	options := make([]engine.UserOption, 0, len(rawOpts))
	seenLabels := map[string]bool{}
	for _, raw := range rawOpts {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		label, _ := m["label"].(string)
		if label == "" {
			continue
		}
		if seenLabels[label] {
			return engine.UserQuestion{}, fmt.Errorf("duplicate option label %q", label)
		}
		seenLabels[label] = true
		desc, _ := m["description"].(string)
		preview, _ := m["preview"].(string)
		options = append(options, engine.UserOption{
			Label: label, Description: desc, Preview: preview,
		})
	}
	if len(options) < 2 {
		return engine.UserQuestion{}, fmt.Errorf("need at least 2 valid options")
	}
	return engine.UserQuestion{
		Question: question, Header: header,
		Options: options, MultiSelect: multi,
	}, nil
}

// legacyQuestion converts the old singular-question shape (question /
// options at the top level) into the new questions[] form so models
// trained on the previous schema keep working.
func legacyQuestion(input map[string]any) (map[string]any, bool) {
	if _, ok := input["question"].(string); !ok {
		return nil, false
	}
	if _, ok := input["options"].([]any); !ok {
		return nil, false
	}
	return map[string]any{
		"question":    input["question"],
		"header":      input["header"],
		"options":     input["options"],
		"multiSelect": input["multiSelect"],
	}, true
}

// formatAnswerResult renders the tool_result content:
//
//	User has answered your questions: "Q1"="A1", "Q2"="B" user notes: ...
//	You can now continue with the user's answers in mind.
//
// Multi-select answers are joined with ", ". Free-text "Other"
// answers come through unquoted. Preview content (when the user
// selected an option that had a preview) is appended on a new line
// so the model can reason about the visual choice it made.
func formatAnswerResult(rows []answeredQuestion) string {
	var b strings.Builder
	b.WriteString("User has answered your questions:\n\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "- %q = ", r.Question)
		switch {
		case r.Other != "":
			fmt.Fprintf(&b, "%q (free text)", r.Other)
		case len(r.Selected) > 0:
			quoted := make([]string, len(r.Selected))
			for i, s := range r.Selected {
				quoted[i] = fmt.Sprintf("%q", s)
			}
			b.WriteString(strings.Join(quoted, ", "))
		default:
			b.WriteString("(no selection)")
		}
		if r.Notes != "" {
			fmt.Fprintf(&b, " — notes: %s", r.Notes)
		}
		b.WriteString("\n")
		if r.SelectedPreview != "" {
			b.WriteString("  preview of selected option:\n")
			for _, line := range strings.Split(r.SelectedPreview, "\n") {
				b.WriteString("    ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\nContinue with these answers in mind.")
	return b.String()
}
