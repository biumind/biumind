// Bundle installer for every interactive tool.
//
// All wiring is optional: each backend pointer (Perms, Cwd, Cron,
// Notifier, Skills) can be nil and the corresponding tool will
// soft-error gracefully. This way the same Register call works in
// headless / cloud / desktop contexts.

package interactive

import "github.com/biumind/biumind/apps/cli/biu/internal/engine"

type Options struct {
	Perms         PermissionsAccessor
	CwdSwitcher   CwdSwitcher
	Cron          *CronStore
	Notifier      Notifier
	Skills        SkillRegistry
	WorktreeState WorktreeStore // optional persistence for --resume

	// Plans persists ExitPlanMode payloads to disk. Optional — nil
	// keeps plans in-memory only (still surfaced via tool result).
	Plans PlanStore

	// SessionID is forwarded to the plan store as the basename.
	// Empty = the store falls back to a timestamp.
	SessionID string

	// StructuredOutputSchema, when non-nil, registers the
	// StructuredOutput tool with the given JSON Schema so SDK /
	// headless callers can ask for typed final responses. nil
	// disables the tool entirely (interactive REPL doesn't need
	// it).
	StructuredOutputSchema map[string]any

	// SkipAskUser, when true, leaves AskUserQuestion out of the
	// catalog. The tool blocks on env.AskUser → UserQuestionAskEvent
	// until someone writes to the Decision channel; consumers that
	// only see translated SDK events (biumindkit embedders without
	// an answer hook) would deadlock the session. Zero value keeps
	// the tool registered (REPL wires the answer path itself).
	SkipAskUser bool
}

// Register installs every interactive tool onto reg.
func Register(reg *engine.SimpleRegistry, opt Options) []string {
	installed := []string{}

	reg.Register(EnterPlanModeTool{Perms: opt.Perms})
	reg.Register(ExitPlanModeTool{
		Perms:     opt.Perms,
		PlanStore: opt.Plans,
		SessionID: opt.SessionID,
	})
	installed = append(installed, "EnterPlanMode", "ExitPlanMode")

	if !opt.SkipAskUser {
		reg.Register(AskUserQuestionTool{})
		installed = append(installed, "AskUserQuestion")
	}

	enter := &EnterWorktreeTool{Cwd: opt.CwdSwitcher, State: opt.WorktreeState}
	reg.Register(enter)
	reg.Register(ExitWorktreeTool{Cwd: opt.CwdSwitcher, Enter: enter})
	installed = append(installed, "EnterWorktree", "ExitWorktree")

	reg.Register(CronCreateTool{Store: opt.Cron})
	reg.Register(CronDeleteTool{Store: opt.Cron})
	reg.Register(CronListTool{Store: opt.Cron})
	installed = append(installed, "CronCreate", "CronDelete", "CronList")

	reg.Register(PushNotificationTool{Notifier: opt.Notifier})
	installed = append(installed, "PushNotification")

	// Brief: structured user-facing message (P20.56). Same Notifier
	// backend powers the proactive ping; nil notifier → tool still
	// works (just no desktop ping on proactive briefs).
	reg.Register(BriefTool{Notifier: opt.Notifier})
	installed = append(installed, BriefToolName)

	// Config: read/write biu's settings.json (P20.56). No backend
	// dependency — the tool reads/writes ~/.biumind/settings.json
	// directly with atomic temp+rename.
	reg.Register(ConfigTool{})
	installed = append(installed, ConfigToolName)

	reg.Register(SkillTool{Registry: opt.Skills})
	installed = append(installed, "Skill")

	// Sleep — wait without holding a shell. Concurrency-safe so
	// multiple parallel calls don't interfere. Always registered.
	reg.Register(SleepTool{})
	installed = append(installed, "Sleep")

	// StructuredOutput — final-response tool for SDK / headless
	// callers. Only registered when an explicit schema is supplied;
	// interactive REPL doesn't need it.
	if opt.StructuredOutputSchema != nil {
		reg.Register(StructuredOutputTool{Schema: opt.StructuredOutputSchema})
		installed = append(installed, "StructuredOutput")
	}

	return installed
}
