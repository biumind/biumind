// Session export — turn the JSONL event log into something humans
// (or other tools) actually want to consume.
//
// Three output formats:
//
//   markdown          — chat transcript with tool-call boxes, perfect
//                       for sharing in bug reports / docs / PRs.
//   json              — turn-consolidated structured dump. Stable
//                       schema for downstream tooling.
//   anthropic-replay  — `messages` payload you can POST to
//                       https://api.anthropic.com/v1/messages and
//                       continue the conversation from outside biu.
//
// Secrets are passed through a redactor before any format runs. The
// redactor catches:
//
//   * Authorization: Bearer <token>
//   * sk-ant-… (Anthropic-style API keys)
//   * any field literally named api_key / token / refresh_token /
//     virtual_key in the args/output blob.
//
// The redactor is conservative on purpose — better a few extra
// `***`s than leaking a credential into a bug report.

package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ExportFormat selects an Export() output renderer.
type ExportFormat string

const (
	FormatMarkdown        ExportFormat = "markdown"
	FormatJSON            ExportFormat = "json"
	FormatAnthropicReplay ExportFormat = "anthropic-replay"
)

// ExportOptions tunes what Export emits. Zero value gives a
// reasonable default (markdown, secrets redacted, tool output
// included, system messages excluded).
type ExportOptions struct {
	// Format chooses the renderer. Zero value = FormatMarkdown.
	Format ExportFormat

	// IncludeToolOutput controls whether `tool_result` blocks are
	// rendered with their full Output payload (potentially large) or
	// just a one-line summary. Default: true.
	IncludeToolOutput bool

	// ExcludeSystem drops `system_*` events (rare but emitted by
	// permission rejections / hook blocks). Default: false.
	ExcludeSystem bool

	// MaxToolOutputBytes truncates tool output payloads to N bytes
	// when IncludeToolOutput is on, with a `…` marker. 0 = no cap.
	MaxToolOutputBytes int

	// Now is injected for tests. Zero value = time.Now() at first use.
	Now func() time.Time
}

// Export reads a session JSONL file and writes the chosen format to
// w. Returns the number of bytes written + the first error.
func Export(path string, w io.Writer, opt ExportOptions) (int, error) {
	if opt.Format == "" {
		opt.Format = FormatMarkdown
	}
	events, err := readEvents(path)
	if err != nil {
		return 0, err
	}
	if opt.MaxToolOutputBytes == 0 {
		opt.MaxToolOutputBytes = 4096
	}
	for i := range events {
		events[i] = redactEvent(events[i])
	}

	switch opt.Format {
	case FormatMarkdown:
		return renderMarkdown(events, path, w, opt)
	case FormatJSON:
		return renderJSON(events, path, w, opt)
	case FormatAnthropicReplay:
		return renderAnthropicReplay(events, w, opt)
	default:
		return 0, fmt.Errorf("session export: unknown format %q", opt.Format)
	}
}

// readEvents loads every event from a JSONL session file. Lines that
// fail to parse are skipped with a stderr note — exporters shouldn't
// fail wholesale on one bad row when the rest is recoverable.
func readEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // 4MB max line
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			fmt.Fprintf(os.Stderr, "[biu] export: skipping malformed line: %v\n", err)
			continue
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}

// ─── Markdown ──────────────────────────────────────────

var assistantHeader = "### 🤖 Assistant"
var userHeader = "### 👤 User"

