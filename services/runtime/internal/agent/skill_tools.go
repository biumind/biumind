// Six builtin tools the agent loop uses to discover, activate, and
// drive Skills. Mirrors the design in
// docs/BiuMind-Skills-Design.md §7. Wired per-run by
// RegisterSkillTools so the (orgID, agentID, ownerID, sessionID)
// context the registry calls need is captured at Run() time rather
// than baked into the global tool fleet.
//
// Risk levels translate to the agent's PermissionMode gate:
//
//	skill.list           Low      always allowed (read-only enumeration)
//	skill.activate       Low      load instructions; pure read
//	skill.read_reference Low      read bundled resource bytes
//	skill.exec_script    High     sandbox command — humanIntervention=required
//	skill.export_file    Medium   write to user Files; bounded blast radius
//	skill.propose        Medium   write staged skill row; bounded
//
// Sandbox-backed tools (exec_script + export_file) soft-error until
// the dedicated wiring lands (PS3.6 / PS3.x). The soft-error pattern
// is deliberate: the tool returns a friendly message instead of
// crashing the loop, so the model can fall back to runCommand or
// give up gracefully.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/biumind/biumind/services/runtime/internal/authz"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
	"github.com/google/uuid"
)

// SkillToolDeps is the per-run context the Skills tools need. All
// fields are required EXCEPT Sandbox + Files which are optional —
// those tools surface a friendly soft-error when they're nil so the
// daemon variants without a sandbox configured don't 500 the model.
type SkillToolDeps struct {
	Registry  *skillsreg.Registry
	OrgID     uuid.UUID
	AgentID   uuid.UUID
	OwnerID   uuid.UUID
	SessionID uuid.UUID
	Cwd       string

	// RunID is the agent loop's run identifier (e.g. "rn_abc123"),
	// used as the topic key for AG-UI event fanout. Publisher emits
	// to "agui:run:<RunID>" so the same SSE stream the chat UI is
	// already subscribed to picks up skill events without a second
	// channel.
	RunID string

	// EventSink 是事件投递层。runtime worker（agentplane.Worker）注入
	// FrameEventSink；nil 时彻底静默（不再 fallback 到 publisher，已删）。
	EventSink SkillEventSink

	// Authz gates every high-risk tool against the Cedar policy file
	// at deploy/docker-compose/authz/policies/policies.cedar (PS3.4).
	// When nil the runtime falls back to authz.AlwaysAllow — useful
	// in CLI-only / dev mode where Authz isn't running, and explicitly
	// flagged on startup. Production daemon main.go wires NewHTTP.
	Authz authz.Decider

	// Sandbox + Files reserved for PS3.6 — for now these stay nil
	// and exec_script / export_file return a "sandbox not
	// configured" message. Keeping the field here so the wiring
	// signature is stable across the gap.
	Sandbox SkillSandbox
	Files   SkillFiles

	// Memory + Wiki back the skill.recall_memory + skill.read_wiki
	// tools (P0-#3). Both are Cedar-gated against the calling skill's
	// permissions list — a skill that didn't declare memory.recall in
	// its SKILL.md frontmatter cannot pull memories even if the
	// Memory client is wired.
	//
	// ProjectID is the brain project memory access scopes to. Empty
	// disables memory.recall (same project_id-required posture as the
	// non-skill memory.recall tool in tools.go).
	Memory    MemoryClient
	Wiki      WikiClient
	ProjectID string
}

// emit publishes an event to the configured sink. Best-effort —
// a failed publish must not abort the tool (the tool result still
// flows back to the model as a regular tool_result; the UI just
// misses the live activity card).
//
// EventSink nil → 静默（S11-4 删了 publisher fallback）。
func (d *SkillToolDeps) emit(ctx context.Context, eventType string, payload map[string]any) {
	if d.EventSink == nil {
		return
	}
	d.EventSink.Emit(ctx, eventType, payload)
}

// decider returns deps.Authz or AlwaysAllow when nil. Centralised so
// every tool factory shares the same fail-open semantics in dev (and
// fail-closed if the user wires AlwaysDeny explicitly).
func (d *SkillToolDeps) decider() authz.Decider {
	if d.Authz != nil {
		return d.Authz
	}
	return authz.AlwaysAllow{}
}

