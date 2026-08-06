// Web tool installer.
//
// Bundles Bash + WebFetch + WebSearch into a single Register call so
// main.go doesn't grow a wall of New / Register pairs every time a
// new web tool lands.

package web

import (
	"context"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/client/searxng"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
)

// Options controls which optional tools are registered. Bash is
// always installed; the search tools require an external backend.
type Options struct {
	// SearxNGURL enables WebSearch via a self-hosted SearxNG.
	// Empty string ⇒ WebSearch is registered with a nil provider so
	// it soft-errors (model is told the tool exists but isn't wired).
	SearxNGURL string

	// AllowBashNetwork sets the default network policy on Bash. The
	// model can still override per-call via input.allow_network.
	AllowBashNetwork bool

	// LSPBackend powers the LSP tool. nil ⇒ tool soft-errors so the
	// model falls back to Grep / Read.
	LSPBackend Backend

	// BgTasks, when non-nil, enables Bash{run_in_background:true} +
	// the BashOutput / KillBash partner tools. Pass the same store
	// instance used by main.go's REPL so /tasks sees the same
	// queue. nil ⇒ background mode is silently disabled.
	BgTasks *bgtask.Store

	// SandboxFSReadDeny / FSReadAllowWithinDeny / FSWriteAllowExtra
	// / FSWriteDenyWithinAllow forward the merged settings.json
	// `sandbox` block straight onto BashTool's struct fields. All
	// paths must be absolute — caller (the wiring layer) is
	// responsible for ~ / ${VAR} expansion before calling Register.
	SandboxFSReadDeny             []string
	SandboxFSReadAllowWithinDeny  []string
	SandboxFSWriteAllowExtra      []string
	SandboxFSWriteDenyWithinAllow []string

	// PermCtx, when non-nil, lets BashTool union the live
	// ctx.AdditionalDirectoryPaths() into FSWriteAllowExtra at every
	// call. /add-dir / settings reload then take effect without a
	// tool re-registration.
	PermCtx *permissions.Context

	// ExecHost (Runtime v3 轴 B) 透传给 BashTool。nil → 本机 local 行为
	// （今天）。non-local（cloud/none）→ BashTool divert 到 Host.Exec。
	ExecHost exechost.Host
}

// Register installs the web tools onto reg. Returns the installed
// tool names for telemetry.
func Register(reg *engine.SimpleRegistry, opt Options) []string {
	installed := []string{}

	reg.Register(BashTool{
		AllowNetworkByDefault:  opt.AllowBashNetwork,
		BgTasks:                opt.BgTasks,
		FSReadDeny:             opt.SandboxFSReadDeny,
		FSReadAllowWithinDeny:  opt.SandboxFSReadAllowWithinDeny,
		FSWriteAllowExtra:      opt.SandboxFSWriteAllowExtra,
		FSWriteDenyWithinAllow: opt.SandboxFSWriteDenyWithinAllow,
		PermCtx:                opt.PermCtx,
		ExecHost:               opt.ExecHost,
	})
	installed = append(installed, "Bash")

	// Background-task partner tools — only register when the store
	// is wired so the model isn't told these tools exist when
	// they'd just soft-error.
	if opt.BgTasks != nil {
		reg.Register(BashOutputTool{BgTasks: opt.BgTasks})
		installed = append(installed, "BashOutput")
		reg.Register(KillBashTool{BgTasks: opt.BgTasks})
		installed = append(installed, "KillBash")
		// Unified task-control surface (TaskOutput / TaskStop) —
		// task-type agnostic names that live alongside BashOutput /
		// KillBash so prompts trained on either set work; the new
		// tools accept the same task IDs.
		reg.Register(TaskOutputTool{BgTasks: opt.BgTasks})
		installed = append(installed, "TaskOutput")
		reg.Register(TaskStopTool{BgTasks: opt.BgTasks})
		installed = append(installed, "TaskStop")
	}

	reg.Register(WebFetchTool{})
	installed = append(installed, "WebFetch")

	var prov SearchProvider
	if opt.SearxNGURL != "" {
		prov = &searxngAdapter{client: searxng.New(opt.SearxNGURL)}
	}
	reg.Register(WebSearchTool{Provider: prov})
	installed = append(installed, "WebSearch")

	reg.Register(LSPTool{Backend: opt.LSPBackend})
	installed = append(installed, "LSP")
	return installed
}

// searxngAdapter bridges the existing searxng.Client into the
// SearchProvider interface this package owns.
type searxngAdapter struct {
	client *searxng.Client
}

func (a *searxngAdapter) WebSearch(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	hits, err := a.client.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchResult{
			Title:   h.Title,
			URL:     h.URL,
			Snippet: h.Snippet,
		})
	}
	return out, nil
}
