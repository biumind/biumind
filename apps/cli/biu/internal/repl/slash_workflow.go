// /workflow slash + helpers. Multi-step task definitions loaded
// from ~/.biumind/workflows/<name>.md (and the project override).
// Pre-flight checks (declared in frontmatter) gate dispatch; the
// `show` subcommand previews without enforcing.

package repl

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	"github.com/biumind/biumind/apps/cli/biu/internal/workflows"
)

// handleWorkflow dispatches /workflow subcommands. Three forms:
//
//	/workflow                — list every loaded workflow
//	/workflow show <name>    — preview the rendered body + pre-flights
//	/workflow <name> [args]  — verify checks → dispatch as user prompt
//
// Pre-flight checks (declared in frontmatter `requires:`) run BEFORE
// the engine is asked to do anything. A failure short-circuits with
// a clear status message — no half-run workflows.
//
// Dispatch routes through the same startEngineStream path that
// /ultraplan / /review use, with the rendered body as the user
// prompt. The literal `/<name> <args>` is recorded in history so
// scrollback shows the command, not the rendered prompt body.
func (m model) handleWorkflow(parts []string, line string) (tea.Model, tea.Cmd) {
	if len(parts) == 1 {
		m.appendSystemNote(m.renderWorkflowList())
		return m, nil
	}
	if parts[1] == "show" {
		if len(parts) < 3 {
			m.appendSystemNote("/workflow: usage: /workflow show <name>")
			return m, nil
		}
		m.appendSystemNote(m.renderWorkflowShow(parts[2]))
		return m, nil
	}
	if m.engine == nil {
		m.appendSystemNote("/workflow: requires engine path (workflows dispatch through the agent loop)")
		return m, nil
	}
	name := parts[1]
	cwd, _ := os.Getwd()
	reg, err := workflows.Load(cwd)
	if err != nil {
		m.appendSystemNote("/workflow: " + err.Error())
		return m, nil
	}
	w, ok := reg.Lookup(name)
	if !ok {
		m.appendSystemNote("/workflow: no workflow " + name + " (try /workflow to list)")
		return m, nil
	}
	if err := w.Verify(cwd); err != nil {
		m.appendSystemNote(fmt.Sprintf("/workflow %s: pre-flight failed (%v) — fix or remove the check from %s",
			name, err, w.Path))
		return m, nil
	}
	args := strings.TrimSpace(strings.TrimPrefix(line, parts[0]+" "+name))
	rendered := w.Render(args)
	if strings.TrimSpace(rendered) == "" {
		m.appendSystemNote("/workflow " + name + ": body is empty after substitution")
		return m, nil
	}
	display := "/workflow " + name
	if args != "" {
		display += " " + args
	}
	m.history = append(m.history, client.Message{Role: "user", Content: display})
	if m.sessionLog != nil {
		_ = m.sessionLog.Append(session.Event{
			Type: "user_message", Content: display,
		})
	}
	return m.startEngineStream(rendered)
}

// renderWorkflowList prints every loaded workflow with its
// description + source tag. Empty registry → helpful prompt to
// create the first one.
func (m model) renderWorkflowList() string {
	cwd, _ := os.Getwd()
	reg, err := workflows.Load(cwd)
	if err != nil {
		return "/workflow: " + err.Error()
	}
	all := reg.All()
	if len(all) == 0 {
		return "/workflow: no workflows defined. " +
			"Drop a markdown file under ~/.biumind/workflows/ — see /workflow show <name> after creating one."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workflows (%d):\n", len(all))
	for _, w := range all {
		desc := w.Description
		if desc == "" {
			desc = "(no description)"
		}
		req := ""
		if len(w.Requires) > 0 {
			req = " · requires=" + strings.Join(w.Requires, ",")
		}
		fmt.Fprintf(&b, "  /workflow %s [%s]%s — %s\n",
			w.Name, w.Source, req, oneLineSlash(desc))
	}
	b.WriteString("  (preview: /workflow show <name>)")
	return b.String()
}

// renderWorkflowShow prints the workflow's frontmatter summary +
// the rendered body. The body is rendered with `<args>` as the
// $ARGUMENTS placeholder so the user sees a representative
// dispatch shape; pre-flight checks are listed but NOT run during
// preview (the user might be inspecting before fixing prerequisites).
func (m model) renderWorkflowShow(name string) string {
	cwd, _ := os.Getwd()
	reg, err := workflows.Load(cwd)
	if err != nil {
		return "/workflow show: " + err.Error()
	}
	w, ok := reg.Lookup(name)
	if !ok {
		return "/workflow show: no workflow " + name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "workflow: %s [%s]\n", w.Name, w.Source)
	if w.Description != "" {
		fmt.Fprintf(&b, "  description: %s\n", w.Description)
	}
	fmt.Fprintf(&b, "  path:        %s\n", w.Path)
	if len(w.Requires) > 0 {
		fmt.Fprintf(&b, "  requires:    %s\n", strings.Join(w.Requires, ", "))
	}
	if len(w.Args) > 0 {
		fmt.Fprintf(&b, "  args:        ")
		names := make([]string, 0, len(w.Args))
		for _, a := range w.Args {
			names = append(names, a.Name)
		}
		fmt.Fprintf(&b, "%s\n", strings.Join(names, ", "))
	}
	b.WriteString("\n--- body (rendered with $ARGUMENTS=<args>) ---\n\n")
	b.WriteString(w.Render("<args>"))
	return strings.TrimRight(b.String(), "\n")
}
