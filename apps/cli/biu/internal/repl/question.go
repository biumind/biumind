// AskUserQuestion REPL panel — multi-select, free-text "Other", and
// preview side rendering, collapsed to a TUI.
//
// State machine (lives on `model`):
//
//   questionAsk == nil          panel hidden
//   questionAsk != nil          showing one question
//     questionTyping == false     navigating options (↑/↓, space toggles in multi)
//     questionTyping == true      textarea active for "Other" or "n"otes
//
// Keybindings (panel active):
//
//   ↑/↓        move cursor (incl. into the synthesised "Other" row)
//   space      multi-select: toggle current; non-multi: no-op
//   enter      submit; non-multi without selection picks current
//   o          jump to "Other" row + start typing free-text answer
//   n          start typing free-text notes (annotation) for the
//              current selection
//   esc / q    cancel — tool soft-errors
//   ctrl+c     also cancel (matches the global abort idiom)
//
// "Other" is a virtual row appended after the model's options. When
// the user picks it we switch to typing mode; the typed text becomes
// UserAnswer.Notes, with Selected empty so the tool path treats it
// as a free-text answer (formatAnswerResult prints "(free text)").

package repl

import (
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	tea "github.com/charmbracelet/bubbletea"
)

// otherIndex returns the cursor index that corresponds to the
// synthesised "Other" row — always the slot just past the last real
// option.
func (m model) otherIndex() int {
	if m.questionAsk == nil {
		return -1
	}
	return len(m.questionAsk.Question.Options)
}

// handleQuestionKey is the panel's keyboard router. Returns the
// updated model + any tea.Cmd.
func (m model) handleQuestionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.questionTyping {
		return m.handleQuestionTyping(msg)
	}

	q := m.questionAsk.Question
	maxIdx := len(q.Options) // "Other" lives at len(Options)
	switch msg.String() {
	case "up", "ctrl+p":
		if m.questionCursor > 0 {
			m.questionCursor--
		}
		m.refreshBody()
		return m, nil
	case "down", "ctrl+n":
		if m.questionCursor < maxIdx {
			m.questionCursor++
		}
		m.refreshBody()
		return m, nil
	case " ", "space":
		// Space toggles the current option in multi-select. No-op for
		// single-select (single-select uses Enter only).
		if !q.MultiSelect {
			return m, nil
		}
		if m.questionCursor == m.otherIndex() {
			// Toggling "Other" enters typing immediately so users can
			// add the free-text alongside the regular picks.
			m.questionTyping = true
			m.questionTypeFor = 0
			m.textarea.Reset()
			m.textarea.Placeholder = "Type your custom answer, Enter to submit, Esc to cancel"
			m.textarea.Focus()
			m.refreshBody()
			return m, nil
		}
		if m.questionPicked == nil {
			m.questionPicked = map[int]bool{}
		}
		m.questionPicked[m.questionCursor] = !m.questionPicked[m.questionCursor]
		m.refreshBody()
		return m, nil
	case "o":
		// Jump straight to "Other" + start typing — power-user shortcut
		// when none of the canned options fit.
		m.questionCursor = m.otherIndex()
		m.questionTyping = true
		m.questionTypeFor = 0
		m.textarea.Reset()
		m.textarea.Placeholder = "Type your custom answer, Enter to submit, Esc to cancel"
		m.textarea.Focus()
		m.refreshBody()
		return m, nil
	case "n":
		// Annotate current selection. Notes get attached to the
		// answer regardless of which option(s) are picked.
		m.questionTyping = true
		m.questionTypeFor = 1
		m.textarea.Reset()
		m.textarea.Placeholder = "Type free-text notes for this answer; Enter submits, Esc cancels"
		m.textarea.Focus()
		m.refreshBody()
		return m, nil
	case "enter":
		return m.submitQuestion()
	case "esc", "q", "ctrl+c":
		m.cancelQuestion()
		return m, nil
	}
	return m, nil
}

// handleQuestionTyping captures keystrokes while the textarea is
// open. Enter commits the typed text; Esc aborts back to navigation.
func (m model) handleQuestionTyping(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.questionTyping = false
		m.textarea.Reset()
		m.textarea.Placeholder = ""
		m.refreshBody()
		return m, nil
	case "enter":
		// Don't insert a newline — Enter commits. Use Alt+Enter for
		// multi-line if the user really needs it (textarea's default).
		text := m.textarea.Value()
		m.questionTyping = false
		m.textarea.Reset()
		m.textarea.Placeholder = ""
		switch m.questionTypeFor {
		case 0:
			// "Other" answer — replaces selection entirely.
			m.questionAsk.Decision <- engine.UserAnswer{
				Selected: nil,
				Notes:    text,
			}
			m.clearQuestion()
		case 1:
			// Notes annotation — keep current selection, send notes.
			selected := m.collectSelectedIndices()
			m.questionAsk.Decision <- engine.UserAnswer{
				Selected: selected,
				Notes:    text,
			}
			m.clearQuestion()
		}
		m.refreshBody()
		return m, nil
	}
	// Forward to the textarea so editing keys keep working.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// submitQuestion sends the current selection back to the engine.
// Single-select with no toggle defaults to picking the focused row.
func (m model) submitQuestion() (tea.Model, tea.Cmd) {
	q := m.questionAsk.Question
	if m.questionCursor == m.otherIndex() {
		// Pressing Enter on "Other" without typing → start typing.
		m.questionTyping = true
		m.questionTypeFor = 0
		m.textarea.Reset()
		m.textarea.Placeholder = "Type your custom answer, Enter to submit, Esc to cancel"
		m.textarea.Focus()
		m.refreshBody()
		return m, nil
	}
	selected := m.collectSelectedIndices()
	if len(selected) == 0 {
		if q.MultiSelect {
			// Empty submit on multi-select is treated as "no answer" —
			// soft-error the tool so the model knows to pick a default.
			m.cancelQuestion()
			return m, nil
		}
		// Single-select default: pick the currently-focused option.
		selected = []int{m.questionCursor}
	}
	m.questionAsk.Decision <- engine.UserAnswer{
		Selected: selected,
	}
	m.clearQuestion()
	m.refreshBody()
	return m, nil
}

// collectSelectedIndices returns the toggled set in display order.
// Empty when nothing has been actively picked (caller decides what
// that means by mode).
func (m model) collectSelectedIndices() []int {
	if !m.questionAsk.Question.MultiSelect {
		// Single-select: questionPicked is unused; the cursor is the
		// pick (collected by the caller's submit path).
		return nil
	}
	out := make([]int, 0, len(m.questionPicked))
	for i := 0; i < len(m.questionAsk.Question.Options); i++ {
		if m.questionPicked[i] {
			out = append(out, i)
		}
	}
	return out
}

// cancelQuestion sends a Cancelled answer (tool soft-errors).
func (m *model) cancelQuestion() {
	m.questionAsk.Decision <- engine.UserAnswer{Cancelled: true}
	m.clearQuestion()
	m.refreshBody()
}

// clearQuestion resets the panel state after a submit/cancel so the
// next AskUserQuestion call starts clean.
func (m *model) clearQuestion() {
	m.questionAsk = nil
	m.questionCursor = 0
	m.questionPicked = nil
	m.questionOther = false
	m.questionTyping = false
	m.questionTypeFor = 0
}
