// biumindkit adapter — projects brain.tools.Tool onto biumindkit.Tool.
//
// S4-2: brain S4 chat mode replaces the local AgentLoop with biumindkit's
// engine. biumindkit needs `[]biumindkit.Tool` (Run signature returns
// `string`); brain has `tools.Tool` with `Invoker` returning `(any, error)`
// JSON-encoded. This adapter normalises the two — pass the brain Tool's
// Descriptor + Invoke through, marshal output to JSON string.
//
// Use:
//
//	bkTools := []biumindkit.Tool{}
//	for _, t := range reg.AllTools() {
//	    bkTools = append(bkTools, tools.BiumindkitAdapter(t))
//	}
//	bkOpts.ExtraTools = bkTools

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// BiumindkitAdapter wraps a brain Tool so biumindkit's engine can call
// it. Returns nil if t.Invoke is nil (descriptor-only client tool —
// biumindkit can't dispatch those, the call would explode at runtime).
func BiumindkitAdapter(t Tool) biumindkit.Tool {
	if t.Invoke == nil {
		return nil
	}
	def := biumindkit.ToolDef{
		Name:        t.Name,
		Description: t.Description,
		// S3 P0-1: project the brain Tool's ReadOnly flag through to
		// biumindkit. Read-only tools (wiki_search / memory_recall /
		// websearch / time_now) stay IsReadOnly=true + concurrency-safe;
		// mutating wiki write tools (create/update/merge_page) become
		// IsReadOnly=false + concurrency-unsafe so biumindkit knows they
		// are side-effecting. RunV2 currently runs BypassPermissions, so
		// this flag is informational there; write-tool safety rests on
		// the store-layer create-only hard gate + page_revisions rollback,
		// not on biumindkit permission.
		IsReadOnly:        t.ReadOnly,
		IsConcurrencySafe: t.ReadOnly,
	}
	if len(t.InputSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
			def.InputSchema = schema
		}
	}
	def.Run = func(ctx context.Context, args map[string]any) (string, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("biumindkit_adapter: marshal args: %w", err)
		}
		out, invokeErr := t.Invoke(ctx, raw)
		// 即使有错也尝试 marshal —— Invoker 约定是错时 out 可能为空字符串
		// 或 nil，给 LLM 看到「something went wrong」即可
		if invokeErr != nil {
			return "", invokeErr
		}
		// 已经是 string —— 直接透传，避免被 json.Marshal 加双引号
		if s, ok := out.(string); ok {
			return s, nil
		}
		bytes, err := json.Marshal(out)
		if err != nil {
			return "", fmt.Errorf("biumindkit_adapter: marshal result: %w", err)
		}
		return string(bytes), nil
	}
	return biumindkit.NewTool(def)
}

// AvailableForBiumindkit projects every cloud-runtime tool in the
// registry to biumindkit.Tool. Skips descriptor-only client tools
// (their Invoke is nil — biumindkit can't dispatch them).
//
// allow is the chat-mode whitelist (see chatmode.go): a nil allow keeps
// every cloud tool; a non-nil allow is default-deny — only tools whose
// name is in the set survive. The RunV2 chat kernel passes its
// AgentLoop.ChatToolAllowlist here.
func (r *Registry) AvailableForBiumindkit(allow map[string]struct{}) []biumindkit.Tool {
	return r.AvailableForBiumindkitGuarded(allow, nil)
}

// AvailableForBiumindkitGuarded is AvailableForBiumindkit with a per-tool
// transform applied before adaptation. P2 #19 RunV2 wiring (agent-42
// leftover): chat.RunV2 passes retrievalGuard.WrapTool so retrieval-class
// tools are gated behind the per-run budget / signature-dedup / no-yield
// guard even though the tool loop runs inside biumindkit. nil wrap =
// plain projection, identical to AvailableForBiumindkit.
func (r *Registry) AvailableForBiumindkitGuarded(allow map[string]struct{}, wrap func(Tool) Tool) []biumindkit.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]biumindkit.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if !t.Runtime.AvailableIn(ExecutionCloud) {
			continue
		}
		if !chatAllows(allow, t.Name) {
			continue
		}
		if wrap != nil {
			t = wrap(t)
		}
		ad := BiumindkitAdapter(t)
		if ad != nil {
			out = append(out, ad)
		}
	}
	return out
}

// ErrAdapterNoInvoker is reserved for callers that try to adapt a
// descriptor-only tool (which can't be dispatched in-process by
// biumindkit). Currently BiumindkitAdapter returns nil instead of
// erroring; callers can sentinel-check with errors.Is when we promote
// this to a return.
var ErrAdapterNoInvoker = errors.New("tools: cannot adapt descriptor-only tool to biumindkit")