// authzAllow asks the decider whether [action] on [skill] is
// permitted for the caller. The skill's permissions slice + status
// + org_id flow into the Cedar resource so the policy file does the
// real work — this helper only marshals.
//
// Returns (true, nil) on Allow, (false, nil) on Deny (with the
// reason reachable via the returned message), or (false, err) when
// the Authz call itself errored. Tools fail-closed on err.
func (d *SkillToolDeps) authzAllow(ctx context.Context, action string, s *skillsreg.Skill) (bool, string, error) {
	ownerID := ""
	if s.OwnerID != nil {
		ownerID = s.OwnerID.String()
	}
	res, err := d.decider().Check(ctx, authz.Request{
		Principal: authz.Entity{
			Type: "User",
			ID:   d.OwnerID.String(),
			Attributes: map[string]any{
				"id":     d.OwnerID.String(),
				"org_id": d.OrgID.String(),
			},
		},
		Action: action,
		Resource: authz.Entity{
			Type: "Skill",
			ID:   s.ID,
			Attributes: map[string]any{
				"id":          s.ID,
				"org_id":      s.OrgID.String(),
				"owner_id":    ownerID,
				"status":      string(s.Status),
				"source":      string(s.Source),
				"permissions": stringSliceToAny(s.Permissions),
			},
		},
	})
	if err != nil {
		return false, "", fmt.Errorf("authz: %w", err)
	}
	return res.Decision == authz.Allow, res.Reason, nil
}

func stringSliceToAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, s := range xs {
		out[i] = s
	}
	return out
}

// SkillSandbox is the slice of services/sandbox the exec_script tool
// needs. Defined here (rather than imported from sandbox) so the
// agent package doesn't take a runtime dep on sandbox until PS3.6.
//
// PS3.6 contract: ExecWithSkill mounts every entry in skill.Resources
// at /skill/<vpath> inside the sandbox, then runs the command. Inline
// resources (≤4KB UTF-8) get written via shell prep; binary or
// large resources require Files CAS → tar pipe (deferred to v2.0
// when bundle_sha256 is non-empty and the Files internal endpoint
// lands).
type SkillSandbox interface {
	ExecWithSkill(ctx context.Context, sessionID, command string, skill *skillsreg.Skill) (output string, exitCode int, err error)
}

// SkillFiles is the slice of services/files the export_file tool
// needs. Same isolation rationale as SkillSandbox.
type SkillFiles interface {
	UploadFromSandbox(ctx context.Context, ownerID uuid.UUID, sandboxPath, filename string) (fileID, downloadURL string, err error)
}

// WikiClient is the slice of services/brain Wiki the skill.read_wiki
// tool needs. Mirrors the Brain HTTP API at /v1/wiki/* — Read by id
// returns the page body; Search returns up to N hits.
type WikiClient interface {
	Search(ctx context.Context, query string, limit int) ([]WikiHit, error)
	Read(ctx context.Context, pageID string) (*WikiPage, error)
}

// WikiHit / WikiPage — minimal shapes the LLM needs. Anything beyond
// id/title/body would just bloat the tool_result without earning its
// keep in the prompt budget.
type WikiHit struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float32 `json:"score"`
}
type WikiPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// RegisterSkillTools attaches the eight tools to a per-run Registry.
// Skip silently when Registry is nil so callers (test harnesses,
// daemon variants without DB access) can opt out.
func RegisterSkillTools(r *Registry, deps SkillToolDeps) {
	if r == nil || deps.Registry == nil {
		return
	}
	r.Register(skillListTool(deps))
	r.Register(skillActivateTool(deps))
	r.Register(skillReadReferenceTool(deps))
	r.Register(skillExecScriptTool(deps))
	r.Register(skillExportFileTool(deps))
	r.Register(skillProposeTool(deps))
	r.Register(skillRecallMemoryTool(deps))
	r.Register(skillReadWikiTool(deps))
}

// ─── skill.list ─────────────────────────────────────────────

func skillListTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.list",
		Description: "List skills available to the current agent. Returns " +
			"a JSON array of {identifier, name, description, status, source}. " +
			"Use this to discover skills before calling skill.activate.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			loaded, err := d.Registry.LoadForAgent(ctx, skillsreg.LoadForAgentInput{
				OrgID:            d.OrgID,
				AgentID:          d.AgentID,
				Cwd:              d.Cwd,
				IncludeOrgShared: true,
			})
			if err != nil {
				return "", fmt.Errorf("list skills: %w", err)
			}
			type row struct {
				Identifier  string `json:"identifier"`
				Name        string `json:"name"`
				Description string `json:"description"`
				Status      string `json:"status"`
				Source      string `json:"source"`
				Tier        string `json:"tier"` // pinned | auto_attach | available
			}
			var out []row
			for _, s := range loaded.Pinned {
				out = append(out, row{s.Identifier, s.Name, s.Description, string(s.Status), string(s.Source), "pinned"})
			}
			for _, s := range loaded.AutoAttach {
				out = append(out, row{s.Identifier, s.Name, s.Description, string(s.Status), string(s.Source), "auto_attach"})
			}
			for _, s := range loaded.Available {
				out = append(out, row{s.Identifier, s.Name, s.Description, string(s.Status), string(s.Source), "available"})
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			return string(b), nil
		},
	}
}

// ─── skill.activate ─────────────────────────────────────────

func skillActivateTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.activate",
		Description: "Load a skill's full instructions (the SKILL.md body) into " +
			"context. Returns the body verbatim; the agent should follow it. " +
			"If the skill is already in <pinned_skills> or " +
			"<auto_attached_skills> in the system prompt, do NOT call this — " +
			"those bodies are already loaded.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier": map[string]any{"type": "string", "description": "The skill's identifier (kebab-case slug)."},
			},
			"required": []string{"identifier"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			ident, _ := args["identifier"].(string)
			ident = strings.TrimSpace(ident)
			if ident == "" {
				return "", fmt.Errorf("identifier required")
			}
			s, err := d.Registry.GetByIdentifier(ctx, d.OrgID, ident)
			if err != nil {
				return availableSkillsList(ctx, d, fmt.Sprintf("Skill %q not found.", ident))
			}
			if s.Status != skillsreg.StatusActive {
				return "", fmt.Errorf("skill %q is %s, not active", ident, s.Status)
			}
			// Audit ledger — append-only row in runtime.skill_activations.
			// Best-effort: a write failure must not block the model
			// response. Same fail-soft contract as emit().
			if _, err := d.Registry.LogActivation(ctx, skillsreg.Activation{
				SessionID: d.SessionID,
				SkillID:   s.ID,
				Trigger:   skillsreg.TriggerToolCall,
				TraceID:   d.RunID,
			}); err != nil {
				// No logger plumbed through SkillToolDeps yet; the
				// activation table is non-critical so we drop silently.
				// If activation telemetry becomes load-bearing, route
				// the registry's logger here.
				_ = err
			}
			d.emit(ctx, "biumind.runtime.skill.activated", map[string]any{
				"skill_id":   s.ID,
				"identifier": s.Identifier,
				"name":       s.Name,
				"trigger":    "tool_call",
			})
			return s.Content, nil
		},
	}
}

// availableSkillsList builds a fallback message when activate misses
// — show what's actually available so the model can pick the right
// identifier. Mirrors lobehub's skill_not_found behaviour.
func availableSkillsList(ctx context.Context, d SkillToolDeps, prefix string) (string, error) {
	loaded, err := d.Registry.LoadForAgent(ctx, skillsreg.LoadForAgentInput{
		OrgID: d.OrgID, AgentID: d.AgentID, IncludeOrgShared: true,
	})
	if err != nil {
		return prefix, nil
	}
	type r struct {
		Identifier  string `json:"identifier"`
		Description string `json:"description"`
	}
	var rows []r
	for _, s := range loaded.Available {
		rows = append(rows, r{s.Identifier, s.Description})
	}
	for _, s := range loaded.AutoAttach {
		rows = append(rows, r{s.Identifier, s.Description})
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return prefix + " Available skills:\n" + string(b), nil
}

// ─── skill.read_reference ───────────────────────────────────

func skillReadReferenceTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.read_reference",
		Description: "Read a bundled resource file from a skill (e.g. " +
			"references/checklist.md). Use only paths the skill's body " +
			"references; the path must NOT contain '..'.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier": map[string]any{"type": "string", "description": "Skill identifier."},
				"path":       map[string]any{"type": "string", "description": "Virtual path inside the skill bundle (e.g. references/foo.md)."},
			},
			"required": []string{"identifier", "path"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			ident, _ := args["identifier"].(string)
			path, _ := args["path"].(string)
			if ident == "" || path == "" {
				return "", fmt.Errorf("identifier + path required")
			}
			if strings.Contains(path, "..") {
				return "", fmt.Errorf("path traversal not allowed")
			}
			s, err := d.Registry.GetByIdentifier(ctx, d.OrgID, ident)
			if err != nil {
				return "", fmt.Errorf("skill %q not found", ident)
			}
			meta, ok := s.Resources[path]
			if !ok {
				return "", fmt.Errorf("resource %q not found in skill %q", path, ident)
			}
			if meta.Inline != "" {
				return meta.Inline, nil
			}
			// CAS path — Files service fetch lands in PS3.6 alongside
			// sandbox bundle mounting; for now surface the hash so the
			// model can at least confirm the resource exists.
			return "", fmt.Errorf("resource %q lives in CAS (sha256=%s); "+
				"non-inline resources require the Files client (PS3.6)", path, meta.Sha256)
		},
	}
}

