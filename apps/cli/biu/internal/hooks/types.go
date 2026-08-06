// Package hooks runs user-defined shell commands at lifecycle points
// in the agent loop.
//
// Wire format mirrors the settings.json hooks section:
//
//   "hooks": {
//     "PreToolUse": [
//       { "matcher": "Bash", "hooks": [
//         { "type": "command", "command": "scripts/audit.sh", "timeout": 30 }
//       ]}
//     ],
//     "PostToolUse": [...],
//     "UserPromptSubmit": [{"hooks":[{"type":"command","command":"echo $CLAUDE_PROMPT"}]}]
//   }
//
// Hook command contract:
//
//   * stdin: JSON object with event-specific fields (tool_name, tool_input,
//     prompt, etc.). One line, terminated with \n.
//   * stdout: optional JSON object. When valid JSON with the right shape,
//     biu uses fields like `decision`, `reason`, `prompt` to alter the
//     turn (block tool call, replace prompt, etc.).
//   * exitCode 0 — success; stdout consumed if JSON.
//   * exitCode 2 — soft block; stderr fed back into the assistant turn so
//     the model can react.
//   * other exit codes — non-fatal warning; stderr shown to the user.
//
// We deliberately ship just `command` hooks for P0; the prompt/agent/
// http/function variants are noted in the schema but not yet executed.

package hooks

// Event is the hook lifecycle name.
type Event string

const (
	EventPreToolUse         Event = "PreToolUse"
	EventPostToolUse        Event = "PostToolUse"
	EventPostToolUseFailure Event = "PostToolUseFailure"
	EventUserPromptSubmit   Event = "UserPromptSubmit"
	EventStop               Event = "Stop"
	// EventStopFailure fires when the top-level Submit terminates
	// with a non-recoverable error (provider failure, max-turn limit,
	// hook block at SessionStart). Distinct from EventStop which
	// fires on the success path. Payload mirrors EventStop with an
	// added `error` field. (P20.55)
	EventStopFailure Event = "StopFailure"
	// EventSubagentStart fires when a sub-agent dispatch begins.
	// Payload: agent_id, agent_type, prompt, parent_session_id.
	// (P20.55)
	EventSubagentStart Event = "SubagentStart"
	// EventSubagentStop fires when a sub-agent dispatch (Plan /
	// Explore / CodeReview / Verification / general-purpose / a
	// user-defined Definition) finishes. The hook payload includes
	// agent_id, agent_type, prompt, output, and stop_reason.
	EventSubagentStop Event = "SubagentStop"
	// EventTeammateIdle fires when an async teammate's Submit
	// returns and the goroutine drains a follow-up message (or
	// finishes for good). Useful for "ping me when researcher is
	// idle" workflows. Payload: handle_id, agent_type, output,
	// queue_depth (remaining queued follow-ups). (P20.55)
	EventTeammateIdle Event = "TeammateIdle"
	// EventPermissionRequest fires before biu asks the user for
	// permission on a tool call. Hook can either approve / deny via
	// Decision or just observe. Payload: tool_name, tool_input,
	// rule_decision (the permissions package's pre-prompt verdict).
	// (P20.55)
	EventPermissionRequest Event = "PermissionRequest"
	// EventPermissionDenied fires after a tool call is denied
	// (rule, user, or hook). Useful for audit trails. Payload:
	// tool_name, tool_input, denial_source, reason. (P20.55)
	EventPermissionDenied Event = "PermissionDenied"
	// EventTaskCreated / EventTaskCompleted fire from
	// TaskCreateTool / TaskUpdateTool when a task transitions
	// state. Useful for project-tracker integration ("post completed
	// tasks to Linear"). Payload: task_id, subject, owner. (P20.55)
	EventTaskCreated   Event = "TaskCreated"
	EventTaskCompleted Event = "TaskCompleted"
	// EventFileChanged fires when an in-engine file mutation
	// (Edit / Write / MultiEdit / NotebookEdit) commits. Payload:
	// path, tool_name. (P20.55)
	EventFileChanged Event = "FileChanged"
	// EventCwdChanged fires when QueryEngine.SetCwd flips the
	// working directory mid-session (e.g. EnterWorktree, the user's
	// `/cd`). Payload: from, to. (P20.55)
	EventCwdChanged   Event = "CwdChanged"
	EventSessionStart Event = "SessionStart"
	EventSessionEnd   Event = "SessionEnd"
	EventPreCompact   Event = "PreCompact"
	EventPostCompact  Event = "PostCompact"
	EventNotification Event = "Notification"
)

