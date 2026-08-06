// Bundle installer for native file tools.

package files

import (
	"context"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// Register installs Read/Edit/Write/MultiEdit/Glob/Grep onto reg
// under their canonical capitalised names AND under the lowercase
// aliases the legacy tools.Defaults() uses, so when both are
// registered the natives win every name comparison the model could
// produce.
//
// engine.SimpleRegistry.Register is last-write-wins on name, so
// callers must invoke this AFTER tools.Defaults().EngineRegistry().
func Register(reg *engine.SimpleRegistry) []string {
	pairs := []struct {
		canonical string
		aliases   []string
		impl      engine.Tool
	}{
		{"Read", []string{"read"}, ReadTool{}},
		{"Edit", []string{"edit"}, EditTool{}},
		{"Write", []string{"write"}, WriteTool{}},
		{"MultiEdit", nil, MultiEditTool{}},
		{"Glob", []string{"glob"}, GlobTool{}},
		{"Grep", []string{"grep"}, GrepTool{}},
		{"NotebookRead", nil, NotebookReadTool{}},
		{"NotebookEdit", nil, NotebookEditTool{}},
	}
	names := make([]string, 0, len(pairs)*2)
	for _, p := range pairs {
		reg.Register(p.impl)
		names = append(names, p.canonical)
		for _, alias := range p.aliases {
			reg.Register(aliasTool{name: alias, inner: p.impl})
			names = append(names, alias)
		}
	}
	return names
}

// aliasTool is a thin wrapper that overrides Name() so we can
// register the same impl under multiple names without changing the
// underlying type.
type aliasTool struct {
	name  string
	inner engine.Tool
}

func (a aliasTool) Name() string                             { return a.name }
func (a aliasTool) Description(in map[string]any) string     { return a.inner.Description(in) }
func (a aliasTool) InputSchema() map[string]any              { return a.inner.InputSchema() }
func (a aliasTool) IsReadOnly(in map[string]any) bool        { return a.inner.IsReadOnly(in) }
func (a aliasTool) IsDestructive(in map[string]any) bool     { return a.inner.IsDestructive(in) }
func (a aliasTool) IsConcurrencySafe(in map[string]any) bool { return a.inner.IsConcurrencySafe(in) }
func (a aliasTool) InterruptBehavior() string                { return a.inner.InterruptBehavior() }
func (a aliasTool) Call(ctx context.Context, in map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	return a.inner.Call(ctx, in, env)
}