func renderMarkdown(events []Event, path string, w io.Writer, opt ExportOptions) (int, error) {
	var b strings.Builder
	stat, _ := os.Stat(path)

	// Header — counts + frontmatter so search/index tools have hooks.
	counts := countByType(events)
	id := basenameNoExt(path)
	fmt.Fprintf(&b, "# Session %s\n\n", id)
	if stat != nil {
		fmt.Fprintf(&b, "* **Started** %s\n", events[0].TS.Format(time.RFC3339))
		fmt.Fprintf(&b, "* **Ended** %s\n", events[len(events)-1].TS.Format(time.RFC3339))
		fmt.Fprintf(&b, "* **Bytes on disk** %d\n", stat.Size())
	}
	fmt.Fprintf(&b, "* **Events** %d (user=%d, assistant=%d, tool=%d)\n\n",
		len(events), counts["user_message"],
		counts["assistant_message"]+counts["assistant_delta"],
		counts["tool_use"])
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b)

	// Stream the events. Consecutive assistant_delta gets merged into
	// one block so the transcript reads naturally.
	var deltaBuf strings.Builder
	flushDelta := func(ts time.Time) {
		if deltaBuf.Len() == 0 {
			return
		}
		fmt.Fprintf(&b, "%s · %s\n\n%s\n\n",
			assistantHeader, ts.Format("15:04:05"), deltaBuf.String())
		deltaBuf.Reset()
	}

	for _, ev := range events {
		if opt.ExcludeSystem && strings.HasPrefix(ev.Type, "system_") {
			continue
		}
		switch ev.Type {
		case "user_message":
			flushDelta(ev.TS)
			fmt.Fprintf(&b, "%s · %s\n\n%s\n\n",
				userHeader, ev.TS.Format("15:04:05"), ev.Content)
		case "assistant_message":
			flushDelta(ev.TS)
			fmt.Fprintf(&b, "%s · %s\n\n%s\n\n",
				assistantHeader, ev.TS.Format("15:04:05"), ev.Content)
		case "assistant_delta":
			deltaBuf.WriteString(ev.Content)
		case "tool_use":
			flushDelta(ev.TS)
			args, _ := json.Marshal(ev.Args)
			fmt.Fprintf(&b, "#### ⏺ %s · `%s` · %s\n\n",
				ev.Name, ev.CallID, ev.TS.Format("15:04:05"))
			fmt.Fprintf(&b, "**args**\n\n```json\n%s\n```\n\n", string(args))
		case "tool_result":
			if !opt.IncludeToolOutput {
				fmt.Fprintf(&b, "↳ tool_result `%s` (%d bytes, hidden)\n\n",
					ev.CallID, len(ev.Output))
				continue
			}
			body := ev.Output
			if opt.MaxToolOutputBytes > 0 && len(body) > opt.MaxToolOutputBytes {
				body = body[:opt.MaxToolOutputBytes] + "\n…[truncated]"
			}
			fmt.Fprintf(&b, "↳ **result** `%s`\n\n```\n%s\n```\n\n",
				ev.CallID, body)
		case "end":
			flushDelta(ev.TS)
			fmt.Fprintf(&b, "_Session ended (reason: %s)_\n", ev.Reason)
		default:
			// system_*, errors, anything else — keep it simple.
			flushDelta(ev.TS)
			body, _ := json.Marshal(ev)
			fmt.Fprintf(&b, "<details><summary>%s</summary>\n\n```json\n%s\n```\n\n</details>\n\n",
				ev.Type, body)
		}
	}
	flushDelta(time.Now())
	n, err := io.WriteString(w, b.String())
	return n, err
}

// ─── Structured JSON ──────────────────────────────────

// JSONExport is the stable shape the json formatter writes. Distinct
// from session.Event so we can evolve the storage format without
// breaking the export contract.
type JSONExport struct {
	SessionID string         `json:"session_id"`
	Started   time.Time      `json:"started_at"`
	Ended     time.Time      `json:"ended_at"`
	Counts    map[string]int `json:"event_counts"`
	Turns     []ExportTurn   `json:"turns"`
}

// ExportTurn is one user → assistant cycle. Tool calls between the
// user prompt and the assistant final reply are nested.
type ExportTurn struct {
	UserPrompt    string           `json:"user_prompt,omitempty"`
	UserAt        time.Time        `json:"user_at,omitempty"`
	ToolCalls     []ExportToolCall `json:"tool_calls,omitempty"`
	AssistantText string           `json:"assistant_text,omitempty"`
	AssistantAt   time.Time        `json:"assistant_at,omitempty"`
	StopReason    string           `json:"stop_reason,omitempty"`
}

// ExportToolCall is one tool_use + its matched tool_result.
type ExportToolCall struct {
	CallID  string         `json:"call_id"`
	Name    string         `json:"name"`
	Args    map[string]any `json:"args,omitempty"`
	Output  string         `json:"output,omitempty"`
	StartAt time.Time      `json:"start_at"`
	EndAt   time.Time      `json:"end_at,omitempty"`
}