// AllEvents lists every event biu will fire. Useful for config
// validation.
var AllEvents = []Event{
	EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
	EventUserPromptSubmit, EventStop, EventStopFailure,
	EventSubagentStart, EventSubagentStop, EventTeammateIdle,
	EventPermissionRequest, EventPermissionDenied,
	EventTaskCreated, EventTaskCompleted,
	EventFileChanged, EventCwdChanged,
	EventSessionStart, EventSessionEnd,
	EventPreCompact, EventPostCompact, EventNotification,
}

// IsValid reports whether s is one of the known events.
func IsValid(s string) bool {
	for _, e := range AllEvents {
		if string(e) == s {
			return true
		}
	}
	return false
}

// Command is one configured hook command. Two execution shapes today:
//
//	type=command  — fork a subprocess; biu's settings.json default
//	type=internal — invoke a Go function registered with
//	                 RegisterInternal. Used by bundled plugins so
//	                 hooks ship inside the binary without depending
//	                 on python3 / bash being installed.
//
// Other types ("prompt", "agent", "http") are accepted but skipped at
// run-time with a warning so settings.json round-trips cleanly.
type Command struct {
	Type    string `json:"type"`              // "command" | "internal" | (reserved: prompt | agent | http)
	Command string `json:"command,omitempty"` // for type=command
	Handler string `json:"handler,omitempty"` // for type=internal — registered name
	Shell   string `json:"shell,omitempty"`   // bash | sh | pwsh — defaults to sh
	Timeout int    `json:"timeout,omitempty"` // seconds; 0 = 60s default
	If      string `json:"if,omitempty"`      // optional precondition rule (parsed lazily)
}

// Matcher is one entry under an event name in settings.json. The
// Matcher field is a glob over event-specific identifiers (tool_name
// for PreToolUse / PostToolUse; notification_type for Notification;
// not used at all for SessionStart / Stop).
type Matcher struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []Command `json:"hooks"`
}

// Decision is what a hook stdout JSON can ask the engine to do. Empty
// fields mean "no opinion".
type Decision struct {
	// Block, when set, asks the engine to abort the in-flight tool call
	// or prompt. Reason flows back
	// into the assistant turn as a tool_result error.
	Block  bool   `json:"block,omitempty"`
	Reason string `json:"reason,omitempty"`

	// AdditionalContext, when non-empty on a SessionStart / UserPromptSubmit
	// hook, gets prepended to the assistant input as system context.
	AdditionalContext string `json:"additionalContext,omitempty"`

	// ReplacePrompt rewrites the user prompt before submission.
	ReplacePrompt string `json:"replacePrompt,omitempty"`

	// HookSpecificOutput is a catch-all for newer fields we don't
	// consume yet (e.g. retry: true on PermissionDenied). Forwarded to
	// callers verbatim.
	HookSpecificOutput map[string]any `json:"hookSpecificOutput,omitempty"`
}

// Result is what the runner returns to the engine. One Result per
// matching hook command.
type Result struct {
	Source   string // settings layer the hook came from (user/project/local)
	Event    Event  // event that fired this hook
	Command  string // shell command (for logging)
	ExitCode int
	Stdout   string
	Stderr   string
	Decision Decision // parsed JSON when stdout looked like JSON
	Err      error    // exec error (timeout, no shell, etc.); not from non-zero exit
	Elapsed  string   // duration as a human-readable string for telemetry
}

// IsBlocking reports whether this hook result should halt the
// in-flight operation. True when:
//
//   - the hook explicitly set decision.block=true, OR
//   - the hook exited with code 2.
func (r Result) IsBlocking() bool {
	return r.Decision.Block || r.ExitCode == 2
}