// ─── skill.exec_script ──────────────────────────────────────

func skillExecScriptTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.exec_script",
		Description: "Execute a shell command inside a sandbox with the " +
			"specified skill's bundle mounted at /skill/. Use for commands " +
			"that need bundled scripts; for ad-hoc commands without skill " +
			"resources use bash. The user must confirm before execution.",
		Risk: RiskHigh,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier":  map[string]any{"type": "string", "description": "Skill whose bundle should be mounted at /skill/."},
				"command":     map[string]any{"type": "string", "description": "Shell command to run."},
				"description": map[string]any{"type": "string", "description": "One-line human-readable summary of what this does."},
			},
			"required": []string{"identifier", "command"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			ident, _ := args["identifier"].(string)
			cmd, _ := args["command"].(string)
			if ident == "" || cmd == "" {
				return "", fmt.Errorf("identifier + command required")
			}
			if d.Sandbox == nil {
				return "", fmt.Errorf("sandbox not configured (PS3.6 wires the runtime " +
					"sandbox client); fall back to bash for non-bundled commands")
			}
			s, err := d.Registry.GetByIdentifier(ctx, d.OrgID, ident)
			if err != nil {
				return "", fmt.Errorf("skill %q not found", ident)
			}
			ok, reason, err := d.authzAllow(ctx, "skill:exec_script", s)
			if err != nil {
				return "", err // fail-closed on authz outage
			}
			if !ok {
				return "", fmt.Errorf("authz denied skill:exec_script for %q: %s",
					ident, reason)
			}
			d.emit(ctx, "biumind.runtime.skill.exec_started", map[string]any{
				"skill_id":   s.ID,
				"identifier": s.Identifier,
				"command":    cmd,
			})
			out, exit, err := d.Sandbox.ExecWithSkill(ctx, d.SessionID.String(), cmd, s)
			if err != nil {
				d.emit(ctx, "biumind.runtime.skill.exec_finished", map[string]any{
					"skill_id":   s.ID,
					"identifier": s.Identifier,
					"error":      err.Error(),
				})
				return "", fmt.Errorf("exec_script: %w", err)
			}
			d.emit(ctx, "biumind.runtime.skill.exec_output", map[string]any{
				"skill_id": s.ID,
				"output":   out,
				"exit":     exit,
			})
			d.emit(ctx, "biumind.runtime.skill.exec_finished", map[string]any{
				"skill_id":   s.ID,
				"identifier": s.Identifier,
				"exit":       exit,
			})
			return fmt.Sprintf("exit=%d\n%s", exit, out), nil
		},
	}
}

// ─── skill.export_file ──────────────────────────────────────

func skillExportFileTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.export_file",
		Description: "Save a file produced inside the sandbox to the user's " +
			"Files. Returns a download URL and a file id. Use this when a " +
			"skill generates output the user should keep (reports, " +
			"processed data, artifacts).",
		Risk: RiskMedium,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sandbox_path": map[string]any{"type": "string", "description": "Path inside the sandbox to the file to export."},
				"filename":     map[string]any{"type": "string", "description": "Display filename for the user-facing copy."},
			},
			"required": []string{"sandbox_path", "filename"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["sandbox_path"].(string)
			fname, _ := args["filename"].(string)
			if path == "" || fname == "" {
				return "", fmt.Errorf("sandbox_path + filename required")
			}
			if d.Files == nil {
				return "", fmt.Errorf("files service not configured (PS3.6); " +
					"export_file unavailable in this runtime")
			}
			// Cedar gate. The export tool is not bound to a specific
			// skill (the model picks the file by sandbox path), so we
			// synthesise a minimal Skill resource pinned to the
			// caller's org. The policies.cedar skills section checks
			// org_id + status="active"; org_id binding gives us the
			// cross-tenant defense-in-depth, status="active" is a
			// constant here because export only runs after a successful
			// exec, by which point the skill row was already gated.
			synth := &skillsreg.Skill{
				OrgID:  d.OrgID,
				Status: skillsreg.StatusActive,
			}
			ok, reason, err := d.authzAllow(ctx, "skill:export_file", synth)
			if err != nil {
				return "", err // fail-closed on authz outage
			}
			if !ok {
				return "", fmt.Errorf("authz denied skill:export_file: %s", reason)
			}
			id, url, err := d.Files.UploadFromSandbox(ctx, d.OwnerID, path, fname)
			if err != nil {
				return "", fmt.Errorf("export_file: %w", err)
			}
			d.emit(ctx, "biumind.runtime.skill.export_file_completed", map[string]any{
				"file_id":  id,
				"url":      url,
				"filename": fname,
			})
			return fmt.Sprintf(`{"file_id":%q,"url":%q,"filename":%q}`, id, url, fname), nil
		},
	}
}

// ─── skill.propose ──────────────────────────────────────────

func skillProposeTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.propose",
		Description: "Save a draft SKILL.md so the user can review and " +
			"approve it. Use after captured a workflow worth reusing. " +
			"The skill lands as status='staged'; the user must explicitly " +
			"approve it before agents can use it.",
		Risk: RiskMedium,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier":  map[string]any{"type": "string", "description": "kebab-case slug, unique within the org."},
				"name":        map[string]any{"type": "string", "description": "Display name."},
				"description": map[string]any{"type": "string", "description": "One-line summary; drives skill.activate matching."},
				"body":        map[string]any{"type": "string", "description": "SKILL.md body, post-frontmatter markdown."},
				"paths":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional auto-attach globs."},
				"permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Declared permissions (sandbox.exec, network.fetch, ...)."},
			},
			"required": []string{"identifier", "name", "description", "body"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			id := newProposedSkillID()
			in := skillsreg.CreateInput{
				ID:          id,
				OrgID:       d.OrgID,
				OwnerID:     &d.OwnerID,
				Identifier:  strArg(args, "identifier"),
				Name:        strArg(args, "name"),
				Description: strArg(args, "description"),
				Source:      skillsreg.SourceUser,
				Content:     strArg(args, "body"),
				Paths:       strSlice(args["paths"]),
				Permissions: strSlice(args["permissions"]),
				Status:      skillsreg.StatusStaged,
			}
			if in.Identifier == "" || in.Name == "" || in.Description == "" || in.Content == "" {
				return "", fmt.Errorf("identifier, name, description, body all required")
			}
			s, err := d.Registry.Create(ctx, in)
			if err != nil {
				return "", fmt.Errorf("propose: %w", err)
			}
			return fmt.Sprintf(`{"skill_id":%q,"status":"staged",`+
				`"message":"Draft saved — user approval required before activation."}`,
				s.ID), nil
		},
	}
}

// ─── helpers ───────────────────────────────────────────────

func newProposedSkillID() string {
	return "skill_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:32]
}

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func strSlice(v any) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// permissionsAllow reports whether a skill's declared permissions
// list contains the given capability. Until Cedar translation lands
// in PS3.4, this is the only gate stopping a low-permission skill
// from invoking high-risk tools.
func permissionsAllow(decl []string, want string) bool {
	for _, p := range decl {
		if p == want {
			return true
		}
	}
	return false
}

// ─── skill.recall_memory ────────────────────────────────────

func skillRecallMemoryTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.recall_memory",
		Description: "Search the user's long-term memory through this skill's " +
			"declared memory.recall permission. Use INSTEAD of memory.recall " +
			"when you're acting on behalf of a specific Skill — every call " +
			"audits against the skill row and respects the skill's Cedar " +
			"permission grant.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier": map[string]any{"type": "string", "description": "Skill identifier the call is acting on behalf of."},
				"query":      map[string]any{"type": "string"},
				"limit":      map[string]any{"type": "integer", "default": 5},
			},
			"required": []string{"identifier", "query"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			ident, _ := args["identifier"].(string)
			q, _ := args["query"].(string)
			ident = strings.TrimSpace(ident)
			q = strings.TrimSpace(q)
			if ident == "" || q == "" {
				return "", fmt.Errorf("identifier + query required")
			}
			if d.Memory == nil {
				return "", fmt.Errorf("memory client not configured")
			}
			if d.ProjectID == "" {
				return "", fmt.Errorf("project_id absent — start the run with a project_id to enable memory access")
			}
			s, err := d.Registry.GetByIdentifier(ctx, d.OrgID, ident)
			if err != nil {
				return "", fmt.Errorf("skill %q not found", ident)
			}
			ok, reason, err := d.authzAllow(ctx, "skill:recall_memory", s)
			if err != nil {
				return "", err // fail-closed
			}
			if !ok {
				return "", fmt.Errorf("authz denied skill:recall_memory for %q: %s",
					ident, reason)
			}
			limit := 5
			if v, ok := args["limit"].(float64); ok && v > 0 {
				limit = int(v)
			} else if v, ok := args["limit"].(int); ok && v > 0 {
				limit = v
			}
			hits, err := d.Memory.Recall(ctx, d.ProjectID, q, limit)
			if err != nil {
				return "", fmt.Errorf("recall: %w", err)
			}
			if len(hits) == 0 {
				return "(no memories)", nil
			}
			b, _ := json.MarshalIndent(hits, "", "  ")
			return string(b), nil
		},
	}
}

// ─── skill.read_wiki ────────────────────────────────────────

func skillReadWikiTool(d SkillToolDeps) *Tool {
	return &Tool{
		Name: "skill.read_wiki",
		Description: "Search or read the user's Wiki through this skill's " +
			"declared wiki.read permission. Pass page_id to fetch one " +
			"page's body verbatim, or query to search and get up to " +
			"limit hits ranked by relevance.",
		Risk:       RiskLow,
		IsReadOnly: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"identifier": map[string]any{"type": "string", "description": "Skill identifier the call is acting on behalf of."},
				"query":      map[string]any{"type": "string", "description": "Search query (mutually exclusive with page_id)."},
				"page_id":    map[string]any{"type": "string", "description": "Specific page to read (mutually exclusive with query)."},
				"limit":      map[string]any{"type": "integer", "default": 5},
			},
			"required": []string{"identifier"},
		},
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			ident, _ := args["identifier"].(string)
			ident = strings.TrimSpace(ident)
			if ident == "" {
				return "", fmt.Errorf("identifier required")
			}
			if d.Wiki == nil {
				return "", fmt.Errorf("wiki client not configured")
			}
			s, err := d.Registry.GetByIdentifier(ctx, d.OrgID, ident)
			if err != nil {
				return "", fmt.Errorf("skill %q not found", ident)
			}
			ok, reason, err := d.authzAllow(ctx, "skill:read_wiki", s)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("authz denied skill:read_wiki for %q: %s",
					ident, reason)
			}
			pageID, _ := args["page_id"].(string)
			pageID = strings.TrimSpace(pageID)
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if pageID != "" {
				page, err := d.Wiki.Read(ctx, pageID)
				if err != nil {
					return "", fmt.Errorf("read wiki page: %w", err)
				}
				b, _ := json.MarshalIndent(page, "", "  ")
				return string(b), nil
			}
			if query == "" {
				return "", fmt.Errorf("either query or page_id required")
			}
			limit := 5
			if v, ok := args["limit"].(float64); ok && v > 0 {
				limit = int(v)
			} else if v, ok := args["limit"].(int); ok && v > 0 {
				limit = v
			}
			hits, err := d.Wiki.Search(ctx, query, limit)
			if err != nil {
				return "", fmt.Errorf("search wiki: %w", err)
			}
			if len(hits) == 0 {
				return "(no pages)", nil
			}
			b, _ := json.MarshalIndent(hits, "", "  ")
			return string(b), nil
		},
	}
}