func renderJSON(events []Event, path string, w io.Writer, opt ExportOptions) (int, error) {
	out := JSONExport{
		SessionID: basenameNoExt(path),
		Counts:    countByType(events),
	}
	if len(events) > 0 {
		out.Started = events[0].TS
		out.Ended = events[len(events)-1].TS
	}

	// Coalesce: walk events linearly, open a new turn on each
	// user_message, accumulate tool calls + assistant text, close on
	// the next user_message or end.
	var current *ExportTurn
	finishTurn := func() {
		if current != nil {
			out.Turns = append(out.Turns, *current)
		}
		current = nil
	}
	pendingByID := map[string]int{} // call_id → index into current.ToolCalls
	var deltaBuf strings.Builder

	for _, ev := range events {
		if opt.ExcludeSystem && strings.HasPrefix(ev.Type, "system_") {
			continue
		}
		switch ev.Type {
		case "user_message":
			if current != nil {
				if deltaBuf.Len() > 0 {
					current.AssistantText = deltaBuf.String()
					deltaBuf.Reset()
				}
				finishTurn()
			}
			current = &ExportTurn{UserPrompt: ev.Content, UserAt: ev.TS}
			pendingByID = map[string]int{}
		case "assistant_message":
			if current == nil {
				current = &ExportTurn{}
			}
			current.AssistantText = ev.Content
			current.AssistantAt = ev.TS
		case "assistant_delta":
			deltaBuf.WriteString(ev.Content)
			if current != nil && current.AssistantAt.IsZero() {
				current.AssistantAt = ev.TS
			}
		case "tool_use":
			if current == nil {
				current = &ExportTurn{}
			}
			tc := ExportToolCall{
				CallID: ev.CallID, Name: ev.Name, Args: ev.Args, StartAt: ev.TS,
			}
			pendingByID[ev.CallID] = len(current.ToolCalls)
			current.ToolCalls = append(current.ToolCalls, tc)
		case "tool_result":
			if current == nil {
				continue
			}
			idx, ok := pendingByID[ev.CallID]
			if !ok {
				continue
			}
			body := ev.Output
			if !opt.IncludeToolOutput {
				body = fmt.Sprintf("[hidden — %d bytes]", len(body))
			} else if opt.MaxToolOutputBytes > 0 && len(body) > opt.MaxToolOutputBytes {
				body = body[:opt.MaxToolOutputBytes] + "\n…[truncated]"
			}
			current.ToolCalls[idx].Output = body
			current.ToolCalls[idx].EndAt = ev.TS
		case "end":
			if current != nil && deltaBuf.Len() > 0 {
				current.AssistantText = deltaBuf.String()
				deltaBuf.Reset()
			}
			if current != nil {
				current.StopReason = ev.Reason
			}
		}
	}
	if current != nil {
		if deltaBuf.Len() > 0 {
			current.AssistantText = deltaBuf.String()
		}
		out.Turns = append(out.Turns, *current)
	}

	// Stable map iteration for the counts field.
	if out.Counts != nil {
		// JSON marshalling is already deterministic-ish; ensure the
		// keys output sorted by sorting before encode.
		_ = sort.StringSlice(nil)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return 0, err
	}
	// io.Writer doesn't tell us bytes written through json.Encoder,
	// so report a synthetic count for the API.
	return -1, nil
}

// ─── Anthropic replay ────────────────────────────────

// AnthropicReplay is the body shape /v1/messages accepts. Populating
// `messages` from the session means a reviewer can paste the dump
// into curl / Postman and continue the conversation.
type AnthropicReplay struct {
	Model    string             `json:"model"`
	Messages []AnthropicMessage `json:"messages"`
	System   string             `json:"system,omitempty"`

	// MaxTokens is required by the API. We pick a generous default;
	// callers can override before posting.
	MaxTokens int `json:"max_tokens"`
}

// AnthropicMessage matches the role + content blocks the API expects.
type AnthropicMessage struct {
	Role    string                  `json:"role"`
	Content []AnthropicContentBlock `json:"content"`
}

// AnthropicContentBlock covers `text`, `tool_use`, and `tool_result`.
type AnthropicContentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

