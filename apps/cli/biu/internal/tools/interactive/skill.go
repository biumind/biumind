// Skill — invoke a registered skill by name.
//
// A skill is a named, parameterised prompt
// template the user has dropped under ~/.biumind/skills/<name>/ or
// the project's own .biumind/skills/. Registry + argument substitution
// land in Phase D — this file is the engine-facing surface.
//
// When SkillRegistry is nil the tool soft-errors so the model can
// fall back to a plain prompt. When it's wired, the tool returns the
// skill's expanded prompt as a tool_result so the parent agent can
// keep going without nesting another QueryEngine.

package interactive

import (
	"context"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// SkillRegistry is the lookup contract. Implementations live in
// internal/skills/ once Phase D ships.
type SkillRegistry interface {
	Lookup(name string) (Skill, bool)
}

// Skill is a prompt template + argument expander. Implementations are
// free to do any side-effect they want inside Run (e.g. call a
// sub-agent), but the simple skill contract is just "produce text".
type Skill interface {
	Name() string
	// Run expands the skill template with the supplied args. Returns
	// the body the model should treat as the skill's contribution.
	Run(ctx context.Context, args string) (string, error)
}

type SkillTool struct {
	Registry SkillRegistry
}

func (SkillTool) Name() string { return "Skill" }

func (SkillTool) Description(_ map[string]any) string {
	return "Invoke a registered skill by name. Use this when the user " +
		"types `/<skill-name>` or when a skill clearly matches the " +
		"current request. Skills are pre-baked prompt templates."
}

func (SkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{"type": "string"},
			"args":  map[string]any{"type": "string"},
		},
		"required": []string{"skill"},
	}
}

func (SkillTool) IsReadOnly(_ map[string]any) bool        { return true }
func (SkillTool) IsDestructive(_ map[string]any) bool     { return false }
func (SkillTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (SkillTool) InterruptBehavior() string               { return "cancel" }

func (s SkillTool) Call(ctx context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if s.Registry == nil {
		return softErr("Skill", "no skill registry configured"), nil
	}
	name, _ := input["skill"].(string)
	if strings.TrimSpace(name) == "" {
		return softErr("Skill", "skill name required"), nil
	}
	skill, ok := s.Registry.Lookup(name)
	if !ok {
		return softErr("Skill", "unknown skill: "+name), nil
	}
	args, _ := input["args"].(string)
	body, err := skill.Run(ctx, args)
	if err != nil {
		return softErr("Skill", err.Error()), nil
	}
	return text(body), nil
}