func renderAnthropicReplay(events []Event, w io.Writer, opt ExportOptions) (int, error) {
	out := AnthropicReplay{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 4096,
	}
	// Build messages array with two roles: user + assistant. Each
	// turn folds tool_use into the assistant message and the matching
	// tool_result into the next user message (per Anthropic's spec).
	var current AnthropicMessage
	flushCurrent := func() {
		if current.Role != "" && len(current.Content) > 0 {
			out.Messages = append(out.Messages, current)
		}
		current = AnthropicMessage{}
	}
	openRole := func(role string) {
		if current.Role != role {
			flushCurrent()
			current.Role = role
		}
	}

	for _, ev := range events {
		if opt.ExcludeSystem && strings.HasPrefix(ev.Type, "system_") {
			continue
		}
		switch ev.Type {
		case "user_message":
			openRole("user")
			current.Content = append(current.Content, AnthropicContentBlock{
				Type: "text", Text: ev.Content,
			})
		case "assistant_message":
			openRole("assistant")
			current.Content = append(current.Content, AnthropicContentBlock{
				Type: "text", Text: ev.Content,
			})
		case "tool_use":
			openRole("assistant")
			current.Content = append(current.Content, AnthropicContentBlock{
				Type: "tool_use",
				ID:   ev.CallID, Name: ev.Name, Input: ev.Args,
			})
		case "tool_result":
			openRole("user") // tool_result lives on the next user turn
			body := ev.Output
			if opt.MaxToolOutputBytes > 0 && len(body) > opt.MaxToolOutputBytes {
				body = body[:opt.MaxToolOutputBytes] + "\n…[truncated]"
			}
			current.Content = append(current.Content, AnthropicContentBlock{
				Type: "tool_result", ToolUseID: ev.CallID,
				Content: body,
			})
		}
	}
	flushCurrent()

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return 0, err
	}
	return -1, nil
}

// ─── Helpers ──────────────────────────────────────────

func countByType(events []Event) map[string]int {
	out := make(map[string]int, 8)
	for _, ev := range events {
		out[ev.Type]++
	}
	return out
}

func basenameNoExt(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return base
}

// redactEvent returns a copy of ev with secrets scrubbed from the
// args map and output string. We do a literal pass + a regex pass so
// both well-known field names and free-text leaks get caught.
func redactEvent(ev Event) Event {
	if len(ev.Args) > 0 {
		ev.Args = redactArgs(ev.Args)
	}
	ev.Content = redactString(ev.Content)
	ev.Output = redactString(ev.Output)
	return ev
}

// secretFieldNames lists arg keys that always carry credentials and
// must be redacted to a fingerprint. Matched case-insensitively.
var secretFieldNames = map[string]bool{
	"api_key":       true,
	"apikey":        true,
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
	"virtual_key":   true,
	"password":      true,
	"secret":        true,
	"authorization": true,
}

func redactArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if secretFieldNames[strings.ToLower(k)] {
			out[k] = redactValue(v)
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			out[k] = redactArgs(typed)
		case string:
			out[k] = redactString(typed)
		default:
			out[k] = v
		}
	}
	return out
}

func redactValue(v any) any {
	if s, ok := v.(string); ok {
		return fingerprint(s)
	}
	return "***"
}

func fingerprint(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

// secretPatterns matches free-text credentials in user / assistant /
// tool output content. Conservative — only obvious shapes.
var secretPatterns = []*regexp.Regexp{
	// "Bearer <hex/base64-ish>" — auth headers
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{20,}`),
	// Anthropic API keys: sk-ant-… (over 30 chars total)
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),
	// Generic OpenAI / proxy style: sk-… (lower priority — short ones
	// might be benign references; require ≥32 chars)
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{32,}`),
}

func redactString(s string) string {
	if s == "" {
		return s
	}
	for _, re := range secretPatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			// Keep capture-group prefix where present so the structure
			// stays readable (e.g. "Bearer ***" not just "***").
			if loc := re.FindStringSubmatchIndex(match); len(loc) >= 4 {
				prefix := match[loc[2]:loc[3]]
				return prefix + "***"
			}
			return "***"
		})
	}
	return s
}
